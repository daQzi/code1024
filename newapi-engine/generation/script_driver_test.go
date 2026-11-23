package generation_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/stretchr/testify/require"
)

func soraScriptDriver(t *testing.T) generation.ScriptDriver {
	t.Helper()
	source, err := builtin.Source("sora")
	require.NoError(t, err)
	plugin, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	return generation.ScriptDriver{
		Hooks: generation.ScriptHooks{
			Has: plugin.Engine.HasExport,
			Call: func(ctx context.Context, hook string, args ...any) (json.RawMessage, error) {
				var raw json.RawMessage
				err := plugin.Engine.CallInto(ctx, &raw, hook, args...)
				return raw, err
			},
		},
		Context: func(task *generation.Task) map[string]any {
			return map[string]any{"baseUrl": "https://upstream.example.test", "apiKey": "offline-fixture",
				"model": task.Model, "upstreamModel": task.UpstreamModel, "action": task.Action,
				"taskId": task.UpstreamID, "requestBody": task.Request, "state": task.State, "data": task.Data}
		},
	}
}

type scriptConnection struct {
	driver         generation.ScriptDriver
	submits, polls *int
	query          json.RawMessage
	fixtureCheck   func(bool)
}

func (*scriptConnection) Schedulable() bool { return true }
func (*scriptConnection) Acquire(context.Context) (func(), bool, error) {
	return func() {}, true, nil
}
func (*scriptConnection) Prepare(context.Context, *generation.Task) error { return nil }
func (c *scriptConnection) Submit(ctx context.Context, task *generation.Task) error {
	descriptor, err := c.driver.BuildRequest(ctx, task, true)
	if err != nil {
		return err
	}
	if err = descriptor.Validate("https://upstream.example.test", nil, "POST"); err != nil {
		return err
	}
	*c.submits++
	c.fixtureCheck(descriptor.URL == "https://upstream.example.test/v1/videos")
	return c.driver.AcceptSubmission(ctx, task, generation.ScriptResponse{Status: 201, Body: json.RawMessage(`{"id":"private-upstream-id","status":"queued"}`)})
}
func (c *scriptConnection) Poll(ctx context.Context, task *generation.Task) error {
	descriptor, err := c.driver.BuildRequest(ctx, task, false)
	if err != nil {
		return err
	}
	if err = descriptor.Validate("https://upstream.example.test", nil, "GET"); err != nil {
		return err
	}
	*c.polls++
	c.fixtureCheck(descriptor.URL == "https://upstream.example.test/v1/videos/private-upstream-id")
	return c.driver.AcceptQuery(ctx, task, generation.ScriptResponse{Status: 200, Body: c.query})
}

func TestScriptDriverOriginalSoraRecoversAndSettlesWithIndependentPorts(t *testing.T) {
	ctx := context.Background()
	store := &recoveryStore{task: queuedTask(t)}
	store.task.Model, store.task.UpstreamModel = "sora-2", "sora-2"
	store.task.Request = json.RawMessage(`{"prompt":"A calm sea","seconds":"8","size":"1280x720"}`)
	submits, polls := 0, 0
	connection := &scriptConnection{driver: soraScriptDriver(t), submits: &submits, polls: &polls,
		query: json.RawMessage(`{"status":"completed","seconds":6,"size":"1280x720"}`), fixtureCheck: func(ok bool) { require.True(t, ok) }}
	options := generation.ExecutorOptions{Store: store, Resolve: func(context.Context, *generation.Task) (generation.ConnectionRuntime, error) { return connection, nil }}
	require.NoError(t, generation.NewExecutor(options).RunOnce(ctx))
	require.Equal(t, generation.Running, store.task.Status)
	// Rebuild both the engine and the original script after accepted submission.
	connection.driver = soraScriptDriver(t)
	require.NoError(t, generation.NewExecutor(options).RunOnce(ctx))
	require.Equal(t, generation.Succeeded, store.task.Status)
	require.Equal(t, generation.DeliveryReady, store.task.DeliveryStatus)
	require.NotNil(t, store.task.FinishedAt)
	require.Zero(t, store.settlements, "terminal observations precede the financial transaction")
	require.NoError(t, generation.NewExecutor(options).RunOnce(ctx))
	require.Equal(t, generation.BillingSettled, store.task.BillingStatus)
	require.Equal(t, "0.12", store.amount.String())
	require.Equal(t, 1, store.settlements)
	require.Equal(t, 1, submits)
	require.Equal(t, 1, polls)
	public, err := json.Marshal(store.task)
	require.NoError(t, err)
	require.NotContains(t, string(public), "private-upstream-id")
	require.NotContains(t, string(public), "offline-fixture")
}

