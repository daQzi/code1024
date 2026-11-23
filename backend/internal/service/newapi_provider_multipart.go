package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/relaymetrics"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/modelmodifier"
	"github.com/Wei-Shaw/sub2api/newapi-engine/provider"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	newAPIProviderMultipartMaxBytes int64 = 32 << 20
	newAPIProviderMultipartMaxParts       = 64
)

type newAPIProviderMultipartInput struct {
	contentType string
	fields      map[string][]string
	files       []provider.ClientFile
	indexed     map[string]provider.ClientFile
	rawBody     []byte
}

func parseNewAPIProviderMultipart(contentType string, rawBody []byte) (*newAPIProviderMultipartInput, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return nil, errors.New("invalid multipart content type")
	}
	boundary := strings.TrimSpace(params["boundary"])
	if boundary == "" {
		return nil, errors.New("multipart boundary is required")
	}
	if int64(len(rawBody)) > newAPIProviderMultipartMaxBytes {
		return nil, errors.New("Provider multipart request exceeds size limit")
	}
	reader := multipart.NewReader(bytes.NewReader(rawBody), boundary)
	fields := make(map[string][]string)
	var files []provider.ClientFile
	var totalBytes int64
	partsCount := 0
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("invalid multipart payload")
		}
		partsCount++
		if partsCount > newAPIProviderMultipartMaxParts {
			_ = part.Close()
			return nil, errors.New("Provider multipart request exceeds part limit")
		}
		name := strings.TrimSpace(part.FormName())
		if name == "" {
			_ = part.Close()
			continue
		}
		remaining := newAPIProviderMultipartMaxBytes - totalBytes
		if remaining < 0 {
			_ = part.Close()
			return nil, errors.New("Provider multipart request exceeds size limit")
		}
		data, readErr := io.ReadAll(io.LimitReader(part, remaining+1))
		filename := strings.TrimSpace(part.FileName())
		partCT := strings.TrimSpace(part.Header.Get("Content-Type"))
		_ = part.Close()
		if readErr != nil {
			return nil, errors.New("failed to read multipart part")
		}
		totalBytes += int64(len(data))
		if totalBytes > newAPIProviderMultipartMaxBytes {
			return nil, errors.New("Provider multipart request exceeds size limit")
		}
		if filename != "" {
			copied := bytes.Clone(data)
			files = append(files, provider.ClientFile{
				Field:       name,
				Filename:    filename,
				ContentType: partCT,
				Size:        int64(len(copied)),
				Open: func() (io.ReadCloser, error) {
					return io.NopCloser(bytes.NewReader(copied)), nil
				},
			})
			continue
		}
		fields[name] = append(fields[name], string(data))
	}
	_, indexed := provider.BuildRequestForm(fields, files, "")
	return &newAPIProviderMultipartInput{
		contentType: contentType,
		fields:      fields,
		files:       files,
		indexed:     indexed,
		rawBody:     rawBody,
	}, nil
}

func (m *newAPIProviderMultipartInput) buildPassthrough(upstreamModel string) ([]byte, string, error) {
	if m == nil {
		return nil, "", errors.New("multipart input is missing")
	}
	formFields := make(map[string][]string, len(m.fields))
	for k, v := range m.fields {
		formFields[k] = slices.Clone(v)
	}
	if upstreamModel != "" {
		formFields["model"] = []string{upstreamModel}
	}
	formFiles := make([]jsplugin.ConvertedFormFile, 0, len(m.files))
	grouped := make(map[string]int)
	for _, file := range m.files {
		idx := grouped[file.Field]
		grouped[file.Field] = idx + 1
		ref := file.Field
		if idx > 0 {
			ref = fmt.Sprintf("%s#%d", file.Field, idx)
		}
		formFiles = append(formFiles, jsplugin.ConvertedFormFile{
			Field:       file.Field,
			From:        ref,
			Filename:    file.Filename,
			ContentType: file.ContentType,
		})
	}
	step := jsplugin.ConvertedRequest{
		Form: &jsplugin.ConvertedForm{Fields: formFields, Files: formFiles},
	}
	body, ct, _, err := provider.BuildUpstreamBody(step, m.indexed, true, newAPIProviderMultipartMaxBytes)
	return body, ct, err
}

