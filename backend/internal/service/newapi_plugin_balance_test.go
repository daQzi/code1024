package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/stretchr/testify/require"
)

const providerBalanceHooks = `
export function buildBalanceRequest(ctx){
 if(ctx.config.label!=="saved")throw Error("wrong config");
 return {path:"/balance/start",headers:{Authorization:"Bearer "+(ctx.token?.value || ctx.apiKey)},state:{count:0,label:ctx.config.label}};
}
export function parseBalance(ctx,resp){
 const b=JSON.parse(resp.body),s=ctx.state;
 if(ctx.config.label!=="saved" || s.label!=="saved")throw Error("configuration drifted");
 if(b.next)return {next:{path:"/balance/"+b.next,body:{page:s.count+1},headers:{Authorization:"Bearer "+(ctx.token?.value || ctx.apiKey)}},state:{count:s.count+1,label:s.label}};
 if(resp.status>=400)return {error:{message:"private-vendor-error-"+ctx.apiKey}};
 if(b.expected!==undefined && s.count!==b.expected)throw Error("wrong balance state");
 return {amount:b.amount,currency:b.currency || "USD",unlimited:b.unlimited || false,windows:b.windows || []};
}`

func providerBalanceFixture(t *testing.T, source string) (*NewAPIPluginService, *Account, *stubConcurrencyCacheForTest) {
	t.Helper()
	s, account, _ := providerFixture(t, source)
	s.gateway.accountRepo = &stubOpenAIAccountRepo{accounts: []Account{*account}}
	cache := &stubConcurrencyCacheForTest{acquireResult: true}
	s.gateway.concurrencyService = NewConcurrencyService(cache)
	return s, account, cache
}

func TestNewAPIPluginBalanceStateSigningSnapshotAndNoWrites(t *testing.T) {
	s, account, cache := providerBalanceFixture(t, providerFixtureSource+providerBalanceHooks)
	var paths, bodies []string
	var previous *providerChainBody
	s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
		require.True(t, HTTPUpstreamPublicHostsOnly(req.Context()))
		require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
		if previous != nil {
			require.Positive(t, previous.closed.Load())
		}
		raw, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		paths, bodies = append(paths, req.URL.Path), append(bodies, string(raw))
		require.Equal(t, "Bearer upstream-fixture-key", req.Header.Get("Authorization"))
		require.Empty(t, req.Header.Get("X-Vendor-Key"), "management calls do not inherit a relay route's auth")
		digest := sha256.Sum256(raw)
		require.Equal(t, hex.EncodeToString(digest[:]), req.Header.Get("X-Body-Digest"))
		require.Equal(t, "saved", req.Header.Get("X-Config"))
		resp := chainResponse(200, `{"next":"page2"}`)
		if len(paths) == 1 {
			require.Equal(t, http.MethodGet, req.Method)
			require.Empty(t, raw)
		} else {
			require.Equal(t, http.MethodPost, req.Method)
			require.JSONEq(t, `{"page":1}`, string(raw))
			resp = chainResponse(200, `{"amount":12.25,"currency":"CNY","expected":1,"windows":[{"name":{"zh":"每日额度","en":"Daily"},"used":2,"limit":20,"unit":"requests","resetAt":1800000000}]}`)
		}
		previous = resp.Body.(*providerChainBody)
		account.Credentials["api_key"] = "changed-key"
		account.Credentials[newAPIPluginConfigEncryptedKey] = `ENC:{"label":"changed"}`
		s.repo.(*requestPluginFixtureRepo).versions[1].Source = "invalid source"
		s.repo.(*requestPluginFixtureRepo).versions[1].Enabled = false
		return resp, nil
	}}
	result, err := s.QueryNewAPIPluginBalance(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"/prefix/balance/start", "/prefix/balance/page2"}, paths)
	require.Len(t, bodies, 2)
	require.Positive(t, previous.closed.Load())
	require.EqualValues(t, 17, result.AccountID)
	require.EqualValues(t, 1, result.PluginVersionID)
	require.Equal(t, 12.25, *result.Balance.Amount)
	require.Equal(t, "CNY", result.Balance.Currency)
	require.Len(t, result.Balance.Windows, 1)
	require.False(t, result.FetchedAt.IsZero())
	require.Equal(t, []int64{17}, cache.releasedAccountIDs)
	require.Empty(t, s.repo.(*requestPluginFixtureRepo).runs, "balance is not a generation or financial event")
}

