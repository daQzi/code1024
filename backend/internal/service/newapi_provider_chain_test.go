//go:build unit

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type providerChainHTTP struct {
	HTTPUpstream
	send         func(*http.Request) (*http.Response, error)
	observeProxy func(string)
}

func (h *providerChainHTTP) Do(req *http.Request, proxy string, _ int64, _ int) (*http.Response, error) {
	if h.observeProxy != nil {
		h.observeProxy(proxy)
	}
	return h.send(req)
}

type providerChainBody struct {
	io.Reader
	closed atomic.Int32
}

func (b *providerChainBody) Close() error { b.closed.Add(1); return nil }

func chainResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: &providerChainBody{Reader: strings.NewReader(body)}}
}

func providerChainSource() string {
	source := strings.ReplaceAll(providerFixtureSource, `upstream_path:`, `converter:"plugin",upstream_path:`) + `
export function convertRequest(ctx,req){return {path:"/chain/start",stream:false,body:{deployment:req.model},headers:{"X-First":"only"},state:{count:0,model:req.model,path:ctx.path,label:ctx.config.label}}}
export function prepareRequest(ctx,req){
 if(req.upstreamPath!=="/prefix/chain/start")throw Error("prepare reran on followup");
 return {path:"/chain/create",effects:[{type:"header.patch",set:{"X-Prepared":"first"}},{type:"json.patch",ops:[{op:"add",path:"/prepared",value:true}]}]};
}
export function convertResponse(ctx,resp){
 const b=JSON.parse(resp.body),s=ctx.state;
 if(ctx.config.label!=="saved" || s.label!=="saved")throw Error("configuration drifted");
 if(b.reject)return {error:{status:401,message:"token rejected"}};
 if(b.next){const n={path:"/chain/"+b.next,headers:{"X-Step":String(s.count+1)}};
  if(b.payload!==undefined)n.body=b.payload;
  if(b.stream!==undefined)n.stream=b.stream;
  if(b.delayMs)n.delayMs=b.delayMs;
  return {next:n,state:{...s,count:s.count+1}};
 }
 if(b.expected!==undefined && s.count!==b.expected)throw Error("wrong step state");
 if(resp.status>=400)return {error:{status:resp.status,message:"upstream failed"}};
 if(s.path==="/v1/responses")return {body:{id:"resp-chain",model:s.model,status:"completed",output:[{type:"message",role:"assistant",content:[{type:"output_text",text:b.reply}]}],usage:{input_tokens:12,output_tokens:5}}};
 return {body:{id:"chat-chain",model:s.model,choices:[{index:0,message:{role:"assistant",content:b.reply},finish_reason:"stop"}],usage:{prompt_tokens:12,completion_tokens:5}}};
}`
	return source + providerConverterHooks[strings.Index(providerConverterHooks, "export function convertStreamEvent"):]
}

