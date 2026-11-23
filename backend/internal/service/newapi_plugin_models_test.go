//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/stretchr/testify/require"
)

func providerModelsSource(converted bool) string {
	converter := "none"
	if converted {
		converter = "plugin"
	}
	route := fmt.Sprintf(`relay:[{incoming_path:"/v1/models",upstream_path:"/catalog",converter:%q,auth:{type:"query",name:"catalog_key",value:"{api_key}"}},`, converter)
	source := strings.Replace(providerFixtureSource, "relay:[", route, 1)
	source += `export function prepareRequest(){throw Error("management never prepares a relay");}`
	if !converted {
		return source
	}
	return source + `
export function convertResponse(ctx,resp){
 if(ctx.path!=="/v1/models" || ctx.config.label!=="saved")throw Error("management identity drifted");
 const b=JSON.parse(resp.body);
 if(b.error)return {error:{status:b.status||502,message:"private-"+ctx.config.label}};
 if(b.next)return {next:{path:b.next,body:{cursor:b.cursor}},state:{cursor:b.cursor}};
 if(b.expected!==undefined && ctx.state.cursor!==b.expected)throw Error("state lost");
 return {body:{object:"list",data:(b.ids||[]).map(id=>({id,secret:"not returned"}))}};
}`
}

func TestNewAPIPluginModelsNativeRouteAuthSigningAndSafeProjection(t *testing.T) {
	s, account, cache := providerBalanceFixture(t, providerModelsSource(false))
	s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/prefix/catalog", req.URL.Path)
		require.Equal(t, "upstream-fixture-key", req.URL.Query().Get("catalog_key"))
		require.Equal(t, "yes", req.URL.Query().Get("signed"))
		require.Empty(t, req.Header.Get("Authorization"))
		require.Empty(t, req.Header.Get("X-Vendor-Key"))
		require.Equal(t, "saved", req.Header.Get("X-Config"))
		require.Equal(t, http.MethodGet, req.Method)
		require.True(t, HTTPUpstreamPublicHostsOnly(req.Context()))
		require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
		return chainResponse(200, `{"data":[{"id":"one","private":"upstream-secret"},{"id":"one"},{"id":"two"}]}`), nil
	}}
	result, err := s.QueryNewAPIPluginModels(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"one", "two"}, result.Models)
	require.Equal(t, account.ID, result.AccountID)
	require.EqualValues(t, 1, result.PluginVersionID)
	require.False(t, result.FetchedAt.IsZero())
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "upstream-secret")
	require.Equal(t, []int64{17}, cache.releasedAccountIDs)
	require.Equal(t, "vendor-text", account.GetMappedModel("public-text"), "discovery never changes account mapping")
	require.Empty(t, s.repo.(*requestPluginFixtureRepo).runs)
}

func TestNewAPIPluginModelsConvertedChainSnapshotAndRenewal(t *testing.T) {
	for _, rejectAgain := range []bool{false, true} {
		t.Run(fmt.Sprint(rejectAgain), func(t *testing.T) {
			s, account, _ := providerBalanceFixture(t, providerExchangeSource(providerModelsSource(true)))
			var paths, tokens []string
			var exchanges, polls int
			var previous *providerChainBody
			s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
				if previous != nil {
					require.Positive(t, previous.closed.Load())
				}
				paths = append(paths, req.URL.Path)
				resp := chainResponse(200, `{}`)
				switch req.URL.Path {
				case "/prefix/token":
					exchanges++
					resp = chainResponse(200, fmt.Sprintf(`{"token":{"value":"token-%d"}}`, exchanges))
				case "/prefix/catalog":
					require.Equal(t, "upstream-fixture-key", req.URL.Query().Get("catalog_key"))
					resp = chainResponse(503, `{"next":"/catalog/page2","cursor":5}`)
					account.Credentials["api_key"] = "changed-key"
					account.Credentials[newAPIPluginConfigEncryptedKey] = `ENC:{"label":"changed"}`
					s.repo.(*requestPluginFixtureRepo).versions[1].Source = "invalid"
					s.repo.(*requestPluginFixtureRepo).versions[1].Enabled = false
				case "/prefix/catalog/page2":
					polls++
					require.Equal(t, http.MethodPost, req.Method)
					require.Equal(t, "upstream-fixture-key", req.URL.Query().Get("catalog_key"))
					raw, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					require.JSONEq(t, `{"cursor":5}`, string(raw))
					tokens = append(tokens, req.Header.Get("X-Token"))
					if polls == 1 || rejectAgain {
						resp = chainResponse(200, `{"error":true,"status":401}`)
					} else {
						resp = chainResponse(200, `{"ids":["a","b"],"expected":5}`)
					}
				default:
					t.Fatalf("unexpected model request: %s", req.URL.Path)
				}
				previous = resp.Body.(*providerChainBody)
				return resp, nil
			}}
			result, err := s.QueryNewAPIPluginModels(context.Background(), account.ID)
			if rejectAgain {
				require.Error(t, err)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.Equal(t, []string{"a", "b"}, result.Models)
			}
			require.Equal(t, []string{"/prefix/token", "/prefix/catalog", "/prefix/catalog/page2", "/prefix/token", "/prefix/catalog/page2"}, paths)
			require.Equal(t, []string{"token-1", "token-2"}, tokens)
			require.Positive(t, previous.closed.Load())
		})
	}
}

