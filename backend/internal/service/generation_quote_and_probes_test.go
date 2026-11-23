package service

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestWithGenerationRequestHeadersStripsSensitiveHeaders(t *testing.T) {
	hdr := http.Header{}
	hdr.Set("X-Tier", "vip")
	hdr.Set("Authorization", "Bearer top-secret")
	hdr.Set("X-Api-Key", "sk-secret")
	hdr.Set("Cookie", "sid=123")
	hdr.Set("X-Custom-Token", "tok-123")
	hdr.Set("X-Long", strings.Repeat("a", 300))

	ctx := WithGenerationRequestHeaders(context.Background(), hdr)
	captured := generationRequestHeaders(ctx)
	require.Equal(t, "vip", captured["x-tier"])
	require.NotContains(t, captured, "authorization")
	require.NotContains(t, captured, "x-api-key")
	require.NotContains(t, captured, "cookie")
	require.NotContains(t, captured, "x-custom-token")
	require.Len(t, captured["x-long"], 257)

	cap := decimal.NewFromInt(10)
	rule := GenerationPriceRule{
		Mode:       "expression",
		Expression: `tier("vip", u("seconds") * (header("X-Tier") == "vip" ? 0.5 : 1) * (param("quality") == "hd" ? 2 : 1))`,
		Maximum:    &cap,
	}
	schema := map[string]jsplugin.UsageFieldSchema{"seconds": {Type: "number", Unit: "second"}}
	snap, err := NewGenerationExpressionSnapshotWithRequest(rule, schema, json.RawMessage(`{"seconds":4}`), []byte(`{"quality":"hd"}`), captured, decimal.NewFromInt(1))
	require.NoError(t, err)
	// 4 * 0.5 * 2 = 4.0
	require.Equal(t, "4", snap.Expression.EstimatedAmount.String())
	require.Len(t, snap.Expression.EstimatedRequestRules, 2)

	// Overlong referenced header must fail closed instead of truncating.
	longRule := GenerationPriceRule{
		Mode:       "expression",
		Expression: `header("X-Long") == "x" ? 1 : 2`,
		Maximum:    &cap,
	}
	_, err = NewGenerationExpressionSnapshotWithRequest(longRule, nil, json.RawMessage(`{}`), []byte(`{}`), captured, decimal.NewFromInt(1))
	require.Error(t, err)
}

func generationPluginSourceFixture(t *testing.T, source string) (*NewAPIPluginService, *Account, *generationFixtureHTTP) {
	t.Helper()
	loaded, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	executable, err := os.Executable()
	require.NoError(t, err)
	pool := runner.NewPool(executable, []string{"-test.run=^TestGenerationPluginWorkerHelper$", "--", "generation-plugin-test-worker"})
	t.Cleanup(pool.Close)
	httpClient := &generationFixtureHTTP{}
	version := &NewAPIPluginVersion{ID: 1, Enabled: true, Source: source, Meta: loaded.Meta, Capabilities: runner.Describe(loaded)}
	s := &NewAPIPluginService{repo: &generationPluginFixtureRepo{version: version}, pool: pool, gateway: &OpenAIGatewayService{httpUpstream: httpClient}}
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.example.com", "api_key": "fixture-key", NewAPIPluginRevisionKey: 1}}
	return s, account, httpClient
}

func TestNewAPITaskQuoteHooksAndStockNoOp(t *testing.T) {
	ctx := context.Background()
	task := &GenerationTask{
		ID: "video_" + strings.Repeat("5", 40), PluginVersionID: 1,
		Model: "test-video", UpstreamModel: "test-video", Action: "text_to_video",
		Request: json.RawMessage(`{"prompt":"sun","seconds":8}`), State: json.RawMessage(`{}`), Data: json.RawMessage(`{}`),
		CreatedAt: time.Now().UTC(),
	}

	t.Run("stock plugin performs zero IO and returns nil quote", func(t *testing.T) {
		s, account, httpClient := generationPluginFixture(t, "xai")
		quote, err := s.QuoteTask(ctx, account, task)
		require.NoError(t, err)
		require.Nil(t, quote)
		require.Empty(t, httpClient.requests)
	})

	t.Run("pure quotePrice hook returns validated non-final quote", func(t *testing.T) {
		source := `
export const meta = { apiVersion: 1, key: "pure-quote", name: "Pure Quote", version: "1.0.0", author: { name: "test" }, models: ["test-video"], fetchMode: "per_task" };
export function buildSubmitRequest() { return { url: "https://api.example.com/submit", method: "POST" }; }
export function parseSubmitResponse() { return { taskId: "t1" }; }
export function buildQueryRequest() { return { url: "https://api.example.com/query", method: "GET" }; }
export function parseTaskResult() { return { status: "SUCCESS" }; }
export function quotePrice(ctx) {
  return { amount: "1.75", currency: "CNY", unit: "currency", source: "pure_quote_table" };
}
`
		s, account, httpClient := generationPluginSourceFixture(t, source)
		quote, err := s.QuoteTask(ctx, account, task)
		require.NoError(t, err)
		require.NotNil(t, quote)
		require.Equal(t, "1.75", quote.Amount.String())
		require.Equal(t, "CNY", quote.Currency)
		require.Equal(t, "currency", quote.Unit)
		require.False(t, quote.Final)
		require.Empty(t, httpClient.requests)
	})

	t.Run("HTTP buildQuoteRequest and parseQuoteResponse respect upstream safeguards and fail closed", func(t *testing.T) {
		source := `
export const meta = { apiVersion: 1, key: "http-quote", name: "HTTP Quote", version: "1.0.0", author: { name: "test" }, models: ["test-video"], fetchMode: "per_task" };
export function buildSubmitRequest() { return { url: "https://api.example.com/submit", method: "POST" }; }
export function parseSubmitResponse() { return { taskId: "t1" }; }
export function buildQueryRequest() { return { url: "https://api.example.com/query", method: "GET" }; }
export function parseTaskResult() { return { status: "SUCCESS" }; }
export function buildQuoteRequest(ctx) {
  return { url: ctx.baseUrl + "/v1/quote", method: "POST", body: { model: ctx.upstreamModel } };
}
export function parseQuoteResponse(ctx, resp) {
  return resp.body;
}
`
		s, account, httpClient := generationPluginSourceFixture(t, source)
		httpClient.status = 200
		httpClient.response = `{"amount":"2.50","currency":"USD","unit":"upstream_cost","source":"upstream_quote_api","expires_at":"2099-01-01T00:00:00Z"}`
		quote, err := s.QuoteTask(ctx, account, task)
		require.NoError(t, err)
		require.NotNil(t, quote)
		require.Equal(t, "2.5", quote.Amount.String())
		require.Equal(t, "USD", quote.Currency)
		require.False(t, quote.Final)
		require.Len(t, httpClient.requests, 1)
		require.Equal(t, "/v1/quote", httpClient.requests[0].URL.Path)

		// Expired quote fails closed.
		httpClient.response = `{"amount":"2.50","currency":"USD","unit":"upstream_cost","source":"upstream_quote_api","expires_at":"2020-01-01T00:00:00Z"}`
		_, err = s.QuoteTask(ctx, account, task)
		require.ErrorIs(t, err, ErrGenerationQuoteExpired)
	})
}
