package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/newapi/replyfilter"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestProviderReplyOriginalPluginsJSONAndSSE(t *testing.T) {
	for _, key := range []string{"request-filter", "request-redact"} {
		for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
			for _, stream := range []bool{false, true} {
				t.Run(key+path+map[bool]string{false: "/json", true: "/sse"}[stream], func(t *testing.T) {
					source, err := builtin.Source(key)
					require.NoError(t, err)
					s, account, upstream, plan := providerPolicyFixture(t, providerConverterSource(), source, []string{"reply"}, path)
					config := `{"replies":true,"rules":[{"pattern":"secret"}]}`
					if key == "request-redact" {
						config = `{"replies":true,"finalCheck":"off","rules":[{"pattern":"secret"}]}`
					}
					plan.items[0].Config, err = s.validateRequestPluginStageConfig(context.Background(), plan.items[0].Version, json.RawMessage(config), false, true)
					require.NoError(t, err)
					// Deliberately change persistence after pinning. The accepted
					// request must keep its source, config and enabled snapshot.
					repo := s.repo.(*requestPluginFixtureRepo)
					repo.items[0].Enabled = false
					repo.items[0].ConfigEncrypted = `ENC:{"replies":false}`
					upstream.response = `{"reply":"你好🚀secret end","input":12,"output":5}`
					if stream {
						upstream.contentType = "text/event-stream"
						upstream.response = "data: {\"seq\":0,\"usage\":true,\"input\":12,\"output\":5}\n\n" +
							"data: {\"seq\":1,\"text\":\"你好🚀sec\"}\n\n" +
							"data: {\"seq\":2,\"text\":\"ret end\"}\n\n" +
							"data: {\"seq\":3,\"finished\":true,\"input\":12,\"output\":5}\n\n"
					}
					body := `{"model":"public-text","messages":[],"input":"hello","stream":` + map[bool]string{true: "true", false: "false"}[stream] + `}`
					c, recorder := providerContext(path, body)
					writer, request := c.Writer, c.Request
					result, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(body), path, "")
					require.NotNil(t, result)
					require.Equal(t, 12, result.Usage.InputTokens)
					require.Equal(t, 5, result.Usage.OutputTokens, "policy stops cannot discard measured usage")
					require.Same(t, writer, c.Writer)
					require.Same(t, request, c.Request)
					require.NoError(t, c.Request.Context().Err(), "local stop must not cancel the restored billing context")
					require.Len(t, upstream.requests, 1)
					require.Len(t, repo.runs, 2, "one admission and one aggregate result, no empty request-stage run")
					require.Equal(t, "started", repo.runs[0].Records[0].Action)
					require.Equal(t, "reply", repo.runs[1].Records[0].Stage)
					require.EqualValues(t, account.ID, repo.runs[1].Records[0].AccountID)
					output := recorder.Body.String()
					require.NotContains(t, output, "secret")
					if key == "request-filter" {
						var failure *RequestPluginFailure
						require.ErrorAs(t, err, &failure)
						require.True(t, failure.Rejected)
						require.Contains(t, output, "response_processing_rejected")
						require.NotContains(t, output, "[DONE]")
						require.Equal(t, "reject", repo.runs[1].Records[0].Action)
					} else {
						require.NoError(t, err)
						require.Contains(t, output, "***")
						require.Positive(t, repo.runs[1].Records[0].Effects)
					}
					audit, err := json.Marshal(repo.runs)
					require.NoError(t, err)
					for _, private := range []string{"secret", "你好", "pattern", "vendor-text", "rules", "upstream-fixture-key"} {
						require.NotContains(t, string(audit), private)
					}
				})
			}
		}
	}
}

