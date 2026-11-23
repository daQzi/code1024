package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const relayReportSource = `export const meta={apiVersion:1,key:"relay-report-fixture",name:"Report",version:"1.0.0",author:{name:"Fixture"},allowedHosts:["reports.example.com"]};
export function report(ctx,record){
 if(ctx.stage!=="report" || !["openai","openai_responses"].includes(ctx.format) || record.task!==undefined || ctx.channel.id!==17 || ctx.channel.type!==61)throw Error("wrong report");
 utils.fetch({url:"https://reports.example.com/callback",method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({ctx,record})});
}`

type relayReportHTTP struct {
	HTTPUpstream
	upstream HTTPUpstream
	reports  *taskReportHTTP
}

func (h *relayReportHTTP) Do(req *http.Request, proxy string, accountID int64, concurrency int) (*http.Response, error) {
	if req.URL.Hostname() == "reports.example.com" {
		return h.reports.Do(req, proxy, accountID, concurrency)
	}
	return h.upstream.Do(req, proxy, accountID, concurrency)
}

func relayReportFixture(t *testing.T, path string) (*NewAPIPluginService, *Account, *generationFixtureHTTP, *RequestPluginPlan, *taskReportHTTP) {
	t.Helper()
	s, account, upstream, plan := providerPolicyFixture(t, providerConverterSource(), relayReportSource, []string{"report"}, path)
	h := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 16)}
	s.gateway.httpUpstream = &relayReportHTTP{upstream: s.gateway.httpUpstream, reports: h}
	s.taskReports = newTaskReportExecutor(s)
	t.Cleanup(s.taskReports.close)
	return s, account, upstream, plan, h
}

func TestProviderHTTPReportFinalResponseAndAsyncSettlement(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
		for _, stream := range []bool{false, true} {
			t.Run(path+map[bool]string{false: "/json", true: "/sse"}[stream], func(t *testing.T) {
				s, account, upstream, plan, h := relayReportFixture(t, path)
				upstream.response = `{"reply":"hello","input":12,"output":5}`
				if stream {
					upstream.contentType = "text/event-stream"
					upstream.response = "data: {\"seq\":0,\"text\":\"hello\"}\n\ndata: {\"seq\":1,\"finished\":true,\"input\":12,\"output\":5}\n\n"
				}
				body := []byte(`{"model":"public-text","messages":[],"input":"hello","stream":` + map[bool]string{true: "true", false: "false"}[stream] + `}`)
				c, recorder := providerContext(path, string(body))
				ctx := WithRequestPluginPlan(c.Request.Context(), plan)
				r := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "fixture-user")
				require.NotNil(t, r)
				c.Writer = r
				result, err := s.gateway.forwardNewAPIProvider(ctx, c, account, body, path, "")
				require.NoError(t, err)
				require.NotNil(t, result.pluginReportBilling)
				r.Finish(body)
				r.Finish(body)
				require.EqualValues(t, 1, s.TaskReportStats().Accepted)
				require.Empty(t, h.requests, "report must await billing, not replace an unknown cost with zero")
				// The handler is now over; only immutable snapshots may survive.
				plan.call.Model = "edited-after-request"
				plan.items[0].Config = json.RawMessage(`{"private":"changed"}`)
				result.pluginReportBilling.complete(decimal.RequireFromString("0.12345678"), true)
				payload := awaitTaskReport(t, h)
				var record relayReportRecord
				require.NoError(t, json.Unmarshal(payload["record"], &record))
				require.Equal(t, "public-text", gjson.GetBytes(payload["ctx"], "model").String())
				require.Equal(t, "vendor-text", gjson.GetBytes(payload["ctx"], "upstreamModel").String())
				require.Equal(t, "fixture-user", record.User.Username)
				require.EqualValues(t, recorder.Body.Len(), record.Response.Bytes)
				require.False(t, record.Response.Truncated)
				require.Equal(t, 12, record.Usage["promptTokens"])
				require.Equal(t, "0.12345678", string(record.Cost["usd"]))
				require.Equal(t, "61728", string(record.Cost["quota"]))
				if stream {
					require.NotEmpty(t, record.Response.Events)
					require.Empty(t, record.Response.Body)
				} else {
					require.JSONEq(t, recorder.Body.String(), string(record.Response.Body))
				}
				raw, _ := json.Marshal(payload)
				for _, secret := range []string{"upstream-fixture-key", "downstream-secret", "downstream-only", "private-upstream", "edited-after-request", "changed"} {
					require.NotContains(t, string(raw), secret)
				}
				require.Nil(t, plan.report)
			})
		}
	}
}

