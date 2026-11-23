package service

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	contract "github.com/Wei-Shaw/sub2api/newapi-engine/contract"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const scopedChannelHooks = `
export function ingress(ctx, req) {
 if(ctx.instance.id==="3")throw Error("wrong channel");
 if(!ctx.channel && ctx.instance.id!=="1")throw Error("late rule at early ingress");
 const order=(req.body.metadata?.order || "")+ctx.instance.id;
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/metadata",value:{order}}]}]};
}
export function request(ctx) {if(ctx.instance.id==="3")throw Error("wrong channel");return {action:"continue"};}
export function outbound(ctx,req) {if(ctx.instance.id==="3" || req.body.metadata?.order!=="124")throw Error("wrong order");return {action:"continue"};}
export function egress(ctx,req) {if(ctx.instance.id==="3" || req.body.metadata?.order!=="124")throw Error("wrong order");return {action:"continue"};}
export function reply(ctx) {if(ctx.instance.id==="3")throw Error("wrong channel");return {action:"continue"};}
`

func scopedChannelVersion(t *testing.T, repo *requestPluginFixtureRepo, item *RequestPluginInstance, types ...int) {
	t.Helper()
	v := *item.Version
	v.ID = item.ID + 30
	source := v.Source
	if len(types) > 0 {
		raw, err := json.Marshal(types)
		require.NoError(t, err)
		source = strings.Replace(source, "apiVersion:1,", "apiVersion:1,scope:{channelTypes:"+string(raw)+"},", 1)
	}
	loaded, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	v.Source, v.Meta, v.Capabilities = source, loaded.Meta, runner.Describe(loaded)
	item.Version, item.PluginVersionID = &v, v.ID
	repo.versions[v.ID] = &v
}

func TestRequestPluginChannelScopeLateIngressNativeProviderAndReport(t *testing.T) {
	for _, provider := range []bool{false, true} {
		for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
			for _, stream := range []bool{false, true} {
				t.Run(strconv.FormatBool(provider)+path+"/stream="+strconv.FormatBool(stream), func(t *testing.T) {
					s, account, upstream, plan := scopedReplyFixture(t, path, globalHTTPReportSource+scopedChannelHooks, []string{"ingress", "request", "outbound", "egress", "reply", "report"})
					if !provider {
						nativeRequestAccount(account, path == newAPIProviderResponses)
					}
					channelType := requestPluginChannelType(account, provider)
					repo := s.repo.(*requestPluginFixtureRepo)
					for i := range plan.items {
						if plan.items[i].ID != 1 {
							scopedChannelVersion(t, repo, &plan.items[i], channelType)
						}
					}
					mismatch := plan.items[0]
					mismatch.ID = 3
					scopedChannelVersion(t, repo, &mismatch, 999)
					plan.items = slices.Insert(plan.items, 2, mismatch)
					body := []byte(`{"model":"public-text","messages":[{"role":"user","content":"hello"}],"input":"hello","stream":` + strconv.FormatBool(stream) + `}`)
					early, err := plan.Ingress(context.Background(), body)
					require.NoError(t, err)
					require.Len(t, early.Records, 1)
					require.EqualValues(t, 1, early.Records[0].InstanceID)
					nativeRequestResponse(upstream, path == newAPIProviderResponses, stream)
					h := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 8)}
					s.gateway.httpUpstream = &relayReportHTTP{upstream: s.gateway.httpUpstream, reports: h}
					s.taskReports = newTaskReportExecutor(s)
					t.Cleanup(s.taskReports.close)
					c, _ := providerContext(path, string(early.Body))
					ctx := WithRequestPluginPlan(c.Request.Context(), plan)
					r := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "")
					c.Writer = r
					var result *OpenAIForwardResult
					if path == newAPIProviderChat {
						result, err = s.gateway.ForwardAsChatCompletions(ctx, c, account, early.Body, "", "")
					} else {
						result, err = s.gateway.Forward(ctx, c, account, early.Body)
					}
					require.NoError(t, err, "audit: %+v", repo.runs)
					require.Len(t, upstream.requests, 1)
					wire, err := io.ReadAll(upstream.requests[0].Body)
					require.NoError(t, err)
					require.Equal(t, "124", gjson.GetBytes(wire, "metadata.order").String())
					r.Finish(early.Body)
					result.pluginReportBilling.complete(decimal.RequireFromString("0.12"), true)
					for _, id := range []int64{1, 2, 4} {
						payload := awaitTaskReport(t, h)
						require.Equal(t, strconv.FormatInt(id, 10), gjson.GetBytes(payload["ctx"], "instance.id").String())
						counts := map[string]int{}
						for _, record := range gjson.GetBytes(payload["record"], "requestPlugins").Array() {
							require.NotEqualValues(t, 3, record.Get("instance_id").Int())
							counts[record.Get("stage").String()]++
						}
						require.Equal(t, map[string]int{"ingress": 3, "request": 3, "outbound": 3, "egress": 3, "reply": 3}, counts)
					}
					require.Empty(t, h.requests)
				})
			}
		}
	}
}