func TestNewAPIProviderChainFinalResponseStateAndSigning(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", path, stream), func(t *testing.T) {
				s, account, _ := providerFixture(t, providerChainSource())
				proxyID := int64(9)
				account.ProxyID, account.Proxy = &proxyID, &Proxy{ID: proxyID, Protocol: "http", Host: "fixed-proxy.example", Port: 8080}
				expectedProxy := account.Proxy.URL()
				var requests []*http.Request
				var bodies []string
				var previous *providerChainBody
				s.gateway.httpUpstream = &providerChainHTTP{observeProxy: func(value string) { require.Equal(t, expectedProxy, value) }, send: func(req *http.Request) (*http.Response, error) {
					require.NoError(t, req.Context().Err())
					require.True(t, HTTPUpstreamPublicHostsOnly(req.Context()))
					require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
					if previous != nil {
						require.Positive(t, previous.closed.Load(), "close the prior HTTP body before the next send")
					}
					raw, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					requests, bodies = append(requests, req), append(bodies, string(raw))
					digest := sha256.Sum256(raw)
					require.Equal(t, hex.EncodeToString(digest[:]), req.Header.Get("X-Body-Digest"))
					require.Empty(t, req.Header.Get("X-Extra-Secret"))
					if path == newAPIProviderChat {
						require.Equal(t, "upstream-fixture-key", req.Header.Get("X-Vendor-Key"))
					} else {
						require.Equal(t, "upstream-fixture-key", req.URL.Query().Get("key"))
					}
					var resp *http.Response
					switch len(requests) {
					case 1:
						require.Equal(t, "/prefix/chain/create", req.URL.Path)
						require.Equal(t, "first", req.Header.Get("X-Prepared"))
						require.True(t, gjson.GetBytes(raw, "prepared").Bool())
						resp = chainResponse(200, `{"next":"poll1"}`)
					case 2:
						require.Equal(t, http.MethodGet, req.Method)
						require.Empty(t, raw)
						require.Empty(t, req.Header.Get("X-Prepared"))
						require.Empty(t, req.Header.Get("X-First"))
						resp = chainResponse(503, fmt.Sprintf(`{"next":"poll2","payload":{"job":"fixed-id"},"stream":%t}`, stream))
					case 3:
						require.Equal(t, "/prefix/chain/poll2", req.URL.Path)
						require.Equal(t, http.MethodPost, req.Method)
						require.JSONEq(t, `{"job":"fixed-id"}`, string(raw))
						require.Empty(t, req.Header.Get("X-Prepared"))
						resp = chainResponse(200, `{"reply":"hello","expected":2}`)
						if stream {
							resp.Header.Set("Content-Type", "application/x-ndjson")
							resp.Body = &providerChainBody{Reader: strings.NewReader("{\"seq\":2,\"text\":\"hello\"}\n{\"seq\":3,\"finished\":true,\"input\":12,\"output\":5}\n")}
						}
					default:
						t.Fatal("unexpected extra upstream request")
					}
					previous = resp.Body.(*providerChainBody)
					// Mutating the live registry/account must not alter this chain.
					s.repo.(*requestPluginFixtureRepo).versions[1].Enabled = false
					s.repo.(*requestPluginFixtureRepo).versions[1].Source = "invalid source"
					account.Credentials["api_key"] = "changed-key"
					account.Credentials["base_url"] = "https://changed.example"
					account.Proxy.Host = "changed-proxy.example"
					*account.ProxyID = 10
					return resp, nil
				}}
				body := fmt.Sprintf(`{"model":"public-text","messages":[],"input":"hello","stream":%t}`, stream)
				c, recorder := providerContext(path, body)
				result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), path, "")
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, recorder.Code)
				require.Len(t, requests, 3)
				require.Len(t, bodies, 3)
				require.Positive(t, previous.closed.Load())
				require.Equal(t, 5, result.Usage.OutputTokens)
				require.Equal(t, "public-text", result.Model)
				require.Equal(t, "vendor-text", result.BillingModel)
				require.Equal(t, "/prefix/chain/poll2", result.UpstreamEndpoint)
				require.Contains(t, recorder.Body.String(), "hello")
				assertNewAPIProviderExistingBilling(t, result, account)
			})
		}
	}
}

func TestNewAPIProviderChainCredentialRenewsOnlyRejectedStep(t *testing.T) {
	for _, scenario := range []string{"raw", "converted", "rejected-again"} {
		t.Run(scenario, func(t *testing.T) {
			s, account, _ := providerFixture(t, providerExchangeSource(providerChainSource()))
			var paths, tokens []string
			var exchanges, polls int
			s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
				paths, tokens = append(paths, req.URL.Path), append(tokens, req.Header.Get("X-Token"))
				switch req.URL.Path {
				case "/prefix/token":
					exchanges++
					return chainResponse(200, fmt.Sprintf(`{"token":{"value":"token-%d"},"expiresAt":0}`, exchanges)), nil
				case "/prefix/chain/create":
					return chainResponse(200, `{"next":"poll"}`), nil
				case "/prefix/chain/poll":
					polls++
					if polls == 1 || scenario == "rejected-again" {
						if scenario == "converted" {
							return chainResponse(200, `{"reject":true}`), nil
						}
						return chainResponse(401, `{"error":"expired"}`), nil
					}
					return chainResponse(200, `{"reply":"hello","expected":1}`), nil
				}
				t.Fatalf("unexpected path %s", req.URL.Path)
				return nil, nil
			}}
			body := `{"model":"public-text","messages":[]}`
			c, _ := providerContext(newAPIProviderChat, body)
			result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderChat, "")
			if scenario == "rejected-again" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
			}
			creates := 0
			for _, path := range paths {
				if path == "/prefix/chain/create" {
					creates++
				}
			}
			require.Equal(t, 1, creates, "never repeat the accepted generation")
			require.Equal(t, []string{"/prefix/token", "/prefix/chain/create", "/prefix/chain/poll", "/prefix/token", "/prefix/chain/poll"}, paths)
			require.Equal(t, "token-1", tokens[2])
			require.Equal(t, "token-2", tokens[4])
		})
	}
}

