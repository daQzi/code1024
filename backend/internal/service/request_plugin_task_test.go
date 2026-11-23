package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/stretchr/testify/require"
)

const taskEgressSource = `export const meta={apiVersion:1,author:{name:"Fixture"},key:"egress-fixture",name:"Egress",version:"1.0.0",configSchema:{type:"object",properties:{deny:{type:"string",title:"Deny",description:"Denied text",default:"private"}}}};
export function egress(ctx,req){
 if(ctx.stage!=="egress" || ctx.format!=="task" || ctx.model!=="sora-2" || ctx.upstreamModel!=="sora-2" || ctx.channel.type!==61 || ctx.attempt!==0) throw new Error("wrong host stage");
 if(req.body.prompt.includes(ctx.config.deny)) return {action:"reject",code:"private_prompt",message:"private-message"};
 return {action:"continue"};
}`

func taskEgressFixture(t *testing.T, source string) (*NewAPIPluginService, *requestPluginFixtureRepo, *Account, *GenerationTask) {
	t.Helper()
	s, repo := requestPluginFixture(t, source)
	policy := repo.versions[1]
	policy.ID = 2
	repo.versions[2] = policy
	xaiSource, err := builtin.Source("sora")
	require.NoError(t, err)
	loaded, err := jsplugin.CompilePlugin(xaiSource, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	repo.versions[1] = &NewAPIPluginVersion{ID: 1, Enabled: true, Source: xaiSource, Meta: loaded.Meta, Capabilities: runner.Describe(loaded)}
	item := requestFixtureItem(3, "global", `{"deny":"private"}`, policy)
	item.Stages, item.Formats = []string{"egress"}, []string{"task"}
	repo.items = []RequestPluginInstance{item}
	key := &APIKey{ID: 5, UserID: 4, Group: &Group{ID: 7}}
	call := RequestPluginCall{RequestID: "client-original", UserID: 4, TokenID: 5, TokenName: "private-token-name", GroupID: 7, Model: "sora-2", Group: "7", Format: "task", Path: "/v1/videos"}
	plan, err := s.NewRequestPluginPlan(context.Background(), call)
	require.NoError(t, err)
	sealed, err := s.snapshotTaskRequestPlugins(WithRequestPluginPlan(context.Background(), plan), key, call.Model)
	require.NoError(t, err)
	account := &Account{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.example.com", "api_key": "fixture-key", NewAPIPluginRevisionKey: 1}}
	body, action, err := s.DecodeVideo(context.Background(), account, 1, "sora-2", json.RawMessage(`{"model":"sora-2","prompt":"private prompt","seconds":"8","size":"1280x720"}`))
	require.NoError(t, err)
	task := &GenerationTask{ID: "video_" + strings.Repeat("a", 40), UserID: 4, APIKeyID: 5, GroupID: 7, AccountID: 9, PluginVersionID: 1, Model: "sora-2", UpstreamModel: "sora-2", Action: action, Request: body, RequestPluginsEncrypted: sealed}
	return s, repo, account, task
}

func TestTaskEgressPinsAdmissionRulesAndStopsBeforeSubmit(t *testing.T) {
	s, repo, account, task := taskEgressFixture(t, taskEgressSource)
	// A later edit, disable or instance deletion cannot change an accepted job.
	repo.items = nil
	err := s.SubmitTask(context.Background(), account, task)
	var submission *GenerationSubmissionError
	var failure *RequestPluginFailure
	require.ErrorAs(t, err, &submission)
	require.False(t, submission.Uncertain)
	require.ErrorAs(t, err, &failure)
	require.True(t, failure.Rejected)
	require.Empty(t, s.gateway.httpUpstream.(*generationFixtureHTTP).requests)
	require.Len(t, repo.runs, 1)
	require.Equal(t, "client-original", repo.runs[0].RequestID)
	require.Equal(t, "egress", repo.runs[0].Records[0].Stage)
	raw, err := json.Marshal(repo.runs)
	require.NoError(t, err)
	for _, value := range []string{"private prompt", "private-message", "private-token-name", "fixture-key", "Config", "request_plugins"} {
		require.NotContains(t, string(raw), value)
	}
	public, err := json.Marshal(task)
	require.NoError(t, err)
	require.NotContains(t, string(public), task.RequestPluginsEncrypted)
}

func TestTaskEgressAllowedSubmitKeepsCertainAndUncertainFailures(t *testing.T) {
	const allow = `export const meta={apiVersion:1,author:{name:"Fixture"},key:"egress-allow",name:"Egress",version:"1.0.0"};export function egress(){return {action:"continue"}}`
	for _, mode := range []string{"accepted", "transport", "rejected"} {
		t.Run(mode, func(t *testing.T) {
			s, repo, account, task := taskEgressFixture(t, allow)
			upstream := s.gateway.httpUpstream.(*generationFixtureHTTP)
			upstream.response = `{"id":"vendor-accepted"}`
			if mode == "transport" {
				upstream.err = errors.New("transport failed")
			}
			if mode == "rejected" {
				upstream.status = 403
			}
			err := s.SubmitTask(context.Background(), account, task)
			if mode == "accepted" {
				require.NoError(t, err)
				require.Equal(t, "vendor-accepted", task.UpstreamID)
				require.Equal(t, GenerationRunning, task.Status)
			} else {
				var submission *GenerationSubmissionError
				require.ErrorAs(t, err, &submission)
				require.Equal(t, mode == "transport", submission.Uncertain)
			}
			require.Len(t, upstream.requests, 1)
			require.Len(t, repo.runs, 1)
			require.Equal(t, "continue", repo.runs[0].Records[0].Action)
		})
	}
}

func TestTaskEgressIsReadonlyAndOptionalErrorsDoNotOverrideRejection(t *testing.T) {
	for _, tc := range []struct {
		name, result, action string
		optional, denied     bool
	}{
		{"effect", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/prompt",value:"changed"}]}]}`, "error", false, true},
		{"optional effect", `return {action:"continue",effects:[{type:"json.patch",ops:[]}]}`, "skipped", true, false},
		{"optional reject", `return {action:"reject",code:"denied"}`, "reject", true, true},
		{"empty effects", `return {action:"continue",effects:[]}`, "continue", false, false},
		{"throw", `throw new Error("private-error")`, "error", false, true},
		{"no network", `utils.fetch({url:"https://api.example.com/private"});return {action:"continue"}`, "error", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := `export const meta={apiVersion:1,author:{name:"Fixture"},key:"egress-effect",name:"Egress",version:"1.0.0",optional:` + map[bool]string{true: "true", false: "false"}[tc.optional] + `};export function egress(){` + tc.result + `}`
			s, repo, _, task := taskEgressFixture(t, source)
			body := json.RawMessage(`{"prompt":"original"}`)
			err := s.taskRequestPluginEgress(context.Background(), task, body, false)
			require.Equal(t, tc.denied, err != nil)
			require.Equal(t, tc.action, repo.runs[0].Records[0].Action)
			require.JSONEq(t, `{"prompt":"original"}`, string(body))
			require.Empty(t, s.gateway.httpUpstream.(*generationFixtureHTTP).requests)
		})
	}
}

func TestTaskEgressMultipartIsUncheckedAndOldTasksKeepOriginalPolicy(t *testing.T) {
	s, repo, _, task := taskEgressFixture(t, taskEgressSource)
	require.NoError(t, s.taskRequestPluginEgress(context.Background(), task, nil, true))
	require.Equal(t, "unchecked", repo.runs[0].Records[0].Action)
	task.RequestPluginsEncrypted = ""
	require.NoError(t, s.taskRequestPluginEgress(context.Background(), task, json.RawMessage(`{"prompt":"private"}`), false))
	require.Len(t, repo.runs, 1)
}

func TestTaskEgressRawTextDescriptorIsExplicitlyUnchecked(t *testing.T) {
	s, repo, account, task := taskEgressFixture(t, taskEgressSource)
	version := repo.versions[1]
	version.Source = strings.Replace(version.Source, "export function buildSubmitRequest(ctx)", "function originalBuildSubmitRequest(ctx)", 1) + `
export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/raw",method:"POST",body:"private prompt"};}`
	upstream := s.gateway.httpUpstream.(*generationFixtureHTTP)
	upstream.response = `{"id":"text-task"}`
	require.NoError(t, s.SubmitTask(context.Background(), account, task))
	require.Len(t, repo.runs, 1)
	require.Equal(t, "unchecked", repo.runs[0].Records[0].Action, "raw wire text must not be reported as a checked JSON object")
	require.Len(t, upstream.requests, 1)
}

func TestTaskEgressRejectsForeignIdentityCorruptVersionAndOversizeSnapshot(t *testing.T) {
	for _, change := range []string{"owner", "version", "cipher", "oversize"} {
		t.Run(change, func(t *testing.T) {
			s, _, account, task := taskEgressFixture(t, taskEgressSource)
			switch change {
			case "owner":
				task.UserID++
			case "version":
				s.repo.(*requestPluginFixtureRepo).versions[2].Source += "\n// drift"
			case "cipher":
				task.RequestPluginsEncrypted = "corrupt"
			case "oversize":
				task.RequestPluginsEncrypted = strings.Repeat("x", MaxTaskRequestPluginEncrypted+1)
			}
			err := s.SubmitTask(context.Background(), account, task)
			var submission *GenerationSubmissionError
			require.ErrorAs(t, err, &submission)
			require.False(t, submission.Uncertain)
			require.Empty(t, s.gateway.httpUpstream.(*generationFixtureHTTP).requests)
		})
	}
}

func TestTaskEgressFormatsDryRunAndCanceledExecution(t *testing.T) {
	s, repo, _, task := taskEgressFixture(t, taskEgressSource)
	plan, err := s.NewRequestPluginPlan(context.Background(), RequestPluginCall{GroupID: 7, Model: "sora-2", Format: "openai"})
	require.NoError(t, err)
	require.Nil(t, plan, "Task policies must not change a same-named text endpoint")
	input := RequestPluginInstance{Name: "Task policy", PluginVersionID: 2, Scope: "global", Stages: []string{"egress"}, Formats: []string{"task"}, Config: json.RawMessage(`{}`)}
	_, err = s.SaveRequestPluginInstance(context.Background(), input, 4)
	require.NoError(t, err)
	input.Formats = nil
	_, err = s.SaveRequestPluginInstance(context.Background(), input, 4)
	require.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = s.taskRequestPluginEgress(ctx, task, json.RawMessage(`{"prompt":"safe"}`), false)
	require.Error(t, err)
	before := len(repo.runs)
	out, err := s.DryRunRequestPluginStage(context.Background(), 2, json.RawMessage(`{}`), json.RawMessage(`{"prompt":"private"}`), RequestPluginCall{Model: "sora-2", Format: "task"}, "egress")
	require.NoError(t, err)
	// Dry run has no selected account/upstream model; that fixture deliberately
	// rejects such a context. It is an execution error, never a live submission.
	require.Equal(t, "execution", out.ErrorClass)
	require.Len(t, repo.runs, before, "caller owns dry-run audit exactly once")
	require.False(t, errors.Is(err, context.Canceled))
}

func TestTaskEgressAuditFailureAndConcurrentSubmitReleaseWorkers(t *testing.T) {
	s, repo, account, task := taskEgressFixture(t, taskEgressSource)
	repo.runErr = errors.New("audit unavailable")
	err := s.SubmitTask(context.Background(), account, task)
	var failure *RequestPluginFailure
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "audit", failure.Class)
	require.Empty(t, s.gateway.httpUpstream.(*generationFixtureHTTP).requests)
	repo.runErr = nil
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	results := make(chan error, runner.MaxProcesses)
	for range runner.MaxProcesses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := *task
			results <- s.SubmitTask(ctx, account, &copy)
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.ErrorAs(t, err, &failure)
		require.True(t, failure.Rejected, "pool contention cannot turn the policy rejection into a timeout")
	}
	require.Len(t, repo.runs, runner.MaxProcesses)
}