func TestRequestPluginChannelScopeExcludesUnselectedBodyAndEarlyFailure(t *testing.T) {
	s, account, _, plan := providerPolicyFixture(t, providerFixtureSource, globalHTTPReportSource+scopedChannelHooks, []string{"ingress", "request", "outbound", "egress", "reply", "report"}, newAPIProviderChat)
	item := &plan.items[0]
	item.Scope, item.AccountIDs = "global", nil
	scopedChannelVersion(t, s.repo.(*requestPluginFixtureRepo), item, 1)
	require.False(t, plan.HasEarlyIngress())
	early, err := plan.Ingress(context.Background(), []byte("binary"))
	require.NoError(t, err)
	require.Equal(t, "binary", string(early.Body))
	require.Empty(t, early.Records)
	require.Empty(t, plan.httpReportItems(0, 0))
	require.Empty(t, plan.replyItems(account.ID, 61))
	require.Empty(t, plan.attemptItems(account.ID, 0))
	require.Len(t, plan.attemptItems(account.ID, 1), 1)
	ctx, release := pkghttputil.WithRequestBodyMemoryBudget(WithRequestPluginPlan(context.Background(), plan), pkghttputil.NewBodyMemoryBudget(1))
	defer release()
	out, err := s.relayRequestPluginStages(ctx, account, newAPIProviderChat, "vendor-text", []byte("binary"), contract.AdvancedCustomRoute{}, requestPluginStageRun{provider: true, stages: []string{"request", "ingress", "outbound", "egress"}})
	require.NoError(t, err)
	require.Equal(t, "binary", string(out.Body))
	require.Empty(t, out.Records)
	require.Empty(t, s.repo.(*requestPluginFixtureRepo).runs)
	// A real native OAuth identity is Codex, even on an OpenAI platform.
	account.Type = AccountTypeOAuth
	require.Equal(t, 57, requestPluginChannelType(account, false))
	require.Empty(t, plan.attemptItems(account.ID, requestPluginChannelType(account, false)))
	account.Type = AccountTypeAPIKey
	account.Platform = PlatformKimi
	require.Empty(t, plan.attemptItems(account.ID, requestPluginChannelType(account, false)))
}

func TestRequestPluginChannelScopeLateIngressRepeatsOnlyPerAttempt(t *testing.T) {
	s, account, _, plan := providerPolicyFixture(t, providerExchangeSource(providerFixtureSource), providerPolicyMeta+`export function ingress(ctx, req){
 if(!ctx.channel || req.body.late!==undefined)throw Error("wrong boundary or retry input");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/late",value:ctx.attempt}]}]};
}`, []string{"ingress"}, newAPIProviderChat)
	plan.items[0].Scope, plan.items[0].AccountIDs = "global", nil
	scopedChannelVersion(t, s.repo.(*requestPluginFixtureRepo), &plan.items[0], 61)
	h := &providerCredentialHTTP{rejectStatus: 419, rejections: 1}
	s.gateway.httpUpstream = h
	body := []byte(`{"model":"public-text","messages":[]}`)
	_, err := plan.Ingress(context.Background(), body)
	require.NoError(t, err)
	c, _ := providerContext(newAPIProviderChat, string(body))
	_, err = s.gateway.ForwardAsChatCompletions(WithRequestPluginPlan(c.Request.Context(), plan), c, account, body, "", "")
	require.NoError(t, err)
	require.Equal(t, 2, h.chats)
	runs := s.repo.(*requestPluginFixtureRepo).runs
	require.Len(t, runs, 2)
	for i, run := range runs {
		require.Len(t, run.Records, 1)
		require.Equal(t, i, run.Records[0].Attempt)
		require.EqualValues(t, i, gjson.Get(h.bodies[i*2+1], "late").Int())
	}
	// A subsequent native attempt must rematch the actual selected identity.
	require.Empty(t, plan.httpReportItems(account.ID, 1))
	require.Empty(t, plan.attemptItems(account.ID, 1))
}

