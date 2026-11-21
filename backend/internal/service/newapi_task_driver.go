package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/newapi/jsplugin"
	"github.com/Wei-Shaw/sub2api/internal/newapi/runner"
	pluginTask "github.com/Wei-Shaw/sub2api/internal/newapi/task"
)

type newAPITaskSession struct {
	service *NewAPIPluginService
	version *NewAPIPluginVersion
	client  *runner.Client
	account *Account
	config  json.RawMessage
	base    string
	hosts   []string
	token   json.RawMessage
}

func (s *NewAPIPluginService) withGenerationSession(ctx context.Context, account *Account, task *GenerationTask, fn func(*newAPITaskSession) error) error {
	copy := *account
	copy.Credentials = maps.Clone(account.Credentials)
	if task.Connection.BaseURL != "" {
		copy.Credentials["base_url"] = task.Connection.BaseURL
		copy.Credentials["newapi_upstream_kind"] = task.Connection.Kind
	}
	return s.withTaskSession(ctx, &copy, task.PluginVersionID, task.ConfigEncrypted, fn)
}

func (s *NewAPIPluginService) withTaskSession(ctx context.Context, account *Account, versionID int64, configEncrypted string, fn func(*newAPITaskSession) error) error {
	version, err := s.repo.Get(ctx, versionID)
	if err != nil {
		return err
	}
	if !version.Capabilities.Task {
		return errors.New("plugin is not a task driver")
	}
	if s.gateway == nil {
		return errors.New("plugin HTTP host service is unavailable")
	}
	base, err := s.gateway.validateUpstreamBaseURL(account.GetCredential("base_url"))
	if err != nil {
		return err
	}
	config := json.RawMessage(`{}`)
	if configEncrypted != "" {
		plain, err := s.encryptor.Decrypt(configEncrypted)
		if err != nil {
			return errors.New("cannot decrypt task configuration")
		}
		config = json.RawMessage(plain)
	}
	return s.pool.With(ctx, version.Source, func(client *runner.Client) error {
		config, err = client.ValidateConfig(ctx, config, false)
		if err != nil {
			return err
		}
		session := &newAPITaskSession{service: s, version: version, client: client, account: account, config: config, base: base, hosts: append([]string(nil), version.Meta.AllowedHosts...)}
		session.hosts = append(session.hosts, client.Meta.ChannelConfig.URLHosts(config)...)
		return fn(session)
	})
}

func (s *newAPITaskSession) call(ctx context.Context, hook string, path []string, args ...any) (json.RawMessage, error) {
	return s.client.CallWithFetcher(ctx, s.fetch, hook, path, args...)
}
func (s *newAPITaskSession) has(hook string) bool {
	return slices.Contains(s.client.Descriptor.Hooks, hook)
}
func (s *newAPITaskSession) fetch(ctx context.Context, f jsplugin.FetchRequest) (jsplugin.FetchResponse, error) {
	if f.BodyStream != nil {
		return jsplugin.FetchResponse{}, errors.New("media streams require a file host service")
	}
	if f.Method == "" {
		f.Method = http.MethodGet
	}
	if err := jsplugin.ValidateRequestHeaders(f.Headers); err != nil {
		return jsplugin.FetchResponse{}, err
	}
	if err := jsplugin.ValidateRequestURL(f.URL, s.base, s.hosts); err != nil {
		return jsplugin.FetchResponse{}, err
	}
	if _, err := s.service.gateway.validateOutboundURL(f.URL); err != nil {
		return jsplugin.FetchResponse{}, err
	}
	opCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamPublicHostsOnly(opCtx)), f.Method, f.URL, strings.NewReader(f.Body))
	if err != nil {
		return jsplugin.FetchResponse{}, err
	}
	for key, value := range f.Headers {
		req.Header.Set(key, value)
	}
	resp, err := s.service.gateway.httpUpstream.Do(req, s.proxy(), s.account.ID, s.account.Concurrency)
	if err != nil {
		return jsplugin.FetchResponse{}, errors.New("plugin upstream fetch failed")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, jsplugin.MaxFetchBodyBytes+1))
	if err != nil {
		return jsplugin.FetchResponse{}, err
	}
	if len(body) > jsplugin.MaxFetchBodyBytes {
		return jsplugin.FetchResponse{}, errors.New("plugin upstream fetch exceeds limit")
	}
	return jsplugin.FetchResponse{Status: resp.StatusCode, Headers: pluginResponseHeaders(resp.Header), Body: body}, nil
}
func (s *newAPITaskSession) proxy() string {
	if s.account.ProxyID != nil && s.account.Proxy != nil {
		return s.account.Proxy.URL()
	}
	return ""
}
func pluginResponseHeaders(headers http.Header) map[string]string {
	result := make(map[string]string)
	for key, values := range headers {
		if len(values) > 0 && !strings.EqualFold(key, "Set-Cookie") {
			result[key] = values[0]
		}
	}
	return result
}
func (s *newAPITaskSession) context(t *GenerationTask) map[string]any {
	kind := s.account.GetCredential("newapi_upstream_kind")
	if kind == "" {
		kind = "vendor"
	}
	return map[string]any{"baseUrl": s.base, "apiKey": s.account.GetCredential("api_key"), "config": s.config,
		"model": t.Model, "upstreamModel": t.UpstreamModel, "originModel": t.Model, "action": t.Action, "taskId": t.UpstreamID, "publicTaskId": t.ID,
		"requestBody": t.Request, "state": jsplugin.RawJSON(t.State), "data": jsplugin.RawJSON(t.Data), "taskData": jsplugin.RawJSON(t.Data), "usagePurpose": "billing_facts", "channelType": 61, "token": s.token, "upstream": map[string]any{"kind": kind}}
}