func TestProviderReplyRenewalUsesFinalAttemptAndPinnedIdentity(t *testing.T) {
	source := providerPolicyMeta + `export function reply(ctx,res){
 if(ctx.stage!=="reply" || ctx.format!=="openai" || ctx.path!=="/v1/chat/completions" || ctx.model!=="public-text" || ctx.upstreamModel!=="vendor-text" || ctx.channel.id!==17 || ctx.attempt!==1 || ctx.user.id!==4 || ctx.token.id!==5 || ctx.instance.id!=="4")throw Error("wrong reply identity");
 return {action:"continue"};}`
	s, account, _, plan := providerPolicyFixture(t, providerExchangeSource(providerFixtureSource), source, []string{"reply"}, newAPIProviderChat)
	h := &providerCredentialHTTP{rejectStatus: 419, rejections: 1}
	s.gateway.httpUpstream = h
	body := `{"model":"public-text","messages":[]}`
	c, recorder := providerContext(newAPIProviderChat, body)
	_, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(body), newAPIProviderChat, "")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 2, h.chats)
	runs := s.repo.(*requestPluginFixtureRepo).runs
	require.Len(t, runs, 3)
	require.Equal(t, "started", runs[0].Records[0].Action)
	require.Equal(t, "unchecked", runs[1].Records[0].Action)
	require.Equal(t, "started", runs[1].Records[1].Action)
	require.Equal(t, 1, runs[1].Records[1].Attempt)
	require.Equal(t, "continue", runs[2].Records[0].Action)
	require.Equal(t, 1, runs[2].Records[0].Attempt)
}

func TestProviderReplyAdmissionAuditCapacityAndInstanceBounds(t *testing.T) {
	for _, boundary := range []string{"audit", "memory", "instances"} {
		t.Run(boundary, func(t *testing.T) {
			s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, providerPolicyMeta+`export function reply(){return {action:"pass"}}`, []string{"reply"}, newAPIProviderChat)
			repo := s.repo.(*requestPluginFixtureRepo)
			body := `{"model":"public-text","messages":[]}`
			c, recorder := providerContext(newAPIProviderChat, body)
			ctx := WithRequestPluginPlan(c.Request.Context(), plan)
			if boundary == "audit" {
				repo.runErr = errors.New("private database error")
			} else if boundary == "memory" {
				var release func()
				ctx, release = pkghttputil.WithRequestBodyMemoryBudget(ctx, pkghttputil.NewBodyMemoryBudget(16<<20))
				defer release()
			} else {
				for range replyfilter.MaxInstances {
					plan.items = append(plan.items, plan.items[0])
				}
			}
			_, err := s.gateway.forwardNewAPIProvider(ctx, c, account, []byte(body), newAPIProviderChat, "")
			require.Error(t, err)
			require.Empty(t, upstream.requests, "admission must finish before the model is called")
			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			require.NotContains(t, recorder.Body.String(), "private")
		})
	}
}

type replyAuditFailureHTTP struct {
	HTTPUpstream
	inner *generationFixtureHTTP
	repo  *requestPluginFixtureRepo
}

func (h *replyAuditFailureHTTP) Do(req *http.Request, proxy string, accountID int64, concurrency int) (*http.Response, error) {
	h.repo.runErr = errors.New("terminal database unavailable")
	return h.inner.Do(req, proxy, accountID, concurrency)
}

func TestProviderReplyTerminalAuditFailureIsVisibleAfterBytesSent(t *testing.T) {
	s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, providerPolicyMeta+`export function reply(){return {action:"continue"}}`, []string{"reply"}, newAPIProviderChat)
	upstream.response = `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":12,"completion_tokens":5}}`
	repo := s.repo.(*requestPluginFixtureRepo)
	s.gateway.httpUpstream = &replyAuditFailureHTTP{inner: upstream, repo: repo}
	body := `{"model":"public-text","messages":[]}`
	c, recorder := providerContext(newAPIProviderChat, body)
	result, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(body), newAPIProviderChat, "")
	var failure *RequestPluginFailure
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "audit", failure.Class)
	require.NotNil(t, result)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.JSONEq(t, `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":12,"completion_tokens":5}}`, recorder.Body.String())
	require.EqualValues(t, 1, s.ReplyAuditFailures())
	require.Len(t, repo.runs, 1)
	require.Equal(t, "started", repo.runs[0].Records[0].Action)
}

