package runner_test

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/stretchr/testify/require"
)

func TestCancelHookValidationAndDescribe(t *testing.T) {
	base := `
export const meta = { apiVersion: 1, key: "cancel-demo", name: "Cancel Demo", version: "1.0.0", author: { name: "Test" }, models: ["m1"], fetchMode: "per_task" };
export function buildSubmitRequest() { return { url: "https://api.example/submit" }; }
export function parseSubmitResponse() { return { taskId: "u1" }; }
export function buildQueryRequest() { return { url: "https://api.example/q" }; }
export function parseTaskResult() { return { status: "SUCCESS" }; }
`
	// Stock plugin without cancel hooks compiles cleanly and omits them from Describe.
	stock, err := jsplugin.CompilePlugin(base, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	desc := runner.Describe(stock)
	require.NotContains(t, desc.Hooks, "buildCancelRequest")
	require.NotContains(t, desc.Hooks, "parseCancelResponse")

	// parseCancelResponse without buildCancelRequest fails closed.
	_, err = jsplugin.CompilePlugin(base+`export function parseCancelResponse() { return { canceled: true }; }`, jsplugin.Options{Concurrency: 1})
	require.Error(t, err)

	// buildCancelRequest alone is permitted (accepted cancel without speculative refund).
	cancelOnly, err := jsplugin.CompilePlugin(base+`export function buildCancelRequest() { return { url: "https://api.example/cancel", method: "POST" }; }`, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	descOnly := runner.Describe(cancelOnly)
	require.Contains(t, descOnly.Hooks, "buildCancelRequest")
	require.NotContains(t, descOnly.Hooks, "parseCancelResponse")

	// Paired cancel hooks are both exposed.
	paired, err := jsplugin.CompilePlugin(base+`
export function buildCancelRequest() { return { url: "https://api.example/cancel", method: "POST" }; }
export function parseCancelResponse() { return { status: "CANCELED", canceled: true }; }
`, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	descPaired := runner.Describe(paired)
	require.Contains(t, descPaired.Hooks, "buildCancelRequest")
	require.Contains(t, descPaired.Hooks, "parseCancelResponse")
}
