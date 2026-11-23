package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ProviderAudio forwards OpenAI Audio endpoints (/v1/audio/transcriptions,
// /v1/audio/translations, /v1/audio/speech) strictly to explicitly bound
// New API Provider accounts declaring that route and model.
func (h *OpenAIGatewayHandler) ProviderAudio(c *gin.Context, path string) {
	capability := service.NewAPIProviderAudioCapability(path)
	if capability == "" {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Audio endpoint is not supported")
		return
	}
	h.forwardProviderMediaOrGemini(c, path, capability, "", false)
}

// TryProviderGemini serves only advertised Provider generation routes. Native
// Gemini groups and non-generation actions stay on the original gateway.
func (h *OpenAIGatewayHandler) TryProviderGemini(c *gin.Context) bool {
	key, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || key == nil || key.Group == nil || effectiveAPIKeyPlatform(c, key) != service.PlatformOpenAI || h.gatewayService == nil {
		return false
	}
	model, action, err := parseGeminiModelAction(strings.TrimPrefix(c.Param("modelAction"), "/"))
	if err != nil || !service.IsSafeGeminiClientModelName(model) || (action != "generateContent" && action != "streamGenerateContent") {
		return false
	}
	routeModel := model
	if mapped, ok := service.ResolvedUpstreamModelFromContext(c.Request.Context()); ok && mapped != "" {
		routeModel = mapped
	}
	path := "/v1beta/models/" + routeModel + ":" + action
	if !h.gatewayService.HasNewAPIProviderRouteModel(c.Request.Context(), key.GroupID, path, routeModel) {
		return false
	}
	h.forwardProviderMediaOrGemini(c, path, service.OpenAIEndpointCapabilityPluginProviderGemini, routeModel, action == "streamGenerateContent")
	return true
}

func (h *OpenAIGatewayHandler) forwardProviderMediaOrGemini(c *gin.Context, path string, capability service.OpenAIEndpointCapability, urlModel string, stream bool) {
	gemini := urlModel != ""
	writeError := func(status int, code, message string) {
		if gemini {
			googleError(c, status, message)
		} else {
			h.errorResponse(c, status, code, message)
		}
	}
	c.Request = c.Request.WithContext(service.WithNewAPIProviderRouting(c.Request.Context(), path))
	streamStarted := false
	requestStart := time.Now()

	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		writeError(http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		writeError(http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(
		c,
		"handler.openai_gateway.provider",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
		zap.String("endpoint", path),
	)
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}

	contentType := c.GetHeader("Content-Type")
	if !gemini && strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "multipart/form-data") {
		if encoding := c.GetHeader("Content-Encoding"); encoding != "" && encoding != "identity" {
			writeError(http.StatusUnsupportedMediaType, "unsupported_media_type", "Multipart audio uploads require an uncompressed request body")
			return
		}
	}
	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if handleRequestBodyReadError(c, reqLog, err) {
			return
		}
		status, message := requestBodyReadFailure(reqLog, c.Request, err)
		writeError(status, "invalid_request_error", message)
		return
	}
	if len(body) == 0 {
		writeError(http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}
	reqModel := urlModel
	if !gemini {
		reqModel = service.ExtractNewAPIProviderMultipartModel(contentType, body)
	}
	if reqModel == "" {
		writeError(http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	ensureCompositeTargetPlatform(c, apiKey, reqModel)
	if !compositeTargetPlatformAllowed(c, apiKey, reqModel, service.PlatformOpenAI) {
		writeError(http.StatusBadRequest, "invalid_request_error", "Model is not supported by this endpoint for composite groups")
		return
	}
	if !gemini && !h.gatewayService.HasNewAPIProviderRouteModel(c.Request.Context(), apiKey.GroupID, path, reqModel) {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		writeError(http.StatusNotFound, "not_found_error", "No Provider plugin supports this endpoint and model")
		return
	}

	reqLog = reqLog.With(zap.String("model", reqModel))
	setOpsRequestContext(c, reqModel, stream)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(stream, false)))

	if gemini {
		if decision := h.checkSecurityAudit(c, reqLog, apiKey, subject, service.ContentModerationProtocolGemini, reqModel, body); decision != nil && !decision.AllowNextStage {
			googleSecurityAuditError(c, decision)
			return
		}
	} else if path == "/v1/audio/speech" {
		if decision := h.checkSecurityAudit(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIResponses, reqModel, body); decision != nil && !decision.AllowNextStage {
			h.openAISecurityAuditError(c, decision)
			return
		}
	}

	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, reqModel)
	forwardModel := openAIChannelForwardModel(channelMapping, reqModel)

	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())

	userReleaseFunc, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, false, &streamStarted, reqLog)
	if !acquired {
		return
	}
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		writeError(status, code, message)
		return
	}

	maxTokens := 1
	if gemini {
		maxTokens = 0 // Use the gateway's regular text estimate.
	}
	inflightDone, inflightErr := reserveInflightBalance(c, h.billingCacheService, h.gatewayService, apiKey, subscription, service.InflightEstimateRequest{Model: reqModel, BodyBytes: len(body), MaxTokens: maxTokens, Kind: service.InflightEstimateToken})
	if inflightErr != nil {
		status, code, message, retryAfter := billingErrorDetails(inflightErr)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		writeError(status, code, message)
		return
	}
	defer inflightDone()

	failedAccountIDs := make(map[int64]struct{})
	var lastFailoverErr *service.UpstreamFailoverError
	switchCount := 0
	maxAccountSwitches := h.maxAccountSwitches
	if maxAccountSwitches <= 0 {
		maxAccountSwitches = 3
	}
	writeFailoverExhausted := func(failoverErr *service.UpstreamFailoverError, written bool) {
		if !gemini {
			h.handleFailoverExhausted(c, failoverErr, written)
			return
		}
		status := http.StatusBadGateway
		message := "Upstream request failed"
		if failoverErr != nil {
			if failoverErr.StatusCode >= 400 && failoverErr.StatusCode <= 599 {
				status = failoverErr.StatusCode
			}
			if failoverErr.StatusCode == http.StatusTooManyRequests {
				status = http.StatusTooManyRequests
				message = "Upstream rate limit exceeded"
			}
		}
		if written || c.Writer.Written() {
			if stream && c.Request.Context().Err() == nil {
				_, _ = c.Writer.WriteString("data: {\"error\":{\"code\":502,\"status\":\"UNAVAILABLE\",\"message\":\"Provider Gemini stream failed\"}}\n\n")
				c.Writer.Flush()
			}
			return
		}
		googleError(c, status, message)
	}
	for {
		selection, _, err := h.gatewayService.SelectAccountWithSchedulerForCapability(
			c.Request.Context(),
			apiKey.GroupID,
			"",
			"",
			forwardModel,
			failedAccountIDs,
			service.OpenAIUpstreamTransportHTTPSSE,
			capability,
			false,
			false,
			false,
		)
		if err != nil {
			if failoverClientGone(c) {
				return
			}
			if len(failedAccountIDs) == 0 {
				cls := classifyNoAccountErrorFromGin(c, h.gatewayService, apiKey, reqModel, reqModel, service.PlatformOpenAI)
				writeError(cls.Status, cls.ErrType, cls.Message)
				return
			}
			if lastFailoverErr != nil {
				writeFailoverExhausted(lastFailoverErr, false)
			} else {
				writeError(http.StatusBadGateway, "api_error", "Upstream request failed")
			}
			return
		}
		if selection == nil || selection.Account == nil {
			cls := classifyNoAccountErrorFromGin(c, h.gatewayService, apiKey, reqModel, reqModel, service.PlatformOpenAI)
			writeError(cls.Status, cls.ErrType, cls.Message)
			return
		}
		account := selection.Account
		setOpsSelectedAccount(c, account.ID, account.Platform)
		accountReleaseFunc, slotResult := h.acquireResponsesAccountSlot(c, apiKey.GroupID, "", selection, false, &streamStarted, reqLog)
		if slotResult != openAISlotAcquireOK {
			return
		}
		writerSizeBeforeForward := c.Writer.Size()
		result, err := func() (*service.OpenAIForwardResult, error) {
			defer func() {
				if accountReleaseFunc != nil {
					accountReleaseFunc()
				}
			}()
			if gemini {
				return h.gatewayService.ForwardNewAPIProviderGemini(c.Request.Context(), c, account, body, path, channelMapping.MappedModel)
			}
			return h.gatewayService.ForwardNewAPIProviderMultipart(c.Request.Context(), c, account, body, contentType, path, reqModel, channelMapping.MappedModel)
		}()
		setOpenAIForwardPerformance(c, result, err)
		if err != nil && (!gemini || result == nil) {
			var failoverErr *service.UpstreamFailoverError
			if errors.As(err, &failoverErr) {
				if c.Writer.Size() != writerSizeBeforeForward {
					writeFailoverExhausted(failoverErr, true)
					return
				}
				h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, reqModel, false, result), false, nil, err)
				if failoverClientGone(c) || !failoverErr.ShouldRetryNextAccount() || switchCount >= maxAccountSwitches {
					writeFailoverExhausted(failoverErr, false)
					return
				}
				h.gatewayService.RecordOpenAIAccountSwitch()
				failedAccountIDs[account.ID] = struct{}{}
				lastFailoverErr = failoverErr
				switchCount++
				continue
			}
			h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, reqModel, false, result), false, nil, err)
			if !openAIForwardErrorAlreadyCommunicated(c, writerSizeBeforeForward, err) {
				if gemini {
					writeError(http.StatusBadGateway, "upstream_error", "Upstream request failed")
				} else {
					h.ensureForwardErrorResponse(c, false)
				}
			}
			return
		}
		h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, reqModel, false, result), err == nil, result.FirstTokenMs, err)

		userAgent := c.GetHeader("User-Agent")
		clientIP := ip.GetClientIP(c)
		requestPayloadHash := service.HashUsageRequestPayload(body)
		inboundEndpoint := GetInboundEndpoint(c)
		upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
		quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
		upstreamModel := ""
		if result != nil {
			upstreamModel = result.UpstreamModel
		}
		sessionID := service.ExtractClientSessionID(c)
		channelUsageFields := clientRequestedUsageFields(c, channelMapping, reqModel, upstreamModel)
		h.submitMandatoryUsageRecordTask(c.Request.Context(), func(ctx context.Context) {
			if err := h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
				Result:             result,
				APIKey:             apiKey,
				User:               apiKey.User,
				Account:            account,
				Subscription:       subscription,
				InboundEndpoint:    inboundEndpoint,
				UpstreamEndpoint:   upstreamEndpoint,
				UserAgent:          userAgent,
				IPAddress:          clientIP,
				RequestPayloadHash: requestPayloadHash,
				APIKeyService:      h.apiKeyService,
				QuotaPlatform:      quotaPlatform,
				SessionID:          sessionID,
				ChannelUsageFields: channelUsageFields,
			}); err != nil {
				logger.L().With(
					zap.String("component", "handler.openai_gateway.provider_audio"),
					zap.Int64("user_id", subject.UserID),
					zap.Int64("api_key_id", apiKey.ID),
					zap.String("model", reqModel),
					zap.Int64("account_id", account.ID),
				).Error("openai.provider_audio.record_usage_failed", zap.Error(err))
			}
		})
		return
	}
}