func TestNewAPIPluginBalanceUnknownUnlimitedAndZero(t *testing.T) {
	for _, response := range []string{`{"amount":null,"windows":[{"name":"Daily","used":1,"limit":10}]}`, `{"amount":null,"unlimited":true}`, `{"amount":0}`} {
		t.Run(response, func(t *testing.T) {
			s, account, _ := providerBalanceFixture(t, providerFixtureSource+providerBalanceHooks)
			s.gateway.httpUpstream = &providerChainHTTP{send: func(*http.Request) (*http.Response, error) { return chainResponse(200, response), nil }}
			result, err := s.QueryNewAPIPluginBalance(context.Background(), account.ID)
			require.NoError(t, err)
			if response == `{"amount":0}` {
				require.NotNil(t, result.Balance.Amount)
				require.Zero(t, *result.Balance.Amount)
			} else {
				require.Nil(t, result.Balance.Amount)
			}
			require.Equal(t, strings.Contains(response, "true"), result.Balance.Unlimited)
		})
	}
}

func TestNewAPIPluginBalanceCredentialRenewalCurrentStepOnly(t *testing.T) {
	for _, rejectAgain := range []bool{false, true} {
		t.Run(fmt.Sprint(rejectAgain), func(t *testing.T) {
			s, account, cache := providerBalanceFixture(t, providerExchangeSource(providerFixtureSource+providerBalanceHooks))
			var paths, tokens []string
			var exchanges, polls int
			s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
				paths, tokens = append(paths, req.URL.Path), append(tokens, req.Header.Get("X-Token"))
				switch req.URL.Path {
				case "/prefix/token":
					exchanges++
					return chainResponse(200, fmt.Sprintf(`{"token":{"value":"token-%d"},"expiresAt":0}`, exchanges)), nil
				case "/prefix/balance/start":
					return chainResponse(200, `{"next":"page2"}`), nil
				case "/prefix/balance/page2":
					polls++
					if polls == 1 || rejectAgain {
						return chainResponse(401, `{}`), nil
					}
					return chainResponse(200, `{"amount":8,"expected":1}`), nil
				}
				t.Fatalf("unexpected path: %s", req.URL.Path)
				return nil, nil
			}}
			result, err := s.QueryNewAPIPluginBalance(context.Background(), account.ID)
			if rejectAgain {
				require.Error(t, err)
				require.Nil(t, result)
				require.NotContains(t, err.Error(), "upstream-fixture-key")
			} else {
				require.NoError(t, err)
				require.Equal(t, 8.0, *result.Balance.Amount)
			}
			require.Equal(t, []string{"/prefix/token", "/prefix/balance/start", "/prefix/balance/page2", "/prefix/token", "/prefix/balance/page2"}, paths)
			require.Equal(t, "token-1", tokens[2])
			require.Equal(t, "token-2", tokens[4])
			require.Equal(t, []int64{17}, cache.releasedAccountIDs)
		})
	}
}