func materializeProviderStepBody(step jsplugin.ConvertedRequest, mp *newAPIProviderMultipartInput) ([]byte, string, bool, error) {
	if step.Form != nil || step.File != "" {
		if mp == nil {
			return nil, "", false, errors.New("Provider conversion requires an unsupported transport or stream mode change")
		}
		return provider.BuildUpstreamBody(step, mp.indexed, true, newAPIProviderMultipartMaxBytes)
	}
	if len(step.Body) > MaxRequestPluginBody || (step.Body != nil && !gjson.ValidBytes(step.Body)) {
		return nil, "", false, errors.New("Provider conversion requires a bounded JSON body")
	}
	body := step.Body
	if len(body) > 0 && bytes.Contains(body, []byte(`"$file"`)) {
		var files map[string]provider.ClientFile
		if mp != nil {
			files = mp.indexed
		}
		var err error
		body, err = provider.ReplaceFileMarkers(body, files, newAPIProviderMultipartMaxBytes)
		if err != nil {
			return nil, "", false, err
		}
	}
	contentType := step.ContentType
	if contentType == "" && mp != nil && len(body) > 0 {
		contentType = "application/json"
	}
	return body, contentType, false, nil
}

func (s *OpenAIGatewayService) HasNewAPIProviderImageModel(ctx context.Context, groupID *int64, endpoint, model string) bool {
	path := normalizeOpenAIImagesEndpointPath(endpoint)
	if path == "" {
		return false
	}
	return s.HasNewAPIProviderRouteModel(ctx, groupID, path, model)
}

func (s *OpenAIGatewayService) HasNewAPIProviderRouteModel(ctx context.Context, groupID *int64, path, model string) bool {
	if s == nil || s.accountRepo == nil || model == "" || path == "" {
		return false
	}
	var accounts []Account
	var err error
	if groupID != nil {
		accounts, err = s.accountRepo.ListSchedulableByGroupIDAndPlatform(ctx, *groupID, PlatformOpenAI)
	} else {
		accounts, err = s.accountRepo.ListSchedulableByPlatform(ctx, PlatformOpenAI)
	}
	if err != nil {
		return false
	}
	for i := range accounts {
		a := &accounts[i]
		if a.HasNewAPIPlugin() && a.SupportsNewAPIProviderModel(path, model) {
			return true
		}
	}
	return false
}

// NewAPIProviderAudioCapability returns the plugin-scoped scheduler capability
// for supported Provider audio endpoints, or empty string if unsupported.
func NewAPIProviderAudioCapability(path string) OpenAIEndpointCapability {
	switch path {
	case newAPIProviderAudioTranscriptions:
		return OpenAIEndpointCapability("plugin_provider_audio_transcriptions")
	case newAPIProviderAudioTranslations:
		return OpenAIEndpointCapability("plugin_provider_audio_translations")
	case newAPIProviderAudioSpeech:
		return OpenAIEndpointCapability("plugin_provider_audio_speech")
	default:
		return ""
	}
}

// ExtractNewAPIProviderMultipartModel reads the model field from either a JSON
// body or a multipart/form-data body for Provider audio routes.
func ExtractNewAPIProviderMultipartModel(contentType string, body []byte) string {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if strings.EqualFold(mediaType, "multipart/form-data") {
		mp, err := parseNewAPIProviderMultipart(contentType, body)
		if err != nil {
			return ""
		}
		if values := mp.fields["model"]; len(values) > 0 {
			return strings.TrimSpace(values[0])
		}
		return ""
	}
	if !gjson.ValidBytes(body) {
		return ""
	}
	model := gjson.GetBytes(body, "model")
	if model.Type != gjson.String {
		return ""
	}
	return strings.TrimSpace(model.Str)
}