func (s *newAPITaskSession) credential() jsplugin.CredentialContext {
	return jsplugin.CredentialContext{BaseURL: s.base, APIKey: s.account.GetCredential("api_key"), Config: s.config, Token: s.token}
}
func (s *newAPITaskSession) exchange(ctx context.Context) error {
	if !s.has("exchangeKey") {
		return nil
	}
	values, err := s.client.Typed(ctx, s.fetch, "CallExchangeKey", s.credential())
	if err != nil {
		return err
	}
	if len(values) != 2 || string(values[1]) != "null" {
		return errors.New("plugin could not exchange the upstream key")
	}
	var result jsplugin.ExchangedToken
	if err = json.Unmarshal(values[0], &result); err != nil {
		return err
	}
	s.token = result.Token
	return nil
}

// sign runs the same validated Provider contract for task and artifact HTTP.
func (s *newAPITaskSession) sign(ctx context.Context, req *http.Request, body []byte) error {
	if !s.has("signRequest") {
		return nil
	}
	digest := sha256.Sum256(body)
	values, err := s.client.Typed(ctx, s.fetch, "CallSignRequest", s.credential(), req.Method, req.URL.String(), pluginResponseHeaders(req.Header), hex.EncodeToString(digest[:]))
	if err != nil {
		return err
	}
	var signed jsplugin.SignResult
	if len(values) != 1 || json.Unmarshal(values[0], &signed) != nil {
		return errors.New("invalid upstream request signature")
	}
	for key, value := range signed.Headers {
		req.Header.Set(key, value)
	}
	query := req.URL.Query()
	for key, value := range signed.Query {
		query.Set(key, value)
	}
	req.URL.RawQuery = query.Encode()
	if len(req.URL.String()) > 8192 {
		return errors.New("signed upstream URL exceeds limit")
	}
	if err = jsplugin.ValidateRequestHeaders(pluginResponseHeaders(req.Header)); err != nil {
		return err
	}
	return jsplugin.ValidateRequestURL(req.URL.String(), s.base, s.hosts)
}