func TestProviderHTTPReportFailureFallbackAndUnconfirmedBilling(t *testing.T) {
	for _, scenario := range []string{"no-result", "other-provider", "native", "failed-billing", "timeout", "shutdown"} {
		t.Run(scenario, func(t *testing.T) {
			s, account, _, plan, h := relayReportFixture(t, newAPIProviderChat)
			ctx := WithRequestPluginPlan(context.Background(), plan)
			c, _ := providerContext(newAPIProviderChat, `{}`)
			r := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "")
			require.NotNil(t, r)
			beginRelayReportAttempt(ctx, account, "vendor-text", true)
			if scenario == "native" {
				beginRelayReportAttempt(ctx, &Account{ID: 18, Platform: PlatformOpenAI}, "", false)
			}
			if scenario == "other-provider" {
				beginRelayReportAttempt(ctx, &Account{ID: 18, Platform: PlatformOpenAI}, "vendor-other", true)
			}
			if scenario == "timeout" {
				job := &relayReportJob{billing: &relayReportBilling{done: make(chan struct{})}}
				require.False(t, s.taskReports.awaitRelayBilling(job, time.Millisecond))
				require.EqualValues(t, 1, s.TaskReportStats().Dropped)
				r.targets = nil
			} else if scenario == "failed-billing" || scenario == "shutdown" {
				result := &OpenAIForwardResult{}
				r.result(result)
				if scenario == "failed-billing" {
					result.pluginReportBilling.complete(decimal.Zero, false)
				}
			}
			c.Writer = r
			c.JSON(403, map[string]any{"error": map[string]any{"type": "request_processing_error", "code": "rejected", "message": "Safe policy rejection"}})
			r.Finish([]byte(`{"prompt":"client-input"}`))
			if scenario == "no-result" {
				payload := awaitTaskReport(t, h)
				require.Equal(t, "0", gjson.GetBytes(payload["record"], "cost.usd").String())
				require.Equal(t, "Safe policy rejection", gjson.GetBytes(payload["record"], "error.message").String())
			} else if scenario == "failed-billing" {
				require.Eventually(t, func() bool { return s.TaskReportStats().Dropped == 1 }, time.Second, time.Millisecond)
				require.Empty(t, h.requests)
			} else if scenario == "shutdown" {
				s.taskReports.close()
				require.EqualValues(t, 1, s.TaskReportStats().Dropped)
				require.Empty(t, h.requests)
			} else {
				require.Zero(t, s.TaskReportStats().Accepted)
			}
		})
	}
}

func TestProviderHTTPReportBillingFactsFromRecordUsage(t *testing.T) {
	for _, scenario := range []string{"committed", "duplicate", "failed", "simple", "early-error", "legacy"} {
		t.Run(scenario, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			billingRepo := &openAIRecordUsageBillingRepoStub{}
			svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			if scenario == "duplicate" {
				billingRepo.result = &UsageBillingApplyResult{Applied: false}
			}
			if scenario == "failed" {
				billingRepo.err = errors.New("ledger unavailable")
			}
			if scenario == "simple" {
				svc.cfg.RunMode = config.RunModeSimple
			}
			if scenario == "legacy" {
				svc.usageBillingRepo = nil
			}
			b := &relayReportBilling{done: make(chan struct{})}
			input := &OpenAIRecordUsageInput{Result: &OpenAIForwardResult{RequestID: "report-ledger", Model: "gpt-5.1", Usage: OpenAIUsage{InputTokens: 1200, OutputTokens: 300}, pluginReportBilling: b}, APIKey: &APIKey{ID: 1004}, User: &User{ID: 2004}, Account: &Account{ID: 3004}}
			if scenario == "early-error" {
				input.Account.ParentAccountID = i64p(99999)
				svc.accountRepo = &openAIRecordUsageAccountRepoStub{}
			}
			err := svc.RecordUsage(context.Background(), input)
			if scenario == "failed" || scenario == "early-error" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			select {
			case <-b.done:
			default:
				t.Fatal("billing completion not signalled")
			}
			switch scenario {
			case "committed":
				require.True(t, b.confirmed)
				require.True(t, b.amount.Equal(decimal.NewFromFloat(usageRepo.lastLog.ActualCost).Round(8)))
				require.Positive(t, b.amount.Sign())
			case "simple":
				require.True(t, b.confirmed)
				require.True(t, b.amount.IsZero())
			default:
				require.False(t, b.confirmed)
			}
			b.complete(decimal.NewFromInt(999), true)
			require.False(t, b.amount.Equal(decimal.NewFromInt(999)), "completion is immutable")
		})
	}
}