func TestScriptDriverUnknownStatusAndMissingUsageDoNotInventFinalCharges(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ name, body, status string }{
		{"unknown", `{"status":"future_vendor_state"}`, generation.Running},
		{"missing observed seconds", `{"status":"completed"}`, generation.Succeeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := queuedTask(t)
			task.Status, task.UpstreamID = generation.Running, "upstream"
			driver := soraScriptDriver(t)
			require.NoError(t, driver.AcceptQuery(ctx, task, generation.ScriptResponse{Status: 200, Body: json.RawMessage(tc.body)}))
			require.Equal(t, tc.status, task.Status)
			store := &recoveryStore{task: task}
			if task.Status == generation.Succeeded {
				require.NoError(t, generation.NewExecutor(generation.ExecutorOptions{Store: store}).Settle(ctx, task))
				require.Equal(t, generation.DeliveryReady, task.DeliveryStatus)
			}
			require.Equal(t, generation.BillingReview, task.BillingStatus)
			require.Zero(t, store.settlements)
		})
	}
}

func TestScriptDriverFailedTerminalHookDoesNotPartiallyApplyResult(t *testing.T) {
	task := queuedTask(t)
	task.Status, task.UpstreamID = generation.Running, "upstream"
	driver := soraScriptDriver(t)
	call := driver.Hooks.Call
	driver.Hooks.Call = func(ctx context.Context, hook string, args ...any) (json.RawMessage, error) {
		if hook == "listArtifacts" {
			return nil, errors.New("fixture artifact failure")
		}
		return call(ctx, hook, args...)
	}
	reported := false
	driver.OnResultURL = func(*generation.Task, string) { reported = true }
	driver.Now = func() time.Time { return time.Unix(1234, 0) }
	before := cloneTask(task)
	require.Error(t, driver.AcceptQuery(context.Background(), task, generation.ScriptResponse{Status: 200, Body: json.RawMessage(`{"status":"completed","seconds":6}`)}))
	require.Equal(t, before, task)
	require.False(t, reported)
	require.Error(t, driver.AcceptSubmission(context.Background(), task, generation.ScriptResponse{Status: 500, Body: json.RawMessage(`{"id":"should-not-be-accepted"}`)}))
	require.Equal(t, before, task)
}

func TestScriptDriverUnavailablePortsReturnErrors(t *testing.T) {
	ctx := context.Background()
	var driver generation.ScriptDriver
	_, err := driver.BuildRequest(ctx, &generation.Task{}, true)
	require.Error(t, err)
	require.Error(t, driver.AcceptSubmission(ctx, nil, generation.ScriptResponse{}))
	require.Error(t, driver.AcceptQuery(ctx, nil, generation.ScriptResponse{}))
	executor := generation.NewExecutor(generation.ExecutorOptions{})
	require.Error(t, executor.Process(ctx, &generation.Task{Status: generation.Uncertain}))
	require.Error(t, executor.Settle(ctx, &generation.Task{Status: generation.Succeeded}))
}

func TestScriptDriverRetainsAcceptedIdentityIfImmediateCompletionFails(t *testing.T) {
	task := queuedTask(t)
	driver := generation.ScriptDriver{
		Context: func(*generation.Task) map[string]any { return map[string]any{} },
		Hooks: generation.ScriptHooks{
			Has: func(name string) bool { return name == "listArtifacts" },
			Call: func(_ context.Context, hook string, _ ...any) (json.RawMessage, error) {
				if hook == "parseSubmitResponse" {
					return json.RawMessage(`{"taskId":"confirmed-external-job","immediate":{"status":"SUCCESS"}}`), nil
				}
				return nil, errors.New("fixture artifact failure")
			},
		},
	}
	require.Error(t, driver.AcceptSubmission(context.Background(), task, generation.ScriptResponse{Status: 200, Body: json.RawMessage(`{}`)}))
	require.Equal(t, "confirmed-external-job", task.UpstreamID)
	require.Equal(t, generation.Running, task.Status)
	require.Equal(t, generation.BillingHeld, task.BillingStatus)
	require.Nil(t, task.FinishedAt)
}

