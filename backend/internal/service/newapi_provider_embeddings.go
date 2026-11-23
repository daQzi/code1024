package service

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/relaymetrics"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Embeddings use the same pinned Provider transport, signing, credentials and
// bounded next/renewal chain as text, then return through the existing usage
// and billing path. No vector is turned into generated text or output tokens.
func (s *OpenAIGatewayService) finishNewAPIProviderEmbeddings(ctx context.Context, c *gin.Context, account *Account, resp *http.Response, req *http.Request, model, upstreamModel string, started time.Time) (*OpenAIForwardResult, error) {
	if !newAPIProviderJSONContentType(resp.Header.Get("Content-Type")) {
		writeOpenAIEmbeddingsError(c, http.StatusBadGateway, "upstream_error", "Provider embeddings require a JSON response")
		return nil, errors.New("Provider embeddings response content type mismatch")
	}
	stop := context.AfterFunc(ctx, func() { _ = resp.Body.Close() })
	defer stop()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxRequestPluginBody+1))
	if err != nil || len(body) > MaxRequestPluginBody || ctx.Err() != nil {
		writeOpenAIEmbeddingsError(c, http.StatusBadGateway, "upstream_error", "Provider embeddings response is unavailable or exceeds 1 MiB")
		return nil, errors.New("Provider embeddings response read failed or exceeds limit")
	}
	root := gjson.ParseBytes(body)
	if !gjson.ValidBytes(body) || !root.IsObject() || !newAPIProviderUnambiguousJSON(root, 0) || embeddingsPerformance(body) != relaymetrics.Success || !newAPIProviderEmbeddingUsage(root.Get("usage")) {
		writeOpenAIEmbeddingsError(c, http.StatusBadGateway, "upstream_error", "Provider returned invalid embeddings")
		return nil, errors.New("Provider embeddings protocol mismatch")
	}
	usage := extractOpenAIEmbeddingsUsage(body)
	if observed := root.Get("model"); observed.Type == gjson.String {
		upstreamResponseModelObserverFromContext(c).Observe(observed.String(), true)
		body, err = sjson.SetBytes(body, "model", model)
		if err != nil {
			return nil, err
		}
	}
	writeOpenAIEmbeddingsUpstreamResponse(c, resp, body, s.responseHeaderFilter)
	return &OpenAIForwardResult{
		RequestID:       firstNonEmptyString(resp.Header.Get("x-request-id"), resp.Header.Get("request-id")),
		UpstreamHeaders: resp.Header, Model: model, BillingModel: upstreamModel, UpstreamModel: upstreamModel,
		UpstreamResponseModel: observedUpstreamResponseModel(c), UpstreamResponseModelConflict: observedUpstreamResponseModelConflict(c),
		Usage: usage, Duration: time.Since(started), UpstreamEndpoint: req.URL.Path, PerformanceOutcome: relaymetrics.Success,
	}, nil
}

// An omitted usage object cannot authorize a free request. Keep explicit zero
// valid while rejecting strings, negatives and counters outside exact JSON
// integer range, before the existing permissive native parser sees them.
func newAPIProviderEmbeddingUsage(usage gjson.Result) bool {
	if !usage.IsObject() {
		return false
	}
	input := false
	for _, path := range []string{"prompt_tokens", "input_tokens", "total_tokens", "completion_tokens", "output_tokens"} {
		value := usage.Get(path)
		if !value.Exists() {
			continue
		}
		if value.Type != gjson.Number || value.Num < 0 || value.Num > 1<<53 || value.Num != math.Trunc(value.Num) {
			return false
		}
		input = input || path == "prompt_tokens" || path == "input_tokens" || path == "total_tokens"
	}
	return input
}