func TestNewAPIProviderChainLimitsAndResourceRelease(t *testing.T) {
	for _, scenario := range []string{"max-followups", "overload", "cancel-read", "cancel-delay"} {
		t.Run(scenario, func(t *testing.T) {
			s, account, _ := providerFixture(t, providerChainSource())
			budgetSize := int64(newAPIProviderMemory)
			if scenario == "overload" {
				budgetSize--
			}
			budget := pkghttputil.NewBodyMemoryBudget(budgetSize)
			ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
			defer release()
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			var count int
			var readStarted chan struct{}
			if scenario == "cancel-read" {
				readStarted = make(chan struct{})
			}
			s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
				count++
				if scenario == "cancel-read" {
					reader, writer := io.Pipe()
					t.Cleanup(func() { _ = writer.Close() })
					close(readStarted)
					resp := chainResponse(200, "")
					resp.Body = reader
					return resp, nil
				}
				if scenario == "cancel-delay" {
					cancel()
					return chainResponse(200, `{"next":"poll","delayMs":10000}`), nil
				}
				return chainResponse(200, `{"next":"poll"}`), nil
			}}
			if readStarted != nil {
				go func() {
					select {
					case <-readStarted:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			body := `{"model":"public-text","messages":[]}`
			c, recorder := providerContext(newAPIProviderChat, body)
			start := time.Now()
			result, err := s.gateway.forwardNewAPIProvider(ctx, c, account, []byte(body), newAPIProviderChat, "")
			require.Error(t, err)
			require.Nil(t, result)
			if scenario == "max-followups" {
				require.Equal(t, 33, count)
				require.Equal(t, http.StatusBadGateway, recorder.Code)
			} else if scenario == "overload" {
				require.Equal(t, 0, count)
				require.ErrorIs(t, err, pkghttputil.ErrRequestBodyMemoryBudget)
				require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
				require.Equal(t, "1", recorder.Header().Get("Retry-After"))
			} else {
				require.Less(t, time.Since(start), 3*time.Second)
			}
			other, done := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
			defer done()
			require.NoError(t, pkghttputil.ReserveRequestBodyMemory(other, budgetSize), "all conversion admission must be released on every exit")
		})
	}
}

func TestNewAPIProviderChainRunsPoliciesAndReportsOnce(t *testing.T) {
	policy := globalHTTPReportSource + `export function outbound(){return {action:"continue",effects:[{type:"header.patch",set:{"X-Policy":"first"}}]}}`
	s, account, _, plan := providerPolicyFixture(t, providerChainSource(), policy, []string{"outbound", "report"}, newAPIProviderChat)
	h := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 4)}
	s.taskReports = newTaskReportExecutor(s)
	t.Cleanup(s.taskReports.close)
	var sends int
	upstream := &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
		sends++
		if sends == 1 {
			require.Equal(t, "first", req.Header.Get("X-Policy"))
			return chainResponse(200, `{"next":"poll"}`), nil
		}
		require.Empty(t, req.Header.Get("X-Policy"))
		return chainResponse(200, `{"reply":"hello","expected":1}`), nil
	}}
	s.gateway.httpUpstream = &relayReportHTTP{upstream: upstream, reports: h}
	body := []byte(`{"model":"public-text","messages":[]}`)
	c, recorder := providerContext(newAPIProviderChat, string(body))
	ctx := WithRequestPluginPlan(c.Request.Context(), plan)
	r := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "")
	c.Writer = r
	result, err := s.gateway.forwardNewAPIProvider(ctx, c, account, body, newAPIProviderChat, "")
	require.NoError(t, err)
	r.Finish(body)
	require.Empty(t, h.requests)
	result.pluginReportBilling.complete(decimal.RequireFromString("0.019"), true)
	payload := awaitTaskReport(t, h)
	require.JSONEq(t, recorder.Body.String(), gjson.GetBytes(payload["record"], "response.body").Raw)
	require.Equal(t, "0.019", gjson.GetBytes(payload["record"], "cost.usd").String())
	require.Empty(t, h.requests)
	require.Equal(t, 2, sends)
	var outbound int
	for _, run := range s.repo.(*requestPluginFixtureRepo).runs {
		for _, record := range run.Records {
			if record.Stage == "outbound" {
				outbound++
			}
		}
	}
	require.Equal(t, 1, outbound, "a poll is not another channel attempt")
}

func TestNewAPIProviderChainFollowupGuards(t *testing.T) {
	s, account, _ := providerFixture(t, providerChainSource())
	initial, err := s.buildProviderRequest(context.Background(), account, newAPIProviderChat, "vendor-text", []byte(`{"model":"vendor-text"}`))
	require.NoError(t, err)
	for _, step := range []jsplugin.ConvertedRequest{
		{URL: "https://unapproved.example/poll"}, {Path: "/poll", Headers: map[string]string{"X-Vendor-Key": "override"}},
		{Path: "/poll", File: "original"}, {Path: "/poll", Form: &jsplugin.ConvertedForm{}},
		{Path: "/poll", WebSocket: &jsplugin.WebSocketRequest{}}, {Path: "/poll", ContentType: "text/plain"},
		{Path: "/poll", Body: []byte("invalid")}, {Path: "/poll", Body: []byte(strings.Repeat("x", MaxRequestPluginBody+1))},
	} {
		_, err := s.buildProviderFollowup(context.Background(), initial.chain, step, nil)
		require.Error(t, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, waitProviderFollowup(ctx, 10*time.Second), context.Canceled)
	require.Error(t, waitProviderFollowup(context.Background(), 11*time.Second))
}