func TestRequestPluginChannelScopeSaveAndIsolatedDryRun(t *testing.T) {
	s, _, _, plan := providerPolicyFixture(t, providerFixtureSource, providerPolicyMeta+`export function ingress(){return {action:"continue"}}`, []string{"ingress"}, newAPIProviderChat)
	repo := s.repo.(*requestPluginFixtureRepo)
	scopedChannelVersion(t, repo, &plan.items[0], 61)
	version := plan.items[0].Version
	input := RequestPluginInstance{Name: "Typed policy", PluginVersionID: version.ID, Scope: "global", Stages: []string{"ingress"}, Config: json.RawMessage(`{}`)}
	for _, formats := range [][]string{nil, {"task", "gemini", "unknown"}} {
		input.Formats = formats
		_, err := s.SaveRequestPluginInstance(context.Background(), input, 4)
		require.Error(t, err)
	}
	for _, formats := range [][]string{{"openai"}, {"openai_responses"}, {"claude"}, {"gemini"}, {"task"}} {
		input.Formats = formats
		_, err := s.SaveRequestPluginInstance(context.Background(), input, 4)
		require.NoError(t, err)
	}
	out, err := s.DryRunRequestPlugin(context.Background(), version.ID, input.Config, json.RawMessage(`{"model":"public-text"}`), plan.call)
	require.NoError(t, err)
	require.Len(t, out.Records, 1, "a single-instance dry run tests the hook without simulating channel selection")
	compatibility := AssessPluginHost(version.Meta, version.Capabilities)
	require.True(t, compatibility.Runnable)
	require.Contains(t, compatibility.Kinds, "transform")
}

func TestTaskChannelScopeLateIngressBeforeFreezeAndPinnedSnapshots(t *testing.T) {
	source := strings.Replace(taskEgressSource, "apiVersion:1,", "apiVersion:1,scope:{channelTypes:[61]},", 1) + `
export function ingress(ctx, req){
 if(!ctx.channel || ctx.channel.type!==61 || ctx.format!=="task" || ctx.upstreamModel!=="sora-2")throw Error("wrong task placement");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/seconds",value:"8"},{op:"add",path:"/prompt",value:"Allowed"}]}]};
}
export function report(){}`
	s, repo, account, _ := taskEgressFixture(t, source)
	account.Credentials[NewAPIVideoPricingKey] = map[string]any{"*": map[string]any{"mode": "fixed", "unit": "second", "unit_price": "0.02"}}
	repo.items[0].Stages = []string{"ingress", "egress", "report"}
	s.requestPlugins.expires = time.Time{}
	call := RequestPluginCall{UserID: 4, TokenID: 5, GroupID: 7, Model: "sora-2", Format: "task", Path: "/v1/videos"}
	plan, err := s.NewRequestPluginPlan(context.Background(), call)
	require.NoError(t, err)
	require.False(t, plan.HasEarlyIngress())
	body := json.RawMessage(`{"model":"sora-2","prompt":"Ocean","seconds":"4"}`)
	ctx := WithRequestPluginPlan(context.Background(), plan)
	key := &APIKey{ID: 5, UserID: 4, User: &User{ID: 4}, Group: &Group{ID: 7, RateMultiplier: 1}}
	held := &generationReplayFixture{}
	service := &GenerationService{repo: held, plugins: s}
	task, err := service.Create(ctx, key, nil, account, call.Model, body, "accepted")
	require.NoError(t, err)
	require.Equal(t, "0.16", task.Price.Hold.String())
	require.Equal(t, "8", task.PublicFields.Seconds)
	require.Equal(t, "Allowed", task.PublicFields.Prompt)
	fingerprint, err := generationRequestFingerprint(body)
	require.NoError(t, err)
	require.Equal(t, fingerprint, task.Fingerprint)
	require.Len(t, repo.runs, 1)
	require.Equal(t, "ingress", repo.runs[0].Records[0].Stage)
	report, err := s.openTaskReport(task)
	require.NoError(t, err)
	require.Equal(t, []int64{3}, report.Instances)
	require.Len(t, report.Records, 1)
	require.NotEmpty(t, task.RequestPluginsEncrypted)
	// Replaying an accepted request must not run a changed or broken rule.
	held.task = task
	repo.runErr = io.ErrClosedPipe
	replayed, err := service.Create(ctx, key, nil, account, call.Model, body, "accepted")
	require.NoError(t, err)
	require.Same(t, task, replayed)
	require.Len(t, repo.runs, 1)
}