func TestProviderReplyDryRunScopeAndConfig(t *testing.T) {
	source, err := builtin.Source("request-redact")
	require.NoError(t, err)
	s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, source, []string{"reply"}, newAPIProviderChat)
	config := json.RawMessage(`{"replies":true,"finalCheck":"off","rules":[{"pattern":"secret"}]}`)
	input := RequestPluginInstance{Name: "Reply rules", PluginVersionID: 2, Scope: "account", AccountIDs: []int64{account.ID}, Formats: []string{"openai"}, Stages: []string{"reply"}, Config: config}
	_, err = s.SaveRequestPluginInstance(context.Background(), input, 4)
	require.NoError(t, err)
	for _, scope := range []string{"global", "group"} {
		require.NoError(t, validateRequestPluginStages([]string{"reply"}, []string{"openai"}, scope))
	}
	require.NoError(t, validateRequestPluginStages([]string{"request", "outbound", "egress", "reply"}, []string{"openai", "openai_responses"}, "account"))
	require.Error(t, validateRequestPluginStages([]string{"reply", "outbound"}, []string{"openai"}, "account"))
	require.Error(t, validateRequestPluginStages([]string{"reply"}, []string{"task"}, "account"))
	_, err = s.validateRequestPluginStageConfig(context.Background(), plan.items[0].Version, config, false, false)
	require.Error(t, err, "replies cannot silently remain enabled on a non-reply instance")
	out, err := s.DryRunRequestPluginStage(context.Background(), 2, config, json.RawMessage(`{"choices":[{"message":{"content":"secret"}}]}`), plan.call, "reply")
	require.NoError(t, err)
	require.Equal(t, "***", gjson.GetBytes(out.Body, "choices.0.message.content").String())
	require.Equal(t, "reply", out.Records[0].Stage)
	require.Empty(t, upstream.requests)
	require.True(t, AssessPluginHost(plan.items[0].Version.Meta, plan.items[0].Version.Capabilities).Runnable)
	// Dry run is non-streaming and excludes unsupported formats explicitly.
	call := plan.call
	call.Format = "task"
	_, err = s.DryRunRequestPluginStage(context.Background(), 2, config, json.RawMessage(`{}`), call, "reply")
	require.Error(t, err)
}

func TestProviderReplyMemoryReleasedAfterForward(t *testing.T) {
	s, account, _, plan := providerPolicyFixture(t, providerFixtureSource, providerPolicyMeta+`export function reply(){return {action:"pass"}}`, []string{"reply"}, newAPIProviderChat)
	body := `{"model":"public-text","messages":[]}`
	ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), pkghttputil.NewBodyMemoryBudget(providerReplyMemory+newAPIProviderMemory+1<<20))
	defer release()
	c, _ := providerContext(newAPIProviderChat, body)
	_, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(ctx, plan), c, account, []byte(body), newAPIProviderChat, "")
	require.NoError(t, err)
	require.NoError(t, pkghttputil.ReserveRequestBodyMemory(ctx, providerReplyMemory+newAPIProviderMemory), "forward scope must release conversion and reply reservations")
}

func TestProviderReplyNoNetworkAndOptionalErrors(t *testing.T) {
	for _, optional := range []bool{false, true} {
		source := providerPolicyMeta + `export function reply(){utils.fetch({url:"https://api.example.com/forbidden"});return {action:"pass"}}`
		if optional {
			source = strings.Replace(source, "apiVersion:1,", "apiVersion:1,optional:true,", 1)
		}
		s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, source, []string{"reply"}, newAPIProviderChat)
		upstream.response = `{"choices":[{"message":{"content":"ok"}}]}`
		body := `{"model":"public-text","messages":[]}`
		c, recorder := providerContext(newAPIProviderChat, body)
		_, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(body), newAPIProviderChat, "")
		if optional {
			require.NoError(t, err)
			require.Contains(t, recorder.Body.String(), `"content":"ok"`)
		} else {
			require.Error(t, err)
			require.NotContains(t, recorder.Body.String(), `"content":"ok"`)
		}
		require.Len(t, upstream.requests, 1, "reply has no fetch access")
	}
}