type taskEgressAccountRepo struct {
	AccountRepository
	account *Account
}

func (r taskEgressAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func TestTaskEgressWorkerMarksLocalFailureAndRefunds(t *testing.T) {
	s, _, account, task := taskEgressFixture(t, taskEgressSource)
	account.Status, account.Schedulable, account.Concurrency = StatusActive, true, 0
	task.Status, task.BillingStatus, task.CreatedAt = GenerationSubmitting, GenerationBillingHeld, time.Now()
	s.gateway.concurrencyService = NewConcurrencyService(nil)
	repo := &generationSettlementFixture{}
	generations := &GenerationService{repo: repo, plugins: s, accounts: taskEgressAccountRepo{account: account}, gateway: s.gateway}
	err := generations.process(context.Background(), task)
	require.Error(t, err)
	require.Equal(t, GenerationFailed, task.Status)
	require.Equal(t, GenerationBillingPending, task.BillingStatus)
	require.Equal(t, "request_processing_rejected", task.ErrorCode)
	require.NotNil(t, task.FinishedAt)
	require.Empty(t, s.gateway.httpUpstream.(*generationFixtureHTTP).requests)
	require.NoError(t, generations.process(context.Background(), task))
	require.True(t, repo.settled)
	require.True(t, repo.amount.IsZero())
	// No path returns a rejected queued task to submitting on the next cycle.
	require.NoError(t, generations.process(context.Background(), task))
	require.Empty(t, s.gateway.httpUpstream.(*generationFixtureHTTP).requests)
}

func TestTaskEgressAdmissionPersistsPlanBeforeHoldingAndEmptyPlanStaysEmpty(t *testing.T) {
	s, _, account, _ := taskEgressFixture(t, taskEgressSource)
	key := &APIKey{ID: 5, UserID: 4, User: &User{ID: 4}, Group: &Group{ID: 7, RateMultiplier: 1}}
	account.Credentials[NewAPIVideoPricingKey] = map[string]any{"*": map[string]any{"mode": "fixed", "unit": "second", "unit_price": "0.02"}}
	call := RequestPluginCall{UserID: 4, TokenID: 5, GroupID: 7, Model: "sora-2", Format: "task", Path: "/v1/videos"}
	plan, err := s.NewRequestPluginPlan(context.Background(), call)
	require.NoError(t, err)
	admissions := &generationReplayFixture{}
	generations := &GenerationService{repo: admissions, plugins: s}
	body := json.RawMessage(`{"model":"sora-2","prompt":"private prompt","seconds":"8","size":"1280x720"}`)
	task, err := generations.Create(WithRequestPluginPlan(context.Background(), plan), key, nil, account, call.Model, body, "pinned")
	require.NoError(t, err)
	require.Same(t, admissions.held, task)
	require.NotEmpty(t, task.RequestPluginsEncrypted)
	require.Equal(t, "0.16", task.Price.Hold.String())
	empty, err := s.snapshotTaskRequestPlugins(WithRequestPluginPlan(context.Background(), nil), key, call.Model)
	require.NoError(t, err)
	require.Empty(t, empty, "a later policy cannot attach itself to the original empty ingress plan")
	_, err = s.snapshotTaskRequestPlugins(WithRequestPluginPlan(context.Background(), plan), key, "different-model")
	require.Error(t, err)
	require.Empty(t, s.gateway.httpUpstream.(*generationFixtureHTTP).requests)
	large := plan.items[0]
	large.Config = json.RawMessage(`{"deny":"` + strings.Repeat("x", 59000) + `"}`)
	plan.items = []RequestPluginInstance{large, large, large, large, large}
	_, err = s.snapshotTaskRequestPlugins(WithRequestPluginPlan(context.Background(), plan), key, call.Model)
	require.ErrorContains(t, err, "256 KiB", "aggregate policy config must be bounded before allocating the encrypted snapshot")
}

func TestTaskEgressBuiltinRedactionFinalCheckAndDryRun(t *testing.T) {
	source, err := builtin.Source("request-redact")
	require.NoError(t, err)
	s, _, _, _ := taskEgressFixture(t, source)
	input := RequestPluginInstance{Name: "Task final redaction", PluginVersionID: 2, Scope: "global", Stages: []string{"egress"}, Formats: []string{"task"}, Config: json.RawMessage(`{"categories":["email"],"finalCheck":"reject","replies":false}`)}
	_, err = s.SaveRequestPluginInstance(context.Background(), input, 4)
	require.NoError(t, err, "the original New API finalCheck config is enabled only with actual egress")
	for _, tc := range []struct {
		prompt   string
		rejected bool
	}{{"Contact person@example.com", true}, {"A calm sea", false}} {
		body, err := json.Marshal(map[string]string{"prompt": tc.prompt})
		require.NoError(t, err)
		out, err := s.DryRunRequestPluginStage(context.Background(), 2, input.Config, body, RequestPluginCall{Model: "sora-2", Format: "task"}, "egress")
		require.NoError(t, err)
		require.Equal(t, tc.rejected, out.Rejected)
		require.JSONEq(t, string(body), string(out.Body), "finalCheck only rejects; it never edits the last upstream JSON")
	}
}