func TestScriptDriverQuoteHooksAndPayloadValidation(t *testing.T) {
	now := time.Date(2026, time.November, 23, 12, 0, 0, 0, time.UTC)
	task := queuedTask(t)
	driver := generation.ScriptDriver{
		Context: func(*generation.Task) map[string]any { return map[string]any{"model": task.Model} },
		Now:     func() time.Time { return now },
		Hooks: generation.ScriptHooks{
			Has: func(name string) bool {
				return name == "buildQuoteRequest" || name == "parseQuoteResponse" || name == "quotePrice"
			},
			Call: func(_ context.Context, hook string, _ ...any) (json.RawMessage, error) {
				switch hook {
				case "buildQuoteRequest":
					return json.RawMessage(`{"url":"https://api.example/v1/quote","method":"POST","body":{"model":"test"}}`), nil
				case "parseQuoteResponse":
					return json.RawMessage(`{"amount":"1.25","currency":"USD","unit":"upstream_cost","source":"upstream_quote_api","expiresAt":"2026-11-23T12:10:00Z"}`), nil
				case "quotePrice":
					return json.RawMessage(`{"amount":0.75,"currency":"CNY","unit":"currency","source":"local_quote_table"}`), nil
				default:
					return nil, errors.New("unexpected hook")
				}
			},
		},
	}
	require.True(t, driver.HasQuoteHTTP())
	require.True(t, driver.HasQuotePure())

	req, err := driver.BuildQuoteRequest(context.Background(), task)
	require.NoError(t, err)
	require.Equal(t, "POST", req.Method)
	httpQuote, err := driver.ParseQuoteResponse(context.Background(), task, generation.ScriptResponse{Status: 200, Body: json.RawMessage(`{}`)})
	require.NoError(t, err)
	require.Equal(t, "1.25", httpQuote.Amount.String())
	require.Equal(t, "USD", httpQuote.Currency)
	require.False(t, httpQuote.Final)
	require.NotNil(t, httpQuote.ExpiresAt)

	pureQuote, err := driver.QuotePrice(context.Background(), task)
	require.NoError(t, err)
	require.Equal(t, "0.75", pureQuote.Amount.String())
	require.Equal(t, "CNY", pureQuote.Currency)
	require.False(t, pureQuote.Final)

	for _, bad := range []string{
		`{}`,
		`{"amount":null,"currency":"USD","unit":"u","source":"s"}`,
		`{"amount":-1,"currency":"USD","unit":"u","source":"s"}`,
		`{"amount":1,"currency":"usd","unit":"u","source":"s"}`,
		`{"amount":1,"currency":"USD","unit":"u","source":"s","unknown":1}`,
		`{"amount":1,"currency":"USD","unit":"u","source":"s","expires_at":"2026-11-23T11:59:00Z"}`,
	} {
		_, err := generation.ParseQuotePayload(json.RawMessage(bad), now)
		require.Error(t, err, bad)
	}
}

func TestScriptDriverCancelHooksAndStrictPayload(t *testing.T) {
	task := &generation.Task{ID: "video_cancel", UpstreamID: "up_1", Model: "m1", UpstreamModel: "m1", Action: "generate"}
	driver := generation.ScriptDriver{
		Hooks: generation.ScriptHooks{
			Has: func(name string) bool {
				return name == "buildCancelRequest" || name == "parseCancelResponse"
			},
			Call: func(_ context.Context, hook string, _ ...any) (json.RawMessage, error) {
				switch hook {
				case "buildCancelRequest":
					return json.RawMessage(`{"url":"https://api.example/tasks/up_1/cancel","method":"POST"}`), nil
				case "parseCancelResponse":
					return json.RawMessage(`{"status":"CANCELED","canceled":true,"reason":"stopped upstream"}`), nil
				default:
					return nil, nil
				}
			},
		},
		Context: func(t *generation.Task) map[string]any { return map[string]any{"taskId": t.UpstreamID} },
	}
	require.True(t, driver.HasCancelHTTP())
	require.True(t, driver.HasCancelParser())
	req, err := driver.BuildCancelRequest(context.Background(), task)
	require.NoError(t, err)
	require.Equal(t, "POST", req.Method)
	outcome, err := driver.ParseCancelResponse(context.Background(), task, generation.ScriptResponse{Status: 200, Body: json.RawMessage(`{}`)})
	require.NoError(t, err)
	require.True(t, outcome.Confirmed)
	require.Equal(t, "CANCELED", outcome.Status)
	require.Equal(t, "stopped upstream", outcome.Reason)

	// Ambiguous or non-terminal payloads never confirm cancellation.
	for _, raw := range []string{
		`{"status":"IN_PROGRESS"}`,
		`{"status":"CANCEL_REQUESTED"}`,
		`{"status":"CANCELED","confirmed":false}`,
		`{"status":"FAILURE"}`,
		`{"canceled":false}`,
	} {
		got, err := generation.ParseCancelPayload(json.RawMessage(raw))
		require.NoError(t, err, raw)
		require.False(t, got.Confirmed, raw)
	}
	// Explicit confirmed payloads succeed.
	for _, raw := range []string{
		`{"status":"CANCELED"}`,
		`{"status":"CANCELLED","canceled":true}`,
		`{"status":"FAILURE","confirmed":true}`,
		`{"canceled":true}`,
	} {
		got, err := generation.ParseCancelPayload(json.RawMessage(raw))
		require.NoError(t, err, raw)
		require.True(t, got.Confirmed, raw)
	}
	// Unknown fields or null fail closed.
	for _, bad := range []string{``, `null`, `{"canceled":true,"extra":1}`} {
		_, err := generation.ParseCancelPayload(json.RawMessage(bad))
		require.Error(t, err, bad)
	}
}