func (s *newAPITaskSession) request(ctx context.Context, descriptor pluginTask.Request, method string, attempted *bool, streamContext map[string]any) (json.RawMessage, int, map[string]string, error) {
	if err := descriptor.Validate(s.base, s.hosts, method); err != nil {
		return nil, 0, nil, err
	}
	body := []byte(descriptor.Body)
	contentType := "application/json"
	if descriptor.BodyType == "multipart" {
		var err error
		body, contentType, err = pluginTask.EncodeMultipart(descriptor.Parts)
		if err != nil {
			return nil, 0, nil, err
		}
	} else if descriptor.BodyType != "" && descriptor.BodyType != "json" || len(descriptor.Parts) > 0 {
		return nil, 0, nil, errors.New("invalid task request body type")
	}
	if descriptor.ResponseType == "sse" && streamContext == nil {
		return nil, 0, nil, errors.New("streaming responses are only supported for submission")
	}
	if _, err := s.service.gateway.validateOutboundURL(descriptor.URL); err != nil {
		return nil, 0, nil, err
	}
	opCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(WithHTTPUpstreamRedirectsDisabled(opCtx), descriptor.Method, descriptor.URL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, nil, err
	}
	for key, value := range descriptor.Headers {
		req.Header.Set(key, value)
	}
	if descriptor.BodyType == "multipart" || req.Header.Get("Content-Type") == "" && len(body) > 0 {
		req.Header.Set("Content-Type", contentType)
	}
	if !descriptor.Credentialless {
		if err = s.sign(ctx, req, body); err != nil {
			return nil, 0, nil, err
		}
	}

	if descriptor.Credentialless {
		req.Header.Del("Authorization")
		req.Header.Del("X-Api-Key")
		req.Header.Del("Cookie")
		req = req.WithContext(WithHTTPUpstreamPublicHostsOnly(req.Context()))
	}
	if attempted != nil {
		*attempted = true
	}
	resp, err := s.service.gateway.httpUpstream.Do(req, s.proxy(), s.account.ID, s.account.Concurrency)
	if err != nil {
		return nil, 0, nil, errors.New("task upstream transport failed")
	}
	defer resp.Body.Close()
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType == "text/event-stream" {
		if descriptor.ResponseType != "sse" || resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, resp.StatusCode, nil, errors.New("unexpected task submit stream")
		}
		delta := slices.Contains(s.version.Meta.RequiredCapabilities, jsplugin.CapabilitySubmitSSEDelta) || s.has("parseSubmitEventDelta") && !s.has("parseSubmitEvent")
		hook := "parseSubmitEvent"
		if delta {
			hook = "parseSubmitEventDelta"
		}
		if !s.has(hook) {
			return nil, resp.StatusCode, nil, errors.New("plugin is missing its submit event hook")
		}
		body, err := pluginTask.ReadSubmitEvents(opCtx, resp.Body, delta, 30*time.Second, func(callCtx context.Context, event pluginTask.SubmitEvent, state json.RawMessage) (json.RawMessage, error) {
			return s.call(callCtx, hook, nil, streamContext, event, state)
		})
		return body, resp.StatusCode, pluginResponseHeaders(resp.Header), err
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, pluginTask.MaxSnapshotBytes+1))
	if err != nil {
		return nil, resp.StatusCode, nil, err
	}
	if len(body) > pluginTask.MaxSnapshotBytes {
		return nil, resp.StatusCode, nil, errors.New("task upstream response exceeds limit")
	}
	if !json.Valid(body) {
		return nil, resp.StatusCode, nil, errors.New("task upstream response is not JSON")
	}
	return body, resp.StatusCode, pluginResponseHeaders(resp.Header), nil
}

func (s *NewAPIPluginService) DecodeVideo(ctx context.Context, account *Account, versionID int64, model string, body json.RawMessage) (json.RawMessage, string, error) {
	var decoded struct {
		Kind          string          `json:"kind"`
		Model         string          `json:"model"`
		Action        string          `json:"action"`
		RequestBody   json.RawMessage `json:"requestBody"`
		OriginTaskIDs []string        `json:"originTaskIds"`
	}
	err := s.withTaskSession(ctx, account, versionID, account.GetCredential(newAPIPluginConfigEncryptedKey), func(session *newAPITaskSession) error {
		if !session.version.Enabled {
			return errors.New("video plugin is disabled")
		}
		claimed := false
		for _, protocol := range session.version.Meta.Protocols {
			if protocol.Name == "openai_video" {
				claimed = true
			}
		}
		if !claimed {
			return errors.New("plugin does not declare OpenAI Videos support")
		}
		if !slices.ContainsFunc(pluginVideoModels(session.version.Meta), func(candidate string) bool {
			return pluginVideoModelMatches(candidate, account.GetMappedModel(model)) || pluginVideoModelMatches(candidate, model)
		}) {
			return errors.New("plugin does not declare this video model")
		}
		raw, err := session.call(ctx, "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"protocol": "openai_video", "operation": "create", "model": model, "upstreamModel": account.GetMappedModel(model), "body": map[string]any{"kind": "json", "value": body}})
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &decoded); err != nil {
			return err
		}
		if decoded.Kind != "submit" || decoded.Action == "" || len(decoded.RequestBody) == 0 {
			return errors.New("plugin did not produce a valid task submission")
		}
		if len(decoded.OriginTaskIDs) > 0 {
			return errors.New("derived task references require the origin-task host service")
		}
		if decoded.Model != "" && decoded.Model != model && decoded.Model != account.GetMappedModel(model) {
			return errors.New("plugin changed the accepted model identity")
		}
		return nil
	})
	return decoded.RequestBody, decoded.Action, err
}

