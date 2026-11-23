//go:build unit

package service

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewAPIProviderContinuationFailureMustNotReplayInitialRequest(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses, newAPIProviderEmbeddings} {
		for _, failure := range []string{"http-500", "http-429", "transport"} {
			t.Run(path+"/"+failure, func(t *testing.T) {
				source := providerChainSource()
				if path == newAPIProviderEmbeddings {
					source = strings.ReplaceAll(source, newAPIProviderChat, newAPIProviderEmbeddings)
				}
				s, account, _ := providerFixture(t, source)
				var creates, polls int
				s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
					if req.URL.Path == "/prefix/chain/create" {
						creates++
						return chainResponse(202, `{"next":"poll"}`), nil
					}
					polls++
					if failure == "transport" {
						return nil, errors.New("connection reset after accepted generation")
					}
					status := 500
					if failure == "http-429" {
						status = 429
					}
					return chainResponse(status, `{"error":"try later"}`), nil
				}}
				body := `{"model":"public-text","messages":[],"input":"hello"}`
				c, _ := providerContext(path, body)
				_, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), path, "")
				require.Error(t, err)
				var retry *UpstreamFailoverError
				require.ErrorAs(t, err, &retry)
				require.False(t, retry.ShouldRetryNextAccount(), "the gateway must not restart a chain on another account")
				require.False(t, retry.RetryableOnSameAccount, "pool mode must not repeat the initial paid request either")
				require.Equal(t, 1, creates)
				require.Equal(t, 1, polls)
			})
		}
	}
}

func TestNewAPIProviderInitialRejectionRetainsExistingFailover(t *testing.T) {
	s, account, _ := providerFixture(t, providerChainSource())
	s.gateway.httpUpstream = &providerChainHTTP{send: func(*http.Request) (*http.Response, error) {
		return chainResponse(429, `{"error":"capacity unavailable"}`), nil
	}}
	body := `{"model":"public-text","messages":[]}`
	c, _ := providerContext(newAPIProviderChat, body)
	_, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderChat, "")
	var retry *UpstreamFailoverError
	require.ErrorAs(t, err, &retry)
	require.True(t, retry.ShouldRetryNextAccount(), "a rejected first request has not started a continuation")
}
