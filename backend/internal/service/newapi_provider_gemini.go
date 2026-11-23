package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/googleapi"
	"github.com/Wei-Shaw/sub2api/internal/pkg/relaymetrics"
	"github.com/Wei-Shaw/sub2api/newapi-engine/provider"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const OpenAIEndpointCapabilityPluginProviderGemini OpenAIEndpointCapability = "plugin_provider_gemini"

func providerGeminiPath(path string) (model string, stream, ok bool) {
	tail, found := strings.CutPrefix(path, "/v1beta/models/")
	if !found || strings.ContainsAny(tail, "/\\?#") {
		return "", false, false
	}
	colon := strings.LastIndexByte(tail, ':')
	if colon <= 0 {
		return "", false, false
	}
	switch tail[colon+1:] {
	case "generateContent":
		return tail[:colon], false, true
	case "streamGenerateContent":
		return tail[:colon], true, true
	}
	return "", false, false
}

func providerGeminiErrorStatus(status int) string { return googleapi.HTTPStatusToGoogleStatus(status) }

func (a *Account) SupportsNewAPIProviderGeminiModel(model string) bool {
	return a.SupportsNewAPIProviderModel("/v1beta/models/"+model+":generateContent", model) ||
		a.SupportsNewAPIProviderModel("/v1beta/models/"+model+":streamGenerateContent", model)
}

func (s *OpenAIGatewayService) ForwardNewAPIProviderGemini(ctx context.Context, c *gin.Context, account *Account, body []byte, path, mappedModel string) (*OpenAIForwardResult, error) {
	return s.forwardNewAPIProvider(ctx, c, account, body, path, mappedModel)
}

func (s *OpenAIGatewayService) finishNewAPIProviderGemini(ctx context.Context, c *gin.Context, resp *http.Response, model, upstreamModel, billingModel string, stream bool, start time.Time, tools *newAPIProviderToolUsage, path string) (*OpenAIForwardResult, error) {
	result := &OpenAIForwardResult{
		RequestID: firstNonEmptyString(resp.Header.Get("x-request-id"), resp.Header.Get("x-goog-request-id")),
		Model:     model, UpstreamModel: upstreamModel, BillingModel: billingModel, UpstreamHeaders: resp.Header, Stream: stream,
	}
	defer func() { result.Duration = time.Since(start) }()
	seenUsage, terminal := false, false
	observe := func(raw []byte) error {
		if !validNewAPIProviderJSON(raw) || gjson.GetBytes(raw, "error").Type != gjson.Null {
			return errors.New("invalid Gemini provider response")
		}
		for _, candidate := range gjson.GetBytes(raw, "candidates").Array() {
			if result.FirstTokenMs == nil && len(candidate.Get("content.parts").Array()) > 0 {
				ms := int(time.Since(start).Milliseconds())
				result.FirstTokenMs = &ms
			}
			if finish := candidate.Get("finishReason").String(); finish != "" && finish != "FINISH_REASON_UNSPECIFIED" {
				terminal = true
			}
		}
		if gjson.GetBytes(raw, "promptFeedback.blockReason").String() != "" {
			terminal = true
		}
		if usage := gjson.GetBytes(raw, "usageMetadata"); usage.IsObject() {
			values := [4]int{}
			present := false
			for i, field := range []string{"promptTokenCount", "candidatesTokenCount", "thoughtsTokenCount", "cachedContentTokenCount"} {
				v := usage.Get(field)
				if v.Exists() {
					if v.Type != gjson.Number || v.Float() != float64(v.Int()) || v.Int() < 0 || v.Int() > 1_000_000_000 {
						return errors.New("invalid Gemini token usage")
					}
					values[i], present = int(v.Int()), true
				}
			}
			if present {
				seenUsage = true
				result.Usage.InputTokens = max(result.Usage.InputTokens, max(values[0], values[3]))
				result.Usage.OutputTokens = max(result.Usage.OutputTokens, values[1]+values[2])
				result.Usage.CacheReadInputTokens = max(result.Usage.CacheReadInputTokens, values[3])
			}
		}
		if tools != nil && tools.protocol != nil {
			return tools.protocol.ObserveVendor(provider.VendorGemini, raw, stream)
		}
		return nil
	}
	fail := func(err error) (*OpenAIForwardResult, error) {
		result.PerformanceOutcome = relaymetrics.Unknown
		if !c.Writer.Written() {
			writeNewAPIProviderError(c, path, http.StatusBadGateway, "upstream_error", "", "Provider Gemini returned an incomplete or invalid result")
		} else if ctx.Err() == nil {
			_, _ = fmt.Fprint(c.Writer, "data: {\"error\":{\"code\":502,\"status\":\"UNAVAILABLE\",\"message\":\"Provider Gemini stream failed\"}}\n\n")
			c.Writer.Flush()
		}
		if !seenUsage {
			return nil, err
		}
		return result, err // Preserve observed usage on cancellation or truncation.
	}
	if !stream {
		raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxRequestPluginBody+1))
		if err != nil || len(raw) > MaxRequestPluginBody {
			return fail(errors.New("Gemini response exceeds limits or was interrupted"))
		}
		if err := observe(raw); err != nil {
			return fail(err)
		}
		if !seenUsage || !terminal {
			return fail(errors.New("Gemini response has no terminal usage"))
		}
		c.Data(http.StatusOK, "application/json", raw)
	} else {
		sseHeadersSent := false
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 4096), MaxRequestPluginBody)
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			data, found := bytes.CutPrefix(line, []byte("data:"))
			if !found {
				continue
			}
			data = bytes.TrimSpace(data)
			if len(data) == 0 {
				continue
			}
			if err := observe(data); err != nil {
				return fail(err)
			}
			if !sseHeadersSent {
				c.Header("Content-Type", "text/event-stream")
				c.Header("Cache-Control", "no-cache")
				c.Header("X-Accel-Buffering", "no")
				sseHeadersSent = true
			}
			if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", data); err != nil {
				return fail(err)
			}
			c.Writer.Flush()
		}
		if err := scanner.Err(); err != nil {
			return fail(err)
		}
		if !seenUsage || !terminal {
			return fail(errors.New("Gemini stream ended without terminal usage"))
		}
	}
	result.PerformanceOutcome = relaymetrics.Success
	return result, nil
}