func TestNewAPIPluginModelsGuards(t *testing.T) {
	for _, scenario := range []string{"missing-route", "disabled", "memory", "busy", "bad-status", "null-list", "ambiguous-json", "private-error", "oversized", "foreign-next", "stream-next", "protected-header", "chain-limit"} {
		t.Run(scenario, func(t *testing.T) {
			source := providerModelsSource(true)
			if scenario == "ambiguous-json" {
				source = strings.Replace(source, `return {body:{object:"list",data:(b.ids||[]).map(id=>({id,secret:"not returned"}))}};`, `return {body:resp.body};`, 1)
			}
			if scenario == "missing-route" {
				source = providerFixtureSource
			}
			if scenario == "chain-limit" {
				source = strings.Replace(source, `if(b.next)return`, `if(true)return`, 1)
			}
			s, account, cache := providerBalanceFixture(t, source)
			if scenario == "disabled" {
				s.repo.(*requestPluginFixtureRepo).versions[1].Enabled = false
			}
			if scenario == "busy" {
				cache.acquireResult = false
			}
			if scenario == "foreign-next" || scenario == "stream-next" || scenario == "protected-header" {
				version := s.repo.(*requestPluginFixtureRepo).versions[1]
				step := `{url:"https://unapproved.invalid/catalog"}`
				if scenario == "stream-next" {
					step = `{path:"/catalog",stream:true}`
				}
				if scenario == "protected-header" {
					step = `{path:"/catalog",headers:{Authorization:"override"}}`
				}
				version.Source = strings.Replace(source, `if(b.next)return {next:{path:b.next,body:{cursor:b.cursor}},state:{cursor:b.cursor}};`, `return {next:`+step+`};`, 1)
			}
			calls := 0
			s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
				calls++
				switch scenario {
				case "bad-status":
					return chainResponse(500, `{"ids":["one"]}`), nil
				case "null-list":
					return chainResponse(200, `not-json`), nil
				case "private-error":
					return chainResponse(200, `{"error":true}`), nil
				case "oversized":
					return chainResponse(200, strings.Repeat("x", MaxRequestPluginBody+1)), nil
				case "ambiguous-json":
					return chainResponse(200, `{"data":[{"id":"a"}],"data":[]}`), nil
				default:
					return chainResponse(200, `{"next":"/catalog","cursor":2}`), nil
				}
			}}
			capacity := int64(newAPIProviderMemory)
			if scenario == "memory" {
				capacity = 1
			}
			budget := pkghttputil.NewBodyMemoryBudget(capacity)
			ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
			defer release()
			result, err := s.QueryNewAPIPluginModels(ctx, account.ID)
			require.Error(t, err)
			require.Nil(t, result)
			require.NotContains(t, err.Error(), "upstream-fixture-key")
			require.NotContains(t, err.Error(), "private-saved")
			if scenario == "missing-route" || scenario == "disabled" || scenario == "memory" || scenario == "busy" {
				require.Zero(t, calls)
			}
			if scenario == "foreign-next" || scenario == "stream-next" || scenario == "protected-header" {
				require.Equal(t, 1, calls)
			}
			if scenario == "chain-limit" {
				require.Equal(t, 33, calls)
			}
			check, done := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
			defer done()
			require.NoError(t, pkghttputil.ReserveRequestBodyMemory(check, capacity))
		})
	}
}

func TestNewAPIPluginModelsCatalogValidation(t *testing.T) {
	for _, body := range []string{`{}`, `{"data":null}`, `{"data":[null]}`, `{"data":[{"id":"a","id":"b"}]}`, `{"data":[],"data":null}`, `{"data":[{"id":"*"}]}`, `{"data":[{"id":" a"}]}`, `{"data":[{"id":"x\ny"}]}`} {
		_, err := pluginManagementModelIDs([]byte(body))
		require.Error(t, err, body)
	}
	empty, err := pluginManagementModelIDs([]byte(`{"data":[]}`))
	require.NoError(t, err)
	require.NotNil(t, empty)
	require.Empty(t, empty)
	_, err = pluginManagementModelIDs([]byte(`{"data":[` + strings.Repeat(`{"id":"a"},`, 4096) + `{"id":"last"}]}`))
	require.Error(t, err)
}

func TestNewAPIPluginModelsCancellationReleasesAdmission(t *testing.T) {
	s, account, cache := providerBalanceFixture(t, providerModelsSource(false))
	reader, writer := io.Pipe()
	defer writer.Close()
	started := make(chan struct{})
	s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
		close(started)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: reader}, nil
	}}
	budget := pkghttputil.NewBodyMemoryBudget(newAPIProviderMemory)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, release := pkghttputil.WithRequestBodyMemoryBudget(ctx, budget)
	defer release()
	done := make(chan error, 1)
	go func() { _, err := s.QueryNewAPIPluginModels(ctx, account.ID); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("model query did not begin")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("model query retained its response")
	}
	require.Equal(t, []int64{17}, cache.releasedAccountIDs)
	check, finish := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
	defer finish()
	require.NoError(t, pkghttputil.ReserveRequestBodyMemory(check, newAPIProviderMemory))
}