type replyCancellationBody struct {
	ctx    context.Context
	chunks []string
	closed bool
	cancel context.CancelFunc
}

func (b *replyCancellationBody) Read(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if len(b.chunks) > 0 {
		n := copy(p, b.chunks[0])
		b.chunks[0] = b.chunks[0][n:]
		if b.chunks[0] == "" {
			b.chunks = b.chunks[1:]
		}
		return n, nil
	}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b *replyCancellationBody) Close() error {
	b.closed = true
	if b.cancel != nil {
		b.cancel()
	}
	return nil
}

type replyCancellationHTTP struct {
	HTTPUpstream
	body       *replyCancellationBody
	timeout    time.Duration
	requestCtx context.Context
	chunks     []string
	requests   int
}

func (h *replyCancellationHTTP) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	h.requests++
	h.requestCtx = req.Context()
	ctx, cancel := req.Context(), context.CancelFunc(nil)
	if h.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, h.timeout)
	}
	h.body = &replyCancellationBody{ctx: ctx, cancel: cancel, chunks: []string{
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":5}}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"secret\"}}]}\n\n",
	}}
	if h.chunks != nil {
		h.body.chunks = h.chunks
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: h.body}, nil
}

func TestProviderReplyStopCancelsActualUpstreamBodyAndKeepsUsage(t *testing.T) {
	s, account, _, plan := providerPolicyFixture(t, providerFixtureSource, providerPolicyMeta+`export function reply(){return {action:"stop",code:"blocked",message:"PRIVATE"}}`, []string{"reply"}, newAPIProviderChat)
	h := &replyCancellationHTTP{}
	s.gateway.httpUpstream = h
	body := `{"model":"public-text","messages":[],"stream":true}`
	c, recorder := providerContext(newAPIProviderChat, body)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	result, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(ctx, plan), c, account, []byte(body), newAPIProviderChat, "")
	var failure *RequestPluginFailure
	require.ErrorAs(t, err, &failure)
	require.True(t, failure.Rejected)
	require.ErrorIs(t, h.body.ctx.Err(), context.Canceled, "stop must reach the HTTP body before the parent deadline")
	require.True(t, h.body.closed)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.NotContains(t, recorder.Body.String(), "secret")
	require.NotContains(t, recorder.Body.String(), "PRIVATE")
	require.NotContains(t, recorder.Body.String(), "[DONE]")
	require.NoError(t, ctx.Err(), "billing context remains usable")
}

func TestProviderReplyLocalFailureDoesNotLowerAccountHealth(t *testing.T) {
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
	stats := newOpenAIAccountRuntimeStats()
	s := &OpenAIGatewayService{rateLimitService: newOpenAIAdvancedSchedulerRateLimitService("true"), openaiAccountStats: stats}
	account := &Account{ID: 17, Type: AccountTypeAPIKey, Platform: PlatformOpenAI}
	for _, failure := range []*RequestPluginFailure{{Rejected: true, Class: "rejected"}, {Class: "audit"}, {Class: "reply_execution"}} {
		require.False(t, s.ReportOpenAIAccountScheduleResult(account, "public-text", false, nil, failure))
		require.False(t, s.ObserveOpenAIAccountHealthFailure(context.Background(), account, failure))
	}
	require.Zero(t, stats.size())
	s.ReportOpenAIAccountScheduleResult(account, "public-text", false, nil, errors.New("actual upstream failure"))
	rate, _, _ := stats.snapshot(account.ID)
	require.Positive(t, rate, "actual upstream failures still update account health")
}