func TestProviderHTTPReportCaptureProjectionAndAdmissionBounds(t *testing.T) {
	s, account, _, plan, _ := relayReportFixture(t, newAPIProviderChat)
	ctx := WithRequestPluginPlan(context.Background(), plan)
	c, recorder := providerContext(newAPIProviderChat, `{}`)
	r := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "")
	require.NotNil(t, r)
	beginRelayReportAttempt(ctx, account, "vendor-text", true)
	payload := strings.Repeat("a", relayReportResponseBytes+123)
	n, err := r.WriteString(payload)
	require.NoError(t, err)
	require.Equal(t, len(payload), n)
	require.Equal(t, payload, recorder.Body.String())
	require.Len(t, r.kept, relayReportResponseBytes)
	r.service.taskReports.mu.Lock()
	r.service.taskReports.bytes = taskReportQueueBytes
	r.service.taskReports.mu.Unlock()
	r.Finish([]byte(`{}`))
	require.EqualValues(t, 1, s.TaskReportStats().Dropped)
	s.taskReports.mu.Lock()
	s.taskReports.bytes = 0
	s.taskReports.mu.Unlock()
	// A report is best effort: unavailable ingress memory does not reject a
	// request or install a writer which cannot safely capture its response.
	budget := pkghttputil.NewBodyMemoryBudget(1)
	bounded, release := pkghttputil.WithRequestBodyMemoryBudget(ctx, budget)
	defer release()
	require.Nil(t, s.BeginRequestPluginHTTPReport(bounded, c.Writer, ""))
	require.EqualValues(t, 2, s.TaskReportStats().Dropped)
	require.NoError(t, pkghttputil.ReserveRequestBodyMemory(bounded, 1))
}

func TestProviderHTTPReportSSEFramesAndBodyLimits(t *testing.T) {
	input := "event: delta\r\ndata: {\"text\":\"hello\"}\r\n\n: ping\n\ndata: [DONE]\n\ndata: unfinished"
	events, truncated := relayReportEvents([]byte(input), false)
	require.True(t, truncated)
	require.Len(t, events, 1)
	require.Equal(t, "delta", events[0].Event)
	require.JSONEq(t, `{"text":"hello"}`, string(events[0].Data))
	events, truncated = relayReportEvents([]byte(strings.Repeat("data: 1\n\n", 1025)), false)
	require.True(t, truncated)
	require.Len(t, events, 1024)
	for _, mediaType := range []string{"application/json", "application/problem+json"} {
		require.JSONEq(t, `{"message":"hello"}`, string(relayReportBody(mediaType, []byte(`{"message":"hello"}`))))
	}
	require.Equal(t, `"hello"`, string(relayReportBody("text/plain", []byte("hello"))))
	require.Nil(t, relayReportBody("video/mp4", []byte("binary")))
	require.Nil(t, relayReportBody("text/plain", []byte{0xff}))
	require.NoError(t, validateRequestPluginStages([]string{"request", "outbound", "egress", "reply", "report"}, []string{"openai"}, "account"))
	require.NoError(t, validateRequestPluginStages([]string{"report"}, []string{"openai"}, "global"))
}