func (s *NewAPIPluginService) EstimateTask(ctx context.Context, account *Account, t *GenerationTask) (json.RawMessage, error) {
	facts := json.RawMessage(`{}`)
	err := s.withGenerationSession(ctx, account, t, func(session *newAPITaskSession) error {
		if !session.has("extractUsage") {
			return nil
		}
		var err error
		facts, err = session.call(ctx, "extractUsage", nil, session.context(t))
		return err
	})
	return nonemptyPluginJSON(facts), err
}

func generationTaskView(t *GenerationTask) map[string]any {
	status := map[string]string{GenerationQueued: "QUEUED", GenerationSubmitting: "SUBMITTED", GenerationRunning: "IN_PROGRESS", GenerationUncertain: "UNKNOWN", GenerationSucceeded: "SUCCESS", GenerationFailed: "FAILURE"}[t.Status]
	return map[string]any{"task_id": t.ID, "status": status, "progress": fmt.Sprintf("%d%%", t.Progress), "action": t.Action, "state": t.State, "data": t.Data, "model": t.Model, "upstreamModel": t.UpstreamModel, "created_at": t.CreatedAt.Unix(), "updated_at": t.UpdatedAt.Unix(), "fail_reason": t.ErrorMessage, "properties": map[string]any{"origin_model_name": t.Model}}
}

// RenderVideo lets the driver add protocol fields, then overlays immutable host
// identity/lifecycle fields so a script cannot expose an upstream task identity.
func (s *NewAPIPluginService) RenderVideo(ctx context.Context, account *Account, t *GenerationTask) (map[string]any, error) {
	output := map[string]any{}
	err := s.withGenerationSession(ctx, account, t, func(session *newAPITaskSession) error {
		raw, err := session.call(ctx, "protocols", []string{"openai_video", "render"}, map[string]any{"protocol": "openai_video", "operation": "retrieve"}, generationTaskView(t))
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, &output)
	})
	if err != nil {
		return nil, err
	}
	view := t.VideoView()
	for _, field := range []string{"seconds", "size"} {
		if value, ok := output[field].(string); ok && len(value) <= 32 && value != "" {
			view[field] = value
		}
	}
	// A charged moderation block is a completed task without a delivered
	// video; preserve its bounded public notice, never arbitrary plugin data.
	if t.Status == GenerationSucceeded && t.DeliveryStatus != GenerationDeliveryReady {
		if notice, ok := output["error"].(map[string]any); ok {
			code, _ := notice["code"].(string)
			message, _ := notice["message"].(string)
			if code != "" && len(code) <= 128 && len(message) <= 4096 {
				view["error"] = map[string]any{"code": code, "message": message}
			}
		}
	}
	return view, nil
}