func (s *OpenAIGatewayService) forwardNewAPIProviderImages(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	parsed *OpenAIImagesRequest,
	path, requestModel, channelMappedModel string,
) (result *OpenAIForwardResult, retErr error) {
	ctx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer release()
	start := time.Now()
	c.Set(imageHTTPPerformanceKey, relaymetrics.Unknown)
	beginUpstreamResponseModelObservation(c)
	ClearActualOpenAIUpstreamEndpoint(c)

	if s.newAPIPlugins == nil || (path != newAPIProviderImagesGenerations && path != newAPIProviderImagesEdits) {
		writeNewAPIProviderError(c, path, http.StatusServiceUnavailable, "plugin_unavailable", "", "Provider Images endpoint is not available")
		return nil, errors.New("Provider Images endpoint unavailable")
	}
	upstreamModel := resolveOpenAIForwardModel(account, requestModel, channelMappedModel)
	report := beginRelayReportAttempt(ctx, account, upstreamModel, true)
	defer func() { report.result(result) }()

	if err := pkghttputil.ReserveRequestBodyMemory(ctx, newAPIProviderMemory); err != nil {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		c.Header("Retry-After", "1")
		writeNewAPIProviderError(c, path, http.StatusServiceUnavailable, "overloaded_error", "", "Provider conversion capacity is busy")
		return nil, err
	}
	if requestModel == "" || !account.SupportsNewAPIProviderModel(path, requestModel) || !account.newAPIProviderBinding().supports(path, modelmodifier.Base(upstreamModel)) {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		writeNewAPIProviderError(c, path, http.StatusBadRequest, "invalid_request_error", "", "Provider does not support this Images endpoint and model")
		return nil, errors.New("Provider model or endpoint mismatch")
	}

	var mpInput *newAPIProviderMultipartInput
	var upstreamBody []byte
	if parsed.Multipart {
		if err := pkghttputil.ReserveRequestBodyMemory(ctx, int64(len(body))*2); err != nil {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			c.Header("Retry-After", "1")
			writeNewAPIProviderError(c, path, http.StatusServiceUnavailable, "overloaded_error", "", "Provider multipart memory capacity is busy")
			return nil, err
		}
		var mpErr error
		mpInput, mpErr = parseNewAPIProviderMultipart(parsed.ContentType, body)
		if mpErr != nil {
			writeNewAPIProviderError(c, path, http.StatusBadRequest, "invalid_request_error", "", mpErr.Error())
			return nil, mpErr
		}
		if mpForm, parseErr := ParseRequestPluginMultipart(parsed.ContentType, body); parseErr == nil {
			ctx = WithRequestPluginMultipart(ctx, mpForm)
			if report != nil && !report.hasRequestProjection {
				report.SetMultipart(mpForm)
			}
			upstreamBody = mpForm.View
		}
	} else {
		if len(body) > MaxRequestPluginBody || !validNewAPIProviderJSON(body) {
			writeNewAPIProviderError(c, path, http.StatusBadRequest, "invalid_request_error", "", "Provider Images JSON request is invalid or exceeds limit")
			return nil, errors.New("invalid Provider Images JSON body")
		}
		ctx = context.WithValue(ctx, requestPluginProviderBodyKey{}, body)
		var setErr error
		upstreamBody, setErr = sjson.SetBytes(body, "model", upstreamModel)
		if setErr != nil {
			return nil, setErr
		}
	}

	upstreamCtx, cancel := context.WithCancel(WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamPublicHostsOnly(ctx)))
	defer cancel()
	setupCtx, setupCancel := context.WithTimeout(upstreamCtx, newAPIProviderSetupTimeout)
	exchange, err := s.newAPIPlugins.buildProviderRequestWithMultipart(setupCtx, account, path, upstreamModel, upstreamBody, mpInput)
	setupCancel()
	if err != nil {
		var processingErr *RequestPluginFailure
		if errors.As(err, &processingErr) {
			return nil, writeProviderRequestPluginFailure(c, processingErr)
		}
		writeNewAPIProviderError(c, path, http.StatusServiceUnavailable, "plugin_unavailable", "", "Provider Images request preparation failed")
		return nil, fmt.Errorf("Provider Images setup: %w", err)
	}

	chainTimer := time.AfterFunc(newAPIProviderChainTimeout, cancel)
	defer chainTimer.Stop()
	var resp *http.Response
	var req *http.Request
	continuationStarted := false
	defer func() {
		if (continuationStarted || exchange.credentialReplayUnsafe) && retErr != nil {
			var retry *UpstreamFailoverError
			if errors.As(retErr, &retry) {
				stopped := *retry
				stopped.NextAccountAction = NextAccountStop
				stopped.RetryableOnSameAccount = false
				retErr = &stopped
			}
		}
	}()
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()

	credentialRejected, renewed := false, false
	for followups := 0; ; {
		req = exchange.request.WithContext(upstreamCtx)
		SetOpsUpstreamModel(c, upstreamModel)
		SetActualOpenAIUpstreamEndpoint(c, req.URL.Path)
		resp, err = s.doNewAPIProviderTransport(upstreamCtx, req, exchange, account)
		if err != nil {
			return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
		}
		credentialRejected, err = s.newAPIPlugins.readProviderStep(upstreamCtx, exchange, resp)
		if err != nil {
			writeNewAPIProviderError(c, path, http.StatusBadGateway, "upstream_error", "", "Provider Images response conversion failed")
			return nil, err
		}
		if credentialRejected {
			s.newAPIPlugins.providerTokens.invalidate(exchange.tokenKey, exchange.hookCtx.Token)
			if !renewed && exchange.renew != nil && !exchange.credentialReplayUnsafe {
				renewed = true
				s.readOpenAIUpstreamError(resp)
				renewCtx, renewCancel := context.WithTimeout(upstreamCtx, newAPIProviderSetupTimeout)
				replacement, renewErr := exchange.renew(renewCtx)
				renewCancel()
				if renewErr == nil {
					_ = resp.Body.Close()
					exchange = replacement
					continue
				}
			}
		}
		if exchange.followup == nil {
			break
		}
		continuationStarted = true
		if followups >= newAPIProviderMaxFollowups {
			writeNewAPIProviderError(c, path, http.StatusBadGateway, "upstream_error", "", "Provider exceeded its chained request limit")
			return nil, errors.New("Provider exceeded 32 follow-up requests")
		}
		_ = resp.Body.Close()
		if err := waitProviderFollowup(upstreamCtx, exchange.followup.Delay); err != nil {
			if ctx.Err() == nil {
				writeNewAPIProviderError(c, path, http.StatusGatewayTimeout, "upstream_error", "", "Provider chain time budget exceeded")
			}
			return nil, err
		}
		stepCtx, stepCancel := context.WithTimeout(upstreamCtx, newAPIProviderSetupTimeout)
		next, nextErr := s.newAPIPlugins.buildProviderFollowup(stepCtx, exchange.chain, *exchange.followup, exchange.followupState)
		stepCancel()
		if nextErr != nil {
			writeNewAPIProviderError(c, path, http.StatusBadGateway, "upstream_error", "", "Provider chained request preparation failed")
			return nil, nextErr
		}
		exchange = next
		followups++
	}
	if !chainTimer.Stop() {
		cancel()
		writeNewAPIProviderError(c, path, http.StatusGatewayTimeout, "upstream_error", "", "Provider chain time budget exceeded")
		return nil, context.DeadlineExceeded
	}
	if credentialRejected && resp.StatusCode != http.StatusUnauthorized {
		s.handleOpenAIAccountUpstreamError(ctx, account, http.StatusUnauthorized, nil, []byte(`{"error":{"message":"Provider credential was rejected"}}`), upstreamModel)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, message := s.readOpenAIUpstreamError(resp)
		if failover := s.failoverOpenAIUpstreamHTTPError(ctx, c, account, resp, responseBody, message, upstreamModel); failover != nil {
			return nil, failover
		}
		return s.handleOpenAIImagesErrorResponse(upstreamCtx, resp, c, account, upstreamModel)
	}

	nonStreamUsage, nonStreamCount, nonStreamSizes, err := s.handleOpenAIImagesNonStreamingResponse(upstreamCtx, resp, c, account, parsed)
	if err != nil {
		return nil, err
	}
	imageCount := resolveBillableImageCount(nonStreamCount, parsed.N, imageCountBillingEnabled(getAPIKeyFromContext(c)))
	return &OpenAIForwardResult{
		RequestID:                     firstNonEmptyString(resp.Header.Get("x-request-id"), resp.Header.Get("request-id")),
		UpstreamHeaders:               resp.Header,
		Usage:                         nonStreamUsage,
		Model:                         requestModel,
		BillingModel:                  upstreamModel,
		UpstreamModel:                 upstreamModel,
		UpstreamResponseModel:         observedUpstreamResponseModel(c),
		UpstreamResponseModelConflict: observedUpstreamResponseModelConflict(c),
		Stream:                        false,
		ResponseHeaders:               resp.Header.Clone(),
		Duration:                      time.Since(start),
		UpstreamEndpoint:              req.URL.Path,
		ImageEndpoint:                 true,
		ImageCount:                    imageCount,
		ImageSize:                     parsed.SizeTier,
		ImageInputSize:                parsed.Size,
		ImageOutputSizes:              nonStreamSizes,
		PerformanceOutcome:            imageHTTPPerformance(c, nonStreamCount),
	}, nil
}