func TestProviderHTTPReportCapturesFilteredReplyAndPolicyRecords(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
		t.Run(path, func(t *testing.T) {
			source, err := builtin.Source("request-redact")
			require.NoError(t, err)
			source = strings.Replace(source, "apiVersion: 1,", `apiVersion: 1, allowedHosts: ["reports.example.com"],`, 1)
			source += relayReportSource[strings.Index(relayReportSource, "export function report"):]
			s, account, upstream, plan := providerPolicyFixture(t, providerConverterSource(), source, []string{"reply", "report"}, path)
			plan.items[0].Config, err = s.validateRequestPluginStageConfig(context.Background(), plan.items[0].Version, json.RawMessage(`{"replies":true,"finalCheck":"off","rules":[{"pattern":"secret"}]}`), false, true)
			require.NoError(t, err)
			h := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 16)}
			s.gateway.httpUpstream = &relayReportHTTP{upstream: s.gateway.httpUpstream, reports: h}
			s.taskReports = newTaskReportExecutor(s)
			t.Cleanup(s.taskReports.close)
			upstream.response = `{"reply":"hello secret","input":12,"output":5}`
			body := []byte(`{"model":"public-text","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,YWJj"}}]}],"input":"hello"}`)
			c, recorder := providerContext(path, string(body))
			ctx := WithRequestPluginPlan(c.Request.Context(), plan)
			r := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "")
			c.Writer = r
			result, err := s.gateway.forwardNewAPIProvider(ctx, c, account, body, path, "")
			require.NoError(t, err)
			require.NotContains(t, recorder.Body.String(), "secret")
			r.Finish(body)
			result.pluginReportBilling.complete(decimal.RequireFromString("0.1"), true)
			payload := awaitTaskReport(t, h)
			record := payload["record"]
			require.JSONEq(t, recorder.Body.String(), gjson.GetBytes(record, "response.body").Raw)
			require.NotContains(t, string(record), "secret")
			require.NotContains(t, string(record), "YWJj")
			require.Equal(t, "[media:0]", gjson.GetBytes(record, "request.body.messages.0.content.0.image_url.url").String())
			require.Equal(t, "image/png", gjson.GetBytes(record, "request.media.0.mimeType").String())
			require.Equal(t, "reply", gjson.GetBytes(record, "requestPlugins.0.stage").String())
			require.Equal(t, "continue", gjson.GetBytes(record, "requestPlugins.0.action").String())
			require.Positive(t, gjson.GetBytes(record, "requestPlugins.0.effects").Int())
		})
	}
}

func TestProviderHTTPReportRenewalReportsOnlyFinalAttempt(t *testing.T) {
	s, account, _, plan := providerPolicyFixture(t, providerExchangeSource(providerFixtureSource), relayReportSource, []string{"report"}, newAPIProviderChat)
	h := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 16)}
	upstream := &providerCredentialHTTP{rejectStatus: 419, rejections: 1}
	s.gateway.httpUpstream = &relayReportHTTP{upstream: upstream, reports: h}
	s.taskReports = newTaskReportExecutor(s)
	t.Cleanup(s.taskReports.close)
	body := []byte(`{"model":"public-text","messages":[]}`)
	c, _ := providerContext(newAPIProviderChat, string(body))
	ctx := WithRequestPluginPlan(c.Request.Context(), plan)
	r := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "")
	c.Writer = r
	result, err := s.gateway.forwardNewAPIProvider(ctx, c, account, body, newAPIProviderChat, "")
	require.NoError(t, err)
	require.Equal(t, 2, upstream.chats)
	r.Finish(body)
	result.pluginReportBilling.complete(decimal.Zero, true)
	payload := awaitTaskReport(t, h)
	require.EqualValues(t, 1, gjson.GetBytes(payload["ctx"], "attempt").Int())
	require.Eventually(t, func() bool { return s.TaskReportStats().Completed == 1 }, time.Second, time.Millisecond)
	require.Empty(t, h.requests)
}

func TestProviderHTTPReportSharedQueueOverloadAndShutdown(t *testing.T) {
	s, account, _, plan, h := relayReportFixture(t, newAPIProviderChat)
	h.block = true
	h.started = make(chan struct{}, 2)
	enqueue := func() {
		c, _ := providerContext(newAPIProviderChat, `{}`)
		ctx := WithRequestPluginPlan(c.Request.Context(), plan)
		r := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "")
		require.NotNil(t, r)
		beginRelayReportAttempt(ctx, account, "vendor-text", true)
		_, err := r.WriteString(`{}`)
		require.NoError(t, err)
		r.Finish([]byte(`{}`))
	}
	for range 2 {
		enqueue()
	}
	for range 2 {
		select {
		case <-h.started:
		case <-time.After(5 * time.Second):
			t.Fatal("report HTTP not started")
		}
	}
	for range taskReportQueueItems + 5 {
		enqueue()
	}
	require.Positive(t, s.TaskReportStats().Dropped)
	s.taskReports.mu.Lock()
	require.LessOrEqual(t, s.taskReports.bytes, int64(taskReportQueueBytes))
	s.taskReports.mu.Unlock()
	s.taskReports.close()
	s.taskReports.mu.Lock()
	require.Zero(t, s.taskReports.bytes)
	s.taskReports.mu.Unlock()
	before := s.TaskReportStats().Accepted
	enqueue()
	require.Equal(t, before, s.TaskReportStats().Accepted)
}