// OpenTaskContent builds the artifact request with the pinned driver. The host
// forwards only range/cache validators and never returns upstream credentials.
func (s *NewAPIPluginService) OpenTaskContent(ctx context.Context, account *Account, t *GenerationTask, method, artifact string, headers http.Header) (*http.Response, error) {
	var response *http.Response
	err := s.withGenerationSession(ctx, account, t, func(session *newAPITaskSession) error {
		if err := session.exchange(ctx); err != nil {
			return err
		}
		value := session.context(t)
		value["data"] = t.Data
		value["taskId"] = t.ID
		value["upstreamTaskId"] = t.UpstreamID
		value["artifactKey"] = artifact
		clientHeaders := map[string]string{}
		for _, name := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
			if header := headers.Get(name); header != "" {
				if len(header) > 4096 {
					return errors.New("content header exceeds limit")
				}
				clientHeaders[name] = header
			}
		}
		value["clientRequest"] = map[string]any{"method": method, "headers": clientHeaders}
		raw, err := session.call(ctx, "buildContentRequest", nil, value)
		if err != nil {
			return err
		}
		descriptor, err := pluginTask.Decode[pluginTask.Request](raw)
		if err != nil {
			return err
		}
		if err = descriptor.Validate(session.base, session.hosts, method); err != nil {
			return err
		}
		if descriptor.Method != http.MethodGet && descriptor.Method != http.MethodHead {
			return errors.New("invalid artifact download method")
		}
		if len(descriptor.ContentPath) > 0 {
			return errors.New("embedded binary artifacts require the file host service")
		}
		if _, err = s.gateway.validateOutboundURL(descriptor.URL); err != nil {
			return err
		}
		requestCtx := WithHTTPUpstreamRedirectsDisabled(ctx)
		if descriptor.Credentialless {
			requestCtx = WithHTTPUpstreamPublicHostsOnly(requestCtx)
		}
		req, err := http.NewRequestWithContext(requestCtx, method, descriptor.URL, nil)
		if err != nil {
			return err
		}
		if !descriptor.Credentialless {
			for name, value := range descriptor.Headers {
				req.Header.Set(name, value)
			}
		}
		for name, value := range clientHeaders {
			req.Header.Set(name, value)
		}
		if !descriptor.Credentialless {
			if err = session.sign(ctx, req, nil); err != nil {
				return err
			}
		}
		response, err = s.gateway.httpUpstream.Do(req, session.proxy(), account.ID, account.Concurrency)
		// Storage redirects must be followed by the host with public-address
		// checks and without upstream credentials, cookies or signature headers.
		for hop := 0; err == nil && response != nil && response.StatusCode >= 300 && response.StatusCode < 400 && response.StatusCode != http.StatusNotModified; hop++ {
			location := response.Header.Get("Location")
			response.Body.Close()
			if hop >= 3 || location == "" {
				return errors.New("invalid artifact redirect")
			}
			target, parseErr := req.URL.Parse(location)
			if parseErr != nil || target.Scheme != "https" || target.User != nil || target.Fragment != "" || len(target.String()) > 8192 {
				return errors.New("invalid artifact redirect destination")
			}
			if _, err = s.gateway.validateOutboundURL(target.String()); err != nil {
				return err
			}
			redirectCtx := WithHTTPUpstreamPublicHostsOnly(WithHTTPUpstreamRedirectsDisabled(ctx))
			req, err = http.NewRequestWithContext(redirectCtx, method, target.String(), nil)
			if err != nil {
				return err
			}
			for name, value := range clientHeaders {
				req.Header.Set(name, value)
			}
			response, err = s.gateway.httpUpstream.Do(req, session.proxy(), account.ID, account.Concurrency)
		}
		return err
	})
	return response, err
}

func (s *NewAPIPluginService) SubmitTask(ctx context.Context, account *Account, t *GenerationTask) error {
	attempted := false
	err := s.withGenerationSession(ctx, account, t, func(session *newAPITaskSession) error {
		if err := session.exchange(ctx); err != nil {
			return err
		}
		value := session.context(t)
		raw, err := session.call(ctx, "buildSubmitRequest", nil, value)
		if err != nil {
			return err
		}
		descriptor, err := pluginTask.Decode[pluginTask.Request](raw)
		if err != nil {
			return err
		}
		if err = descriptor.Validate(session.base, session.hosts, http.MethodPost); err != nil {
			return err
		}
		body, status, headers, err := session.request(ctx, descriptor, http.MethodPost, &attempted, value)
		if status != 0 && (status < 200 || status >= 300) {
			if status >= 400 && status < 500 && status != 408 && status != 409 {
				attempted = false
			}
			return fmt.Errorf("task submission returned HTTP %d; requires reconciliation", status)
		}
		if err != nil {
			return err
		}
		submitHeaders := make(map[string][]string, len(headers))
		for name, value := range headers {
			submitHeaders[name] = []string{value}
		}
		raw, err = session.call(ctx, "parseSubmitResponse", nil, value, map[string]any{"statusCode": status, "headers": submitHeaders, "body": body})
		if err != nil {
			return err
		}
		submitted, err := pluginTask.Decode[pluginTask.Submitted](raw)
		if err != nil {
			return err
		}
		if err = submitted.Validate(); err != nil {
			return err
		}
		t.UpstreamID = submitted.TaskID
		t.State = nonemptyPluginJSON(submitted.State)
		t.Data = nonemptyPluginJSON(submitted.TaskData)
		t.Status = GenerationRunning
		if submitted.Immediate != nil {
			applyGenerationResult(t, *submitted.Immediate)
			return session.complete(ctx, t, *submitted.Immediate, nonemptyPluginJSON(submitted.TaskData))
		}
		return nil
	})
	if err != nil {
		return &GenerationSubmissionError{Uncertain: attempted, Cause: err}
	}
	return nil
}