// ForwardNewAPIProviderMultipart serves the non-streaming Provider Audio routes.
func (s *OpenAIGatewayService) ForwardNewAPIProviderMultipart(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	contentType, path, model, defaultMappedModel string,
) (result *OpenAIForwardResult, retErr error) {
	ctx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer release()
	start := time.Now()
	ClearActualOpenAIUpstreamEndpoint(c)
	if s.newAPIPlugins == nil {
		writeNewAPIProviderError(c, path, http.StatusServiceUnavailable, "plugin_unavailable", "", "Provider service is unavailable")
		return nil, errors.New("Provider service unavailable")
	}
	upstreamModel := resolveOpenAIForwardModel(account, model, defaultMappedModel)
	report := beginRelayReportAttempt(ctx, account, upstreamModel, true)
	defer func() { report.result(result) }()
	if err := pkghttputil.ReserveRequestBodyMemory(ctx, newAPIProviderMemory); err != nil {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		c.Header("Retry-After", "1")
		writeNewAPIProviderError(c, path, http.StatusServiceUnavailable, "overloaded_error", "", "Provider conversion capacity is busy")
		return nil, err
	}
	if model == "" || !account.SupportsNewAPIProviderModel(path, model) || !account.newAPIProviderBinding().supports(path, modelmodifier.Base(upstreamModel)) {
		writeNewAPIProviderError(c, path, http.StatusBadRequest, "invalid_request_error", "", "Provider does not support this endpoint and model")
		return nil, errors.New("Provider model or endpoint mismatch")
	}
	price, priced := s.resolveProviderAudioPrice(ctx, getAPIKeyFromContext(c), path, upstreamModel, defaultMappedModel, model)
	if !priced {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalModelConfiguration)
		writeNewAPIProviderError(c, path, http.StatusBadRequest, "invalid_request_error", "", "Provider Audio requires an explicit per-request or group audio price")
		return nil, ErrModelPricingUnavailable
	}
	var mpInput *newAPIProviderMultipartInput
	var upstreamBody []byte
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if strings.EqualFold(mediaType, "multipart/form-data") {
		if err := pkghttputil.ReserveRequestBodyMemory(ctx, int64(len(body))*2); err != nil {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			c.Header("Retry-After", "1")
			writeNewAPIProviderError(c, path, http.StatusServiceUnavailable, "overloaded_error", "", "Provider multipart memory capacity is busy")
			return nil, err
		}
		var err error
		mpInput, err = parseNewAPIProviderMultipart(contentType, body)
		if err != nil {
			writeNewAPIProviderError(c, path, http.StatusBadRequest, "invalid_request_error", "", err.Error())
			return nil, err
		}
		if mpForm, parseErr := ParseRequestPluginMultipart(contentType, body); parseErr == nil {
			ctx = WithRequestPluginMultipart(ctx, mpForm)
			if report != nil && !report.hasRequestProjection {
				report.SetMultipart(mpForm)
			}
			upstreamBody = mpForm.View
		}
	} else {
		if len(body) > MaxRequestPluginBody || !validNewAPIProviderJSON(body) {
			writeNewAPIProviderError(c, path, http.StatusBadRequest, "invalid_request_error", "", "Provider JSON request is invalid or exceeds limit")
			return nil, errors.New("invalid Provider JSON body")
		}
		ctx = context.WithValue(ctx, requestPluginProviderBodyKey{}, body)
		var err error
		upstreamBody, err = sjson.SetBytes(body, "model", upstreamModel)
		if err != nil {
			return nil, err
		}
	}
	upstreamCtx, cancel := context.WithCancel(WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamPublicHostsOnly(ctx)))
	defer cancel()
	setupCtx, setupCancel := context.WithTimeout(upstreamCtx, newAPIProviderSetupTimeout)
	exchange, err := s.newAPIPlugins.buildProviderRequestWithMultipart(setupCtx, account, path, upstreamModel, upstreamBody, mpInput)
	setupCancel()
	if err != nil {
		var processingErr *RequestPluginFailure
		if errors.As(err, &processingErr) {
			return nil, writeProviderRequestPluginFailure(c, processingErr)
		}
		writeNewAPIProviderError(c, path, http.StatusServiceUnavailable, "plugin_unavailable", "", "Provider request preparation failed")
		return nil, err
	}
	var units float64
	if price.unit == providerAudioCharacters {
		input := gjson.GetBytes(exchange.hostBody, "input")
		if input.Type != gjson.String || input.String() == "" {
			writeNewAPIProviderError(c, path, http.StatusBadRequest, "invalid_request_error", "", "Speech metering requires a nonempty text input")
			return nil, errors.New("Provider Audio input cannot be metered")
		}
		units = float64(utf8.RuneCountInString(input.String())) / 1_000_000
	}
	chainTimer := time.AfterFunc(newAPIProviderChainTimeout, cancel)
	defer chainTimer.Stop()
	var resp *http.Response
	var req *http.Request
	continuationStarted := false
	defer func() {
		if (continuationStarted || exchange.credentialReplayUnsafe) && retErr != nil {
			var retry *UpstreamFailoverError
			if errors.As(retErr, &retry) {
				stopped := *retry
				stopped.NextAccountAction = NextAccountStop
				stopped.RetryableOnSameAccount = false
				retErr = &stopped
			}
		}
	}()
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	credentialRejected, renewed := false, false
	for followups := 0; ; {
		req = exchange.request.WithContext(upstreamCtx)
		SetOpsUpstreamModel(c, upstreamModel)
		SetActualOpenAIUpstreamEndpoint(c, req.URL.Path)
		resp, err = s.doNewAPIProviderTransport(upstreamCtx, req, exchange, account)
		if err != nil {
			return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
		}
		credentialRejected, err = s.newAPIPlugins.readProviderStep(upstreamCtx, exchange, resp)
		if err != nil {
			writeNewAPIProviderError(c, path, http.StatusBadGateway, "upstream_error", "", "Provider response conversion failed")
			return nil, err
		}
		if credentialRejected {
			s.newAPIPlugins.providerTokens.invalidate(exchange.tokenKey, exchange.hookCtx.Token)
			if !renewed && exchange.renew != nil && !exchange.credentialReplayUnsafe {
				renewed = true
				s.readOpenAIUpstreamError(resp)
				renewCtx, renewCancel := context.WithTimeout(upstreamCtx, newAPIProviderSetupTimeout)
				replacement, renewErr := exchange.renew(renewCtx)
				renewCancel()
				if renewErr == nil {
					_ = resp.Body.Close()
					exchange = replacement
					continue
				}
			}
		}
		if exchange.followup == nil {
			break
		}
		continuationStarted = true
		if followups >= newAPIProviderMaxFollowups {
			writeNewAPIProviderError(c, path, http.StatusBadGateway, "upstream_error", "", "Provider exceeded its chained request limit")
			return nil, errors.New("Provider exceeded 32 follow-up requests")
		}
		_ = resp.Body.Close()
		if err := waitProviderFollowup(upstreamCtx, exchange.followup.Delay); err != nil {
			return nil, err
		}
		stepCtx, stepCancel := context.WithTimeout(upstreamCtx, newAPIProviderSetupTimeout)
		next, nextErr := s.newAPIPlugins.buildProviderFollowup(stepCtx, exchange.chain, *exchange.followup, exchange.followupState)
		stepCancel()
		if nextErr != nil {
			writeNewAPIProviderError(c, path, http.StatusBadGateway, "upstream_error", "", "Provider chained request preparation failed")
			return nil, nextErr
		}
		exchange = next
		followups++
	}
	if !chainTimer.Stop() {
		cancel()
		writeNewAPIProviderError(c, path, http.StatusGatewayTimeout, "upstream_error", "", "Provider chain time budget exceeded")
		return nil, context.DeadlineExceeded
	}
	if credentialRejected && resp.StatusCode != http.StatusUnauthorized {
		s.handleOpenAIAccountUpstreamError(ctx, account, http.StatusUnauthorized, nil, []byte(`{"error":{"message":"Provider credential was rejected"}}`), upstreamModel)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, newAPIProviderMultipartMaxBytes+1))
	if err != nil || int64(len(payload)) > newAPIProviderMultipartMaxBytes {
		writeNewAPIProviderError(c, path, http.StatusBadGateway, "upstream_error", "", "Provider response exceeds limit")
		return nil, errors.New("Provider response exceeds limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if failover := s.failoverOpenAIUpstreamHTTPError(ctx, c, account, resp, payload, string(payload), upstreamModel); failover != nil {
			return nil, failover
		}
		writeNewAPIProviderError(c, path, http.StatusBadGateway, "upstream_error", "", "Provider Audio request failed")
		return nil, errors.New("Provider Audio upstream returned a non-success status")
	}
	respCT, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if !validProviderAudioResponse(path, respCT, payload) {
		writeNewAPIProviderError(c, path, http.StatusBadGateway, "upstream_error", "", "Provider Audio returned an invalid response")
		return nil, errors.New("invalid Provider Audio response")
	}
	var audioUsage *AudioUsage
	switch price.unit {
	case providerAudioDuration:
		seconds, err := providerAudioSeconds(respCT, payload)
		if err != nil {
			writeNewAPIProviderError(c, path, http.StatusBadGateway, "upstream_error", "", "Provider Audio response lacks verified duration")
			return nil, err
		}
		units = seconds / 3600
		audioUsage = &AudioUsage{Mode: "stt", DurationOrUnits: units}
	case providerAudioCharacters:
		audioUsage = &AudioUsage{Mode: "tts", DurationOrUnits: units}
	case providerAudioRequest:
		units = 1
	}
	audioPrice := price.amount * units
	if !validProviderAudioPrice(audioPrice) {
		writeNewAPIProviderError(c, path, http.StatusBadGateway, "upstream_error", "", "Provider Audio cost exceeds bounds")
		return nil, errors.New("Provider Audio cost exceeds bounds")
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	respMediaType, _, _ := mime.ParseMediaType(respCT)
	if strings.HasPrefix(strings.ToLower(respMediaType), "audio/") || strings.EqualFold(respMediaType, "application/octet-stream") {
		c.Header("Content-Security-Policy", "sandbox")
	}
	c.Data(resp.StatusCode, respCT, payload)
	return &OpenAIForwardResult{
		RequestID:          firstNonEmptyString(resp.Header.Get("x-request-id"), resp.Header.Get("request-id")),
		UpstreamHeaders:    resp.Header,
		Model:              model,
		BillingModel:       upstreamModel,
		UpstreamModel:      upstreamModel,
		Duration:           time.Since(start),
		UpstreamEndpoint:   req.URL.Path,
		PerformanceOutcome: relaymetrics.Success,
		AudioUsage:         audioUsage,
		providerAudioPrice: &audioPrice,
	}, nil
}