func TestProviderHTTPReportJSONDepthNodesAmbiguityAndFrameLines(t *testing.T) {
	for _, body := range []string{`{"a":1,"a":2}`, strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65), "[" + strings.Repeat("0,", 16384) + "0]"} {
		require.False(t, relayReportJSON([]byte(body)))
		require.True(t, gjson.ParseBytes(relayReportBody("application/json", []byte(body))).Type == gjson.String)
	}
	for _, body := range []string{`{"a":1}`, `null`, `123`, `["hello"]`} {
		require.True(t, relayReportJSON([]byte(body)))
	}
	events, truncated := relayReportEvents([]byte(strings.Repeat("data: x\n", 4097)+"\n"), false)
	require.Empty(t, events)
	require.True(t, truncated)
}

func BenchmarkRelayReportProjection(b *testing.B) {
	for _, size := range []int{64 << 10, 900 << 10} {
		body := []byte(`{"text":"` + strings.Repeat("a", size) + `"}`)
		stream := append(append([]byte("event: delta\ndata: "), body...), []byte("\n\n")...)
		b.Run(strconv.Itoa(size)+"/json", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				relayReportBody("application/json", body)
			}
		})
		b.Run(strconv.Itoa(size)+"/sse", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				relayReportEvents(stream, false)
			}
		})
	}
}

func TestNewAPIReportCostKeepsIntegerQuotaAndExactUSD(t *testing.T) {
	for _, input := range []struct {
		amount string
		quota  int64
	}{{"0", 0}, {"0.12345678", 61728}, {"0.12345799", 61729}, {"0.00000001", 0}} {
		cost := newAPIReportCost(decimal.RequireFromString(input.amount))
		raw, err := json.Marshal(cost)
		require.NoError(t, err)
		var typed struct {
			Quota int64       `json:"quota"`
			USD   json.Number `json:"usd"`
		}
		require.NoError(t, json.Unmarshal(raw, &typed), "quota must also decode into New API's integer record type")
		require.Equal(t, input.quota, typed.Quota)
		require.Equal(t, input.amount, typed.USD.String())
	}
}

func TestProviderHTTPReportSaveScopeAndNoDryRun(t *testing.T) {
	s, account, _, plan, _ := relayReportFixture(t, newAPIProviderChat)
	input := RequestPluginInstance{Name: "Provider report", PluginVersionID: 2, Scope: "account", AccountIDs: []int64{account.ID}, Formats: []string{"openai", "openai_responses"}, Stages: []string{"report"}, Config: json.RawMessage(`{}`)}
	saved, err := s.SaveRequestPluginInstance(context.Background(), input, 4)
	require.NoError(t, err)
	require.Equal(t, []string{"report"}, saved.Stages)
	_, err = s.DryRunRequestPluginStage(context.Background(), 2, json.RawMessage(`{}`), json.RawMessage(`{}`), plan.call, "report")
	require.Error(t, err)
}

func TestProviderHTTPReportRecordOverflowDropsOnceAndReleasesAdmission(t *testing.T) {
	s, account, _, plan, _ := relayReportFixture(t, newAPIProviderChat)
	ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), pkghttputil.NewBodyMemoryBudget(relayReportMemory))
	defer release()
	ctx = WithRequestPluginPlan(ctx, plan)
	c, _ := providerContext(newAPIProviderChat, `{}`)
	r := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "")
	require.NotNil(t, r)
	beginRelayReportAttempt(ctx, account, "vendor-text", true)
	plan.records = make([]RequestPluginRecord, relayReportMaxRecords)
	plan.appendReportRecords([]RequestPluginRecord{{Stage: "reply"}})
	plan.appendReportRecords([]RequestPluginRecord{{Stage: "reply"}})
	require.True(t, r.disabled)
	require.EqualValues(t, 1, s.TaskReportStats().Dropped)
	require.Len(t, plan.records, relayReportMaxRecords)
	r.Finish([]byte(`{}`))
	require.Zero(t, s.TaskReportStats().Accepted)
	require.NoError(t, pkghttputil.ReserveRequestBodyMemory(ctx, relayReportMemory))
}