func (s *NewAPIPluginService) PollTask(ctx context.Context, account *Account, t *GenerationTask) error {
	next := *t
	if err := s.pollTask(ctx, account, &next); err != nil {
		return err
	}
	*t = next
	return nil
}

func (s *NewAPIPluginService) pollTask(ctx context.Context, account *Account, t *GenerationTask) error {
	return s.withGenerationSession(ctx, account, t, func(session *newAPITaskSession) error {
		if err := session.exchange(ctx); err != nil {
			return err
		}
		value := session.context(t)
		raw, err := session.call(ctx, "buildQueryRequest", nil, value)
		if err != nil {
			return err
		}
		descriptor, err := pluginTask.Decode[pluginTask.Request](raw)
		if err != nil {
			return err
		}
		body, status, headers, err := session.request(ctx, descriptor, http.MethodGet, nil, nil)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("task poll returned HTTP %d", status)
		}
		raw, err = session.call(ctx, "parseTaskResult", nil, value, body, map[string]any{"status": status, "headers": headers})
		if err != nil {
			return err
		}
		result, err := pluginTask.Decode[pluginTask.Result](raw)
		if err != nil {
			return err
		}
		if err = result.Validate(); err != nil {
			return err
		}
		t.Data = body
		applyGenerationResult(t, result)
		return session.complete(ctx, t, result, body)
	})
}
func (s *newAPITaskSession) complete(ctx context.Context, t *GenerationTask, result pluginTask.Result, body json.RawMessage) error {
	if t.Status != GenerationSucceeded && t.Status != GenerationFailed {
		return nil
	}
	if s.has("extractUsageOnComplete") && (t.Status == GenerationSucceeded || t.Status == GenerationFailed) {
		view := map[string]any{"action": t.Action, "state": t.State, "data": t.Data, "model": t.Model, "upstreamModel": t.UpstreamModel, "task_id": t.ID}
		resultView := map[string]any{"status": result.Status, "plugin_state": result.State, "completionTokens": result.CompletionTokens, "totalTokens": result.TotalTokens}
		usage, err := s.call(ctx, "extractUsageOnComplete", nil, view, resultView, body)
		if err != nil {
			return err
		}
		t.Usage = mergeGenerationFacts(t.Usage, nonemptyPluginJSON(usage))
		// Keep the final observation separate from the admission estimate.
		// Missing final usage must not silently become an observed charge.
		var facts map[string]json.RawMessage
		if json.Unmarshal(t.Usage, &facts) != nil {
			return errors.New("invalid final generation facts")
		}
		facts["_host_final_usage"] = nonemptyPluginJSON(usage)
		encoded, err := json.Marshal(facts)
		if err != nil {
			return err
		}
		t.Usage = encoded
	}
	if t.Status == GenerationSucceeded && s.has("listArtifacts") {
		artifacts, err := s.call(ctx, "listArtifacts", nil, map[string]any{"data": body, "status": "SUCCESS", "task_id": t.ID, "state": t.State})
		if err != nil {
			return err
		}
		items, err := pluginTask.DecodeArtifacts(artifacts)
		if err != nil {
			return err
		}
		t.DeliveryStatus = GenerationDeliveryPending
		for _, item := range items {
			if item.Type == "video" && item.Key == "video" {
				t.DeliveryStatus = GenerationDeliveryReady
			}
		}
	}
	return nil
}

func nonemptyPluginJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`{}`)
	}
	return raw
}
func applyGenerationResult(t *GenerationTask, r pluginTask.Result) {
	t.Progress = r.ProgressPercent()
	if len(r.State) > 0 {
		t.State = r.State
	}
	switch r.Status {
	case "SUCCESS":
		t.Status = GenerationSucceeded
		t.Progress = 100
	case "FAILURE":
		t.Status = GenerationFailed
		t.ErrorCode = "video_generation_failed"
		t.ErrorMessage = "The upstream video generation failed."
	case "UNKNOWN":
		t.BillingStatus = GenerationBillingReview
		t.ErrorCode = "upstream_status_unknown"
		t.ErrorMessage = "The upstream task status requires reconciliation."
	default:
		t.Status = GenerationRunning
	}
	if (t.Status == GenerationSucceeded || t.Status == GenerationFailed) && t.FinishedAt == nil {
		now := time.Now().UTC()
		t.FinishedAt = &now
		t.BillingStatus = GenerationBillingPending
	}
}