type providerAudioUnit uint8

const (
	providerAudioRequest providerAudioUnit = iota
	providerAudioCharacters
	providerAudioDuration
)

type providerAudioPrice struct {
	unit   providerAudioUnit
	amount float64
}

func validProviderAudioPrice(price float64) bool {
	return price >= 0 && !math.IsNaN(price) && !math.IsInf(price, 0)
}

func (s *OpenAIGatewayService) resolveProviderAudioPrice(ctx context.Context, key *APIKey, path, model string, alternates ...string) (providerAudioPrice, bool) {
	for _, candidate := range usageBillingModelCandidates(model, alternates...) {
		price := s.resolveOpenAIChannelPricing(ctx, candidate, key)
		if price == nil {
			continue
		}
		if price.Mode == BillingModePerRequest && price.channelPricing != nil && price.channelPricing.PerRequestPrice != nil && len(price.RequestTiers) == 0 {
			flat := *price.channelPricing.PerRequestPrice
			return providerAudioPrice{unit: providerAudioRequest, amount: flat}, validProviderAudioPrice(flat)
		}
		break
	}
	if cfg := groupAudioPriceConfigFromAPIKey(key); cfg != nil {
		unit, price := providerAudioDuration, cfg.STTPerHour
		if path == newAPIProviderAudioSpeech {
			unit, price = providerAudioCharacters, cfg.TTSPerMChars
		}
		if price != nil {
			return providerAudioPrice{unit: unit, amount: *price}, validProviderAudioPrice(*price)
		}
	}
	return providerAudioPrice{}, false
}