func TestTaskChannelScopeRejectsBeforeFreezeAndExcludesUnmatchedSnapshots(t *testing.T) {
	for _, mode := range []string{"reject", "audit", "cancel", "capacity", "mismatch", "routing", "optional", "multipart"} {
		t.Run(mode, func(t *testing.T) {
			types, hook := "61", `return {action:"continue"}`
			if mode == "reject" {
				hook = `return {action:"reject"}`
			} else if mode == "routing" || mode == "optional" {
				hook = `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/seconds",value:"8"},{op:"replace",path:"/model",value:"other"}]}]}`
			} else if mode == "mismatch" {
				types, hook = "1", `throw Error("wrong channel")`
			}
			source := strings.Replace(taskEgressSource, "apiVersion:1,", "apiVersion:1,scope:{channelTypes:["+types+"]},", 1) + `export function ingress(){` + hook + `}export function report(){}`
			if mode == "optional" {
				source = strings.Replace(source, "apiVersion:1,", "apiVersion:1,optional:true,", 1)
			}
			s, repo, account, _ := taskEgressFixture(t, source)
			account.Credentials[NewAPIVideoPricingKey] = map[string]any{"*": map[string]any{"mode": "fixed", "unit": "second", "unit_price": "0.02"}}
			repo.items[0].Stages = []string{"ingress", "egress", "report"}
			s.requestPlugins.expires = time.Time{}
			call := RequestPluginCall{UserID: 4, TokenID: 5, GroupID: 7, Model: "sora-2", Format: "task", Path: "/v1/videos"}
			plan, err := s.NewRequestPluginPlan(context.Background(), call)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(WithRequestPluginPlan(context.Background(), plan))
			defer cancel()
			if mode == "audit" {
				repo.runErr = io.ErrClosedPipe
			} else if mode == "cancel" {
				cancel()
			} else if mode == "capacity" {
				var release func()
				ctx, release = pkghttputil.WithRequestBodyMemoryBudget(ctx, pkghttputil.NewBodyMemoryBudget(1))
				defer release()
			} else if mode == "multipart" {
				ctx = context.WithValue(ctx, generationMultipartBodyKey{}, true)
			}
			key := &APIKey{ID: 5, UserID: 4, User: &User{ID: 4}, Group: &Group{ID: 7, RateMultiplier: 1}}
			held := &generationReplayFixture{}
			service := &GenerationService{repo: held, plugins: s}
			task, err := service.Create(ctx, key, nil, account, call.Model, json.RawMessage(`{"model":"sora-2","prompt":"Ocean","seconds":"4"}`), "accepted")
			if mode == "mismatch" {
				require.NoError(t, err)
				require.Empty(t, task.RequestPluginsEncrypted)
				require.Empty(t, task.RequestReportEncrypted)
				require.Empty(t, repo.runs)
			} else if mode == "multipart" {
				require.NoError(t, err)
				require.Equal(t, "continue", repo.runs[0].Records[0].Action)
			} else if mode == "optional" {
				require.NoError(t, err)
				require.Equal(t, "skipped", repo.runs[0].Records[0].Action)
				require.Equal(t, "0.08", task.Price.Hold.String(), "a failed optional rule cannot partially change the charged duration")
			} else {
				require.Error(t, err)
				require.Nil(t, held.held)
			}
			require.Empty(t, s.gateway.httpUpstream.(*generationFixtureHTTP).requests)
		})
	}
}