func TestNewAPIPluginBalanceAdmissionAndResponseGuards(t *testing.T) {
	for _, scenario := range []string{"missing-hooks", "disabled", "busy", "memory", "unapproved-host", "fetch-denied", "bad-result", "empty-result", "oversize-response", "chain-limit", "vendor-error"} {
		t.Run(scenario, func(t *testing.T) {
			source := providerFixtureSource + providerBalanceHooks
			if scenario == "missing-hooks" {
				source = providerFixtureSource
			}
			if scenario == "unapproved-host" {
				source = strings.Replace(source, `path:"/balance/start"`, `url:"https://unapproved.example/balance"`, 1)
			}
			if scenario == "fetch-denied" {
				source = strings.Replace(source, `if(ctx.config.label!=="saved")`, `utils.fetch({url:ctx.baseUrl+"/forbidden"}); if(ctx.config.label!=="saved")`, 1)
			}
			if scenario == "bad-result" {
				source = strings.Replace(source, `amount:b.amount`, `amount:NaN`, 1)
			}
			s, account, cache := providerBalanceFixture(t, source)
			if scenario == "disabled" {
				s.repo.(*requestPluginFixtureRepo).versions[1].Enabled = false
			}
			if scenario == "busy" {
				cache.acquireResult = false
			}
			ctx := context.Background()
			if scenario == "memory" {
				var release func()
				ctx, release = pkghttputil.WithRequestBodyMemoryBudget(ctx, pkghttputil.NewBodyMemoryBudget(1))
				defer release()
			}
			var sends int
			var bodies []*providerChainBody
			s.gateway.httpUpstream = &providerChainHTTP{send: func(*http.Request) (*http.Response, error) {
				sends++
				resp := chainResponse(200, `{"amount":2}`)
				switch scenario {
				case "empty-result":
					resp = chainResponse(200, `{"amount":null}`)
				case "oversize-response":
					resp = chainResponse(200, strings.Repeat("x", MaxRequestPluginBody+1))
				case "chain-limit":
					resp = chainResponse(200, `{"next":"again"}`)
				case "vendor-error":
					resp = chainResponse(500, `{"private":"upstream-fixture-key"}`)
				}
				bodies = append(bodies, resp.Body.(*providerChainBody))
				return resp, nil
			}}
			result, err := s.QueryNewAPIPluginBalance(ctx, account.ID)
			require.Error(t, err)
			require.Nil(t, result)
			require.NotContains(t, err.Error(), "upstream-fixture-key")
			if scenario == "memory" {
				require.ErrorIs(t, err, pkghttputil.ErrRequestBodyMemoryBudget)
			}
			if scenario == "chain-limit" {
				require.Equal(t, 33, sends)
			} else if scenario == "bad-result" || scenario == "empty-result" || scenario == "oversize-response" || scenario == "vendor-error" {
				require.Equal(t, 1, sends)
			} else {
				require.Zero(t, sends)
			}
			for _, body := range bodies {
				require.Positive(t, body.closed.Load())
			}
		})
	}
}

func TestNewAPIPluginBalanceCancellationReleasesMemoryAndSlot(t *testing.T) {
	s, account, cache := providerBalanceFixture(t, providerFixtureSource+providerBalanceHooks)
	budget := pkghttputil.NewBodyMemoryBudget(newAPIProviderMemory)
	ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
	defer release()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	started := make(chan struct{})
	reader, writer := io.Pipe()
	defer writer.Close()
	s.gateway.httpUpstream = &providerChainHTTP{send: func(*http.Request) (*http.Response, error) {
		close(started)
		resp := chainResponse(200, "")
		resp.Body = reader
		return resp, nil
	}}
	done := make(chan error, 1)
	go func() { _, err := s.QueryNewAPIPluginBalance(ctx, account.ID); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("balance never started")
	}
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("balance read did not cancel")
	}
	require.Equal(t, []int64{17}, cache.releasedAccountIDs)
	other, otherRelease := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
	defer otherRelease()
	require.NoError(t, pkghttputil.ReserveRequestBodyMemory(other, newAPIProviderMemory))
}

func TestNewAPIPluginBalanceHTTPNeverHoldsWorkers(t *testing.T) {
	s, account, _ := providerBalanceFixture(t, providerFixtureSource+providerBalanceHooks)
	// No shared test-cache mutations while all four requests are in flight.
	s.gateway.accountRepo = &stubOpenAIAccountRepo{accounts: []Account{*account}}
	s.gateway.accountRepo.(*stubOpenAIAccountRepo).accounts[0].Concurrency = 0
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
		started <- struct{}{}
		select {
		case <-release:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
		return chainResponse(200, `{"amount":3}`), nil
	}}
	done := make(chan error, 4)
	for range 4 {
		go func() { _, err := s.QueryNewAPIPluginBalance(ctx, account.ID); done <- err }()
	}
	for range 4 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("balance calls failed to release workers before HTTP")
		}
	}
	hookCtx, hookCancel := context.WithTimeout(ctx, 3*time.Second)
	defer hookCancel()
	err := s.pool.With(hookCtx, s.repo.(*requestPluginFixtureRepo).versions[1].Source, func(*runner.Client) error { return nil })
	require.NoError(t, err, "all four HTTP waits must leave JavaScript capacity available")
	close(release)
	for range 4 {
		require.NoError(t, <-done)
	}
}