func providerAudioSeconds(contentType string, body []byte) (float64, error) {
	if contentType != "application/json" || !validNewAPIProviderJSON(body) {
		return 0, errors.New("duration billing requires a JSON audio response")
	}
	root := gjson.ParseBytes(body)
	seconds := root.Get("usage.seconds")
	if !seconds.Exists() {
		seconds = root.Get("duration")
	}
	value := seconds.Float()
	if seconds.Type != gjson.Number || value <= 0 || value > 24*60*60 {
		return 0, errors.New("audio duration is missing or invalid")
	}
	return value, nil
}

func validProviderAudioResponse(path, contentType string, body []byte) bool {
	if len(body) == 0 {
		return false
	}
	if path == newAPIProviderAudioSpeech {
		return strings.HasPrefix(contentType, "audio/") || contentType == "application/octet-stream"
	}
	switch contentType {
	case "application/json":
		return gjson.ValidBytes(body) && gjson.GetBytes(body, "text").Type == gjson.String && !gjson.GetBytes(body, "error").Exists()
	case "text/plain", "text/vtt", "application/x-subrip":
		return true
	default:
		return false
	}
}

func (m *newAPIProviderMultipartInput) clone() *newAPIProviderMultipartInput {
	if m == nil {
		return nil
	}
	fields := make(map[string][]string, len(m.fields))
	for k, v := range m.fields {
		fields[k] = slices.Clone(v)
	}
	return &newAPIProviderMultipartInput{
		contentType: m.contentType,
		fields:      fields,
		files:       slices.Clone(m.files),
		indexed:     maps.Clone(m.indexed),
		rawBody:     bytes.Clone(m.rawBody),
	}
}
