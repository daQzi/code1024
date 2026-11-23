package runner_test

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/stretchr/testify/require"
)

func TestOptionalQuoteHooksValidationAndDescribe(t *testing.T) {
	base := `
export const meta = { apiVersion: 1, key: "quote-fixture", name: "Quote Fixture", version: "1.0.0", author: { name: "test" }, models: ["video"], fetchMode: "per_task" };
export function buildSubmitRequest() { return { url: "https://api.example/submit" }; }
export function parseSubmitResponse() { return { taskId: "t1" }; }
export function buildQueryRequest() { return { url: "https://api.example/query" }; }
export function parseTaskResult() { return { status: "SUCCESS" }; }
`
	// Stock plugin without quote hooks compiles cleanly.
	stock, err := jsplugin.CompilePlugin(base, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	desc := runner.Describe(stock)
	require.NotContains(t, desc.Hooks, "buildQuoteRequest")
	require.NotContains(t, desc.Hooks, "parseQuoteResponse")
	require.NotContains(t, desc.Hooks, "quotePrice")

	// Unpaired buildQuoteRequest without parseQuoteResponse fails closed.
	_, err = jsplugin.CompilePlugin(base+`export function buildQuoteRequest() { return {}; }`, jsplugin.Options{Concurrency: 1})
	require.Error(t, err)
	_, err = jsplugin.CompilePlugin(base+`export function parseQuoteResponse() { return {}; }`, jsplugin.Options{Concurrency: 1})
	require.Error(t, err)

	// Complete HTTP + pure quote exports compile and appear in runner.Describe.
	full, err := jsplugin.CompilePlugin(base+`
export function buildQuoteRequest() { return { url: "https://api.example/quote" }; }
export function parseQuoteResponse() { return { amount: "1.0", currency: "USD", unit: "upstream_cost", source: "api" }; }
export function quotePrice() { return { amount: "1.0", currency: "USD", unit: "upstream_cost", source: "pure" }; }
`, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	desc = runner.Describe(full)
	require.Contains(t, desc.Hooks, "buildQuoteRequest")
	require.Contains(t, desc.Hooks, "parseQuoteResponse")
	require.Contains(t, desc.Hooks, "quotePrice")

	// Non-task plugin exporting quotePrice is rejected.
	_, err = jsplugin.CompilePlugin(`
export const meta = { apiVersion: 1, key: "bad-quote", name: "Bad", version: "1.0.0", author: { name: "test" } };
export function ingress() { return {}; }
export function quotePrice() { return {}; }
`, jsplugin.Options{Concurrency: 1})
	require.Error(t, err)
}
