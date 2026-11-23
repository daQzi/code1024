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

	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/tidwall/gjson"
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
	task    *GenerationTask
}

func (s *newAPITaskSession) openFile(ctx context.Context, ref string) (pluginTask.File, io.ReadCloser, error) {
	if s.task == nil || s.service.files == nil {
		return pluginTask.File{}, nil, ErrGenerationFileNotFound
	}
	for _, file := range s.task.Files {
		if file.Ref != ref {
			continue
		}
		stored, err := s.service.files.GetGenerationFile(ctx, file.ID, s.task.UserID, s.task.APIKeyID, s.task.GroupID)
		if err != nil {
			return pluginTask.File{}, nil, err
		}
		if stored.Size != file.Size || stored.SHA256 != file.SHA256 || stored.MimeType != file.MimeType {
			return pluginTask.File{}, nil, errors.New("task file no longer matches its snapshot")
		}
		reader, err := s.service.files.OpenGenerationFile(ctx, file.ID, s.task.UserID, s.task.APIKeyID, s.task.GroupID)
		return file.File, reader, err
	}
	return pluginTask.File{}, nil, ErrGenerationFileNotFound
}

func (s *NewAPIPluginService) withGenerationSession(ctx context.Context, account *Account, task *GenerationTask, fn func(*newAPITaskSession) error) error {
	copy := *account
	copy.Credentials = maps.Clone(account.Credentials)
	if task.Connection.BaseURL != "" {
		copy.Credentials["base_url"] = task.Connection.BaseURL
		copy.Credentials["newapi_upstream_kind"] = task.Connection.Kind
	}
	return s.withTaskSession(ctx, &copy, task.PluginVersionID, task.ConfigEncrypted, func(session *newAPITaskSession) error { session.task = task; return fn(session) })
}

func (s *NewAPIPluginService) withAuthorizedGenerationSession(ctx context.Context, account *Account, task *GenerationTask, fn func(*newAPITaskSession) error) error {
	resolved, err := s.prepareTaskCredential(ctx, account, task, false)
	if err != nil {
		return err
	}
	return s.withGenerationSession(ctx, resolved, task, fn)
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
	stop := context.AfterFunc(opCtx, func() { _ = resp.Body.Close() })
	defer stop()
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
	files := make([]pluginTask.File, 0, len(t.Files))
	for _, file := range t.Files {
		files = append(files, file.File)
	}
	value := map[string]any{"baseUrl": s.base, "apiKey": s.account.GetCredential("api_key"), "config": s.config, "files": files,
		"model": t.Model, "upstreamModel": t.UpstreamModel, "originModel": t.Model, "action": t.Action, "taskId": t.UpstreamID, "publicTaskId": t.ID,
		"requestBody": t.Request, "state": t.State, "data": t.Data, "taskData": t.Data, "usagePurpose": "billing_facts", "channelType": 61, "token": s.token, "upstream": map[string]any{"kind": kind}}
	if len(t.Origins) > 0 {
		value["originTaskId"] = t.Origins[0].UpstreamTaskID
		value["originTasks"] = t.Origins
	}
	s.taskCredentialContext(value)
	return value
}

func (s *newAPITaskSession) credential() jsplugin.CredentialContext {
	value := providerCredentialContext(s.account, s.base, s.config, s.token)
	if s.version.Meta.Auth.Type == "oauth2_jwt" && s.account.GetCredential("newapi_upstream_kind") != "new_api" && s.account.newAPIProviderCredential == "" {
		value.APIKey = ""
	}
	return value
}
func (s *newAPITaskSession) exchange(ctx context.Context) error {
	if s.account.newAPIProviderCredential != "" || !s.has("exchangeKey") {
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
	digest := sha256.Sum256(body)
	return s.signDigest(ctx, req, hex.EncodeToString(digest[:]))
}

func (s *newAPITaskSession) signDigest(ctx context.Context, req *http.Request, digest string) error {
	if applyProviderLoginAuth(req, s.account) {
		if err := jsplugin.ValidateRequestHeaders(pluginResponseHeaders(req.Header)); err != nil {
			return err
		}
		if err := jsplugin.ValidateRequestURL(req.URL.String(), s.base, s.hosts); err != nil {
			return err
		}
	}
	if !s.has("signRequest") {
		return nil
	}
	values, err := s.client.Typed(ctx, s.fetch, "CallSignRequest", s.credential(), req.Method, req.URL.String(), pluginResponseHeaders(req.Header), digest)
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
	body, err := descriptor.ResolveBody(ctx, s.openFile)
	if err != nil {
		return nil, 0, nil, err
	}
	var source io.Reader = bytes.NewReader(body)
	digest := sha256.Sum256(body)
	bodyDigest := hex.EncodeToString(digest[:])
	length := int64(len(body))
	contentType := "application/json"
	if descriptor.BodyType == "multipart" {
		spool, createErr := newGenerationSpool()
		if createErr != nil {
			return nil, 0, nil, createErr
		}
		defer spool.Close()
		hasher := sha256.New()
		contentType, err = pluginTask.WriteMultipart(ctx, io.MultiWriter(spool, hasher), descriptor.Parts, s.openFile)
		if err != nil {
			return nil, 0, nil, err
		}
		length, err = spool.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, 0, nil, err
		}
		if _, err = spool.Seek(0, io.SeekStart); err != nil {
			return nil, 0, nil, err
		}
		bodyDigest = hex.EncodeToString(hasher.Sum(nil))
		source = spool
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
	req, err := http.NewRequestWithContext(WithHTTPUpstreamRedirectsDisabled(opCtx), descriptor.Method, descriptor.URL, source)
	if err != nil {
		return nil, 0, nil, err
	}
	req.ContentLength = length
	for key, value := range descriptor.Headers {
		req.Header.Set(key, value)
	}
	if descriptor.BodyType == "multipart" || req.Header.Get("Content-Type") == "" && len(body) > 0 {
		req.Header.Set("Content-Type", contentType)
	}
	if !descriptor.Credentialless {
		if err = s.signDigest(ctx, req, bodyDigest); err != nil {
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
	return s.DecodeVideoProtocol(ctx, account, versionID, model, body, GenerationProtocolOpenAI)
}

func (s *NewAPIPluginService) DecodeVideoProtocol(ctx context.Context, account *Account, versionID int64, model string, body json.RawMessage, protocol string) (json.RawMessage, string, error) {
	intent, err := s.decodeVideoIntent(ctx, versionID, model, account.GetMappedModel(model), body, protocol)
	if err != nil {
		return nil, "", err
	}
	if err = validateGenerationVideoIntent(ctx, account, model, protocol, intent); err != nil {
		return nil, "", err
	}
	return intent.RequestBody, intent.Action, nil
}

type generationVideoIntent struct {
	Kind          string          `json:"kind"`
	Model         string          `json:"model"`
	Action        string          `json:"action"`
	RequestBody   json.RawMessage `json:"requestBody"`
	OriginTaskIDs json.RawMessage `json:"originTaskIds"`
	Renderer      json.RawMessage `json:"renderer"`
	origins       []string
}

// Protocol decoding runs before account selection, without configuration,
// credentials or network access, matching the baseline route decoder contract.
func (s *NewAPIPluginService) decodeVideoIntent(ctx context.Context, versionID int64, model, upstreamModel string, body json.RawMessage, protocol string) (*generationVideoIntent, error) {
	ingress, ok := generationVideoIngress(protocol)
	if !ok {
		return nil, errors.New("unsupported video protocol")
	}
	version, err := s.repo.Get(ctx, versionID)
	if err != nil {
		return nil, err
	}
	if version == nil || !version.Enabled || !version.Capabilities.Task {
		return nil, errors.New("video plugin is disabled or is not a task driver")
	}
	remix, isRemix := ctx.Value(generationRemixKey{}).(generationRemix)
	if isRemix && (protocol != GenerationProtocolOpenAI || !generationVideoRemixVersion(version) || model != remix.model) {
		return nil, ErrGenerationRemixUnsupported
	}
	if !slices.ContainsFunc(taskProtocolModels(version, protocol), func(candidate string) bool {
		return pluginVideoModelMatches(candidate, upstreamModel) || pluginVideoModelMatches(candidate, model)
	}) {
		return nil, errors.New("plugin does not declare this video model")
	}
	hook, path := "protocols", []string{"openai_video", "decodeRequest"}
	wireProtocol := protocol
	method := http.MethodPost
	operation := "create"
	ingressPath := generationIngressPath(ctx, ingress.Path)
	params := map[string]string{}
	useDirectFallback := false
	switch {
	case protocol == GenerationProtocolSunoTask:
		wireProtocol, path = "openai_responses", []string{"openai_responses", "decodeRequest"}
	case protocol == GenerationProtocolOpenAI:
	case protocol == GenerationProtocolOpenAIResponses:
		wireProtocol, path = "openai_responses", []string{"openai_responses", "decodeRequest"}
	case protocol == GenerationProtocolOpenAIImage:
		wireProtocol, path = "openai_image", []string{"openai_image", "decodeRequest"}
	case strings.HasPrefix(protocol, GenerationProtocolNativePrefix):
		match, matched := generationRouteMatchFromContext(ctx)
		if !matched || match.VersionID != versionID || match.Route.Decode == "" || version.Meta.Key != strings.TrimPrefix(protocol, GenerationProtocolNativePrefix) {
			return nil, errors.New("plugin does not declare this native route")
		}
		if len(match.Route.Models) > 0 && !nativeVideoRouteModel(match.Route.Models, model, upstreamModel) {
			return nil, errors.New("plugin native route does not declare this model")
		}
		hook, path = "native", []string{match.Route.Decode}
		method = match.Route.Method
		operation = string(match.Route.Type)
		wireProtocol = ""
		if match.Path != "" {
			ingressPath = match.Path
		}
		if match.Params != nil {
			params = match.Params
		}
	case protocol == GenerationProtocolGenericTask:
		switch {
		case slices.ContainsFunc(version.Meta.Protocols, func(p jsplugin.ProtocolClaim) bool { return p.Name == "openai_responses" }):
			wireProtocol, path = "openai_responses", []string{"openai_responses", "decodeRequest"}
		case slices.ContainsFunc(version.Meta.Protocols, func(p jsplugin.ProtocolClaim) bool { return p.Name == "openai_video" }):
			wireProtocol, path = "openai_video", []string{"openai_video", "decodeRequest"}
		case slices.ContainsFunc(version.Meta.Protocols, func(p jsplugin.ProtocolClaim) bool { return p.Name == "openai_image" }):
			wireProtocol, path = "openai_image", []string{"openai_image", "decodeRequest"}
		default:
			var submitRoute jsplugin.Route
			for _, candidate := range version.Meta.Routes {
				if candidate.Type == "submit" && candidate.Decode != "" && (len(candidate.Models) == 0 || nativeVideoRouteModel(candidate.Models, model, upstreamModel)) {
					submitRoute = candidate
					break
				}
			}
			if submitRoute.Decode != "" {
				hook, path = "native", []string{submitRoute.Decode}
				wireProtocol = ""
				ingressPath = submitRoute.Path
			} else {
				useDirectFallback = true
			}
		}
	default:
		route, found := pluginNativeVideoRoute(version.Meta, protocol)
		if !found || !nativeVideoRouteModel(route.Models, model, upstreamModel) {
			return nil, errors.New("plugin does not declare this native video operation")
		}
		hook, path = "native", []string{route.Decode}
	}
	files, _ := ctx.Value(generationDecodeFilesKey{}).([]GenerationFileSnapshot)
	if protocol == GenerationProtocolSunoTask {
		if len(files) != 0 || GenerationTaskSubmitPlugin(ctx) != "sunoapi" || (upstreamModel != "suno_music" && upstreamModel != "suno_lyrics") || !validSunoTaskBody(body) {
			return nil, ErrGenerationDecode
		}
	}
	if useDirectFallback {
		if _, err := GenerationTaskSubmitModel(body); err != nil {
			return nil, ErrGenerationDecode
		}
		action := strings.TrimSpace(gjson.GetBytes(body, "action").Str)
		if action == "" {
			action = "generate"
		}
		if len(action) > 128 {
			return nil, ErrGenerationDecode
		}
		var rawOrigins json.RawMessage
		if originRes := gjson.GetBytes(body, "originTaskIds"); originRes.Exists() {
			rawOrigins = json.RawMessage(originRes.Raw)
		}
		origins, originErr := pluginTask.OriginTaskIDs(rawOrigins)
		if originErr != nil {
			return nil, originErr
		}
		return &generationVideoIntent{
			Kind:          "submit",
			Model:         model,
			Action:        action,
			RequestBody:   bytes.Clone(body),
			OriginTaskIDs: rawOrigins,
			origins:       origins,
		}, nil
	}
	wireBody, err := generationFileDecodeBody(body, files)
	if err != nil {
		return nil, err
	}
	if protocol == GenerationProtocolOpenAIImage {
		operation = "generate"
		if strings.HasSuffix(ingressPath, "/edits") {
			operation = "edit"
		}
	}
	var intent generationVideoIntent
	err = s.pool.With(ctx, version.Source, func(client *runner.Client) error {
		raw, err := client.Call(ctx, hook, path, map[string]any{"protocol": wireProtocol, "operation": operation, "method": method, "path": ingressPath, "params": params, "model": model, "upstreamModel": upstreamModel, "body": wireBody})
		if err != nil {
			return err
		}
		intent, err = pluginTask.Decode[generationVideoIntent](raw)
		return err
	})
	if err != nil {
		return nil, err
	}
	if intent.Kind != "submit" || intent.Action == "" || len(intent.Action) > 128 || len(intent.RequestBody) == 0 {
		return nil, errors.New("plugin did not produce a valid task submission")
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(intent.RequestBody, &object) != nil || object == nil {
		return nil, errors.New("plugin task request must be a JSON object")
	}
	intent.origins, err = pluginTask.OriginTaskIDs(intent.OriginTaskIDs)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(protocol, GenerationProtocolNativePrefix) {
		source := nativeAliasTaskID(ingressPath)
		if source != "" && !slices.Contains(intent.origins, source) || method == http.MethodDelete && len(intent.origins) == 0 {
			return nil, ErrGenerationOrigin
		}
	}
	if isRemix {
		if len(intent.origins) != 0 || len(object) != 2 || object["prompt"] == nil || object["model"] == nil || len(files) != 0 {
			return nil, ErrGenerationOrigin
		}
		// As in New API's ResolveOriginTask, remix is a host operation applied
		// around the ordinary video decoder. The shipped Sora driver explicitly
		// switches its submit URL on this action.
		intent.Action, intent.origins = "remix", []string{remix.origin}
	}
	if len(intent.Renderer) > 0 {
		return nil, errors.New("task decoders cannot change the host renderer")
	}
	if protocol == GenerationProtocolSunoTask && (len(intent.origins) != 0 || upstreamModel == "suno_music" && intent.Action != "MUSIC" || upstreamModel == "suno_lyrics" && intent.Action != "LYRICS") {
		return nil, ErrGenerationDecode
	}
	if intent.Model != "" && intent.Model != model && intent.Model != upstreamModel {
		return nil, errors.New("plugin changed the accepted model identity")
	}
	return &intent, nil
}

func (s *NewAPIPluginService) EstimateTask(ctx context.Context, account *Account, t *GenerationTask) (json.RawMessage, error) {
	facts := json.RawMessage(`{}`)
	err := s.withAuthorizedGenerationSession(ctx, account, t, func(session *newAPITaskSession) error {
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
	status := generation.PublicTaskStatus(t.Status)
	var finishedAt int64
	if t.FinishedAt != nil {
		finishedAt = t.FinishedAt.Unix()
	}
	return map[string]any{"task_id": t.ID, "platform": t.Platform, "status": status, "progress": fmt.Sprintf("%d%%", t.Progress), "action": t.Action, "state": t.State, "data": t.Data, "model": t.Model, "upstreamModel": t.UpstreamModel, "created_at": t.CreatedAt.Unix(), "updated_at": t.UpdatedAt.Unix(), "finished_at": finishedAt, "fail_reason": t.ErrorMessage, "properties": map[string]any{"origin_model_name": t.Model}}
}

func generationTaskPublicRenderView(t *GenerationTask) map[string]any {
	view := generationTaskView(t)
	view["fail_reason"] = t.PublicTaskReadView().FailReason
	return view
}

// CallTaskRenderHook invokes a pinned task plugin's protocol or native render
// hook using the task's immutable version and connection snapshot.
func (s *NewAPIPluginService) CallTaskRenderHook(ctx context.Context, account *Account, t *GenerationTask, hook string, path []string, args ...any) (json.RawMessage, error) {
	var out json.RawMessage
	err := s.withGenerationSession(ctx, account, t, func(session *newAPITaskSession) error {
		var err error
		out, err = session.call(ctx, hook, path, args...)
		return err
	})
	return out, err
}

// CallVersionHook invokes an offline hook (such as a native decode/render hook)
// on an enabled pinned plugin version without account secrets or network access.
func (s *NewAPIPluginService) CallVersionHook(ctx context.Context, versionID int64, hook string, path []string, args ...any) (json.RawMessage, error) {
	return s.callVersionHook(ctx, versionID, true, hook, path, args...)
}

// CallPinnedVersionHook invokes an offline read-only hook on a historical task's
// pinned plugin version even after the plugin version is disabled or unbound.
func (s *NewAPIPluginService) CallPinnedVersionHook(ctx context.Context, versionID int64, hook string, path []string, args ...any) (json.RawMessage, error) {
	return s.callVersionHook(ctx, versionID, false, hook, path, args...)
}

// ListPinnedTaskArtifacts projects saved artifacts from a historical task's
// pinned plugin version without requiring the original account or plugin version
// to remain active or bound.
func (s *NewAPIPluginService) ListPinnedTaskArtifacts(ctx context.Context, t *GenerationTask) ([]pluginTask.Artifact, error) {
	if s == nil || s.repo == nil || s.pool == nil || t == nil {
		return nil, errors.New("plugin service is unavailable")
	}
	if t.Status != GenerationSucceeded {
		return []pluginTask.Artifact{}, nil
	}
	version, err := s.repo.Get(ctx, t.PluginVersionID)
	if err != nil {
		return nil, err
	}
	if version == nil || !version.Capabilities.Task {
		return nil, errors.New("plugin is not a task driver")
	}
	var out []pluginTask.Artifact
	err = s.pool.With(ctx, version.Source, func(client *runner.Client) error {
		if !slices.Contains(client.Descriptor.Hooks, "listArtifacts") {
			out = []pluginTask.Artifact{}
			return nil
		}
		raw, callErr := client.Call(ctx, "listArtifacts", nil, generation.TaskArtifactInput(t))
		if callErr != nil {
			return callErr
		}
		out, callErr = pluginTask.DecodeArtifacts(raw)
		return callErr
	})
	return out, err
}

func (s *NewAPIPluginService) callVersionHook(ctx context.Context, versionID int64, requireEnabled bool, hook string, path []string, args ...any) (json.RawMessage, error) {
	if s == nil || s.repo == nil || s.pool == nil {
		return nil, errors.New("plugin service is unavailable")
	}
	version, err := s.repo.Get(ctx, versionID)
	if err != nil {
		return nil, err
	}
	if version == nil || !version.Capabilities.Task || (requireEnabled && !version.Enabled) {
		return nil, errors.New("task plugin version is disabled or unavailable")
	}
	var out json.RawMessage
	err = s.pool.With(ctx, version.Source, func(client *runner.Client) error {
		var err error
		out, err = client.Call(ctx, hook, path, args...)
		return err
	})
	return out, err
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
	return s.openTaskContent(ctx, account, t, method, artifact, headers, false)
}

func (s *NewAPIPluginService) openTaskContent(ctx context.Context, account *Account, t *GenerationTask, method, artifact string, headers http.Header, declaredOnly bool) (*http.Response, error) {
	if declaredOnly {
		if err := s.requireTaskArtifact(ctx, account, t, artifact); err != nil {
			return nil, err
		}
	}
	var response *http.Response
	var embedded *pluginTask.Request
	err := s.withAuthorizedGenerationSession(ctx, account, t, func(session *newAPITaskSession) error {
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
		if err = descriptor.Validate(session.base, session.hosts, http.MethodGet); err != nil {
			return err
		}
		if err = descriptor.ValidateContent(); err != nil {
			return err
		}
		if len(descriptor.ContentPath) > 0 {
			if descriptor.Method != http.MethodGet && descriptor.Method != http.MethodPost || descriptor.ResponseType != "json" || len(descriptor.Parts) != 0 || descriptor.BodyType != "" && descriptor.BodyType != "json" {
				return errors.New("embedded artifacts require a GET or POST JSON request")
			}
			embedded = &descriptor
		} else if descriptor.Method != http.MethodGet && descriptor.Method != http.MethodHead {
			return errors.New("invalid artifact download method")
		}
		if _, err = s.gateway.validateOutboundURL(descriptor.URL); err != nil {
			return err
		}
		requestCtx := WithHTTPUpstreamRedirectsDisabled(ctx)
		if descriptor.Credentialless {
			requestCtx = WithHTTPUpstreamPublicHostsOnly(requestCtx)
		}
		upstreamMethod := method
		var requestBody []byte
		if embedded != nil {
			upstreamMethod = descriptor.Method
			requestBody, err = descriptor.WireBody(ctx)
			if err != nil {
				return err
			}
			clientHeaders = nil // Validators address the media, not its JSON envelope.
		}
		req, err := http.NewRequestWithContext(requestCtx, upstreamMethod, descriptor.URL, bytes.NewReader(requestBody))
		if err != nil {
			return err
		}
		if !descriptor.Credentialless {
			for name, value := range descriptor.Headers {
				req.Header.Set(name, value)
			}
		}
		if len(requestBody) > 0 && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
		for name, value := range clientHeaders {
			req.Header.Set(name, value)
		}
		if !descriptor.Credentialless {
			if err = session.sign(ctx, req, requestBody); err != nil {
				return err
			}
		}
		response, err = s.gateway.httpUpstream.Do(req, session.proxy(), account.ID, account.Concurrency)
		// Storage redirects must be followed by the host with public-address
		// checks and without upstream credentials, cookies or signature headers.
		for hop := 0; err == nil && response != nil && response.StatusCode >= 300 && response.StatusCode < 400 && response.StatusCode != http.StatusNotModified; hop++ {
			location := response.Header.Get("Location")
			response.Body.Close()
			if embedded != nil {
				return errors.New("embedded artifact redirects are not supported")
			}
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
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, err
	}
	if response == nil || response.Body == nil {
		return nil, errors.New("artifact upstream returned no body")
	}
	if embedded != nil {
		// Release the JS worker before reading the potentially large envelope
		// or waiting for the downstream client to consume the decoded media.
		return generationEmbeddedResponse(ctx, response, embedded, method)
	}
	return response, err
}

func (s *NewAPIPluginService) SubmitTask(ctx context.Context, account *Account, t *GenerationTask) error {
	attempted := false
	var descriptor pluginTask.Request
	var token json.RawMessage
	var authorized *Account
	err := s.withAuthorizedGenerationSession(ctx, account, t, func(session *newAPITaskSession) error {
		authorized = session.account
		if err := session.exchange(ctx); err != nil {
			return err
		}
		var err error
		descriptor, err = session.scriptDriver().BuildRequest(ctx, t, true)
		if err != nil {
			return err
		}
		if err = descriptor.Validate(session.base, session.hosts, http.MethodPost); err != nil {
			return err
		}
		if t.PreparationStatus != "" {
			if !officialFiveUploadVersion(session.version) {
				return errors.New("prepared upload is not supported by this plugin revision")
			}
			descriptor.Body, err = s.resolveTaskPreparedBody(session.account, t, descriptor.Body)
			if err != nil {
				return err
			}
		}
		token = session.token
		if t.RequestPluginsEncrypted == "" {
			return session.submit(ctx, t, descriptor, &attempted)
		}
		return nil
	})
	if err != nil {
		return &GenerationSubmissionError{Uncertain: attempted, Cause: err}
	}
	if t.RequestPluginsEncrypted == "" {
		return nil
	}
	// Release the Task worker before acquiring a Transform worker. Four
	// concurrent submits must not wait for one another's occupied lanes.
	if err = s.taskRequestPluginEgressDescriptor(ctx, t, descriptor); err != nil {
		return &GenerationSubmissionError{Uncertain: false, Cause: err}
	}
	err = s.withGenerationSession(ctx, authorized, t, func(session *newAPITaskSession) error {
		session.token = token
		return session.submit(ctx, t, descriptor, &attempted)
	})
	if err != nil {
		return &GenerationSubmissionError{Uncertain: attempted, Cause: err}
	}
	return nil
}

func (session *newAPITaskSession) submit(ctx context.Context, t *GenerationTask, descriptor pluginTask.Request, attempted *bool) error {
	value := session.context(t)
	body, status, headers, err := session.request(ctx, descriptor, http.MethodPost, attempted, value)
	if status != 0 && (status < 200 || status >= 300) {
		if status >= 400 && status < 500 && status != 408 && status != 409 {
			*attempted = false
		}
		return fmt.Errorf("task submission returned HTTP %d; requires reconciliation", status)
	}
	if err != nil {
		return err
	}
	return session.scriptDriver().AcceptSubmission(ctx, t, generation.ScriptResponse{Status: status, Headers: headers, Body: body})
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
	return s.withAuthorizedGenerationSession(ctx, account, t, func(session *newAPITaskSession) error {
		if err := session.exchange(ctx); err != nil {
			return err
		}
		driver := session.scriptDriver()
		var descriptor pluginTask.Request
		var err error
		batch := session.version.Meta.FetchMode == "batch"
		if batch {
			descriptor, err = driver.BuildBatchQuery(ctx, t)
		} else {
			descriptor, err = driver.BuildRequest(ctx, t, false)
		}
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
		response := generation.ScriptResponse{Status: status, Headers: headers, Body: body}
		if batch {
			return driver.AcceptBatchQuery(ctx, t, response)
		}
		return driver.AcceptQuery(ctx, t, response)
	})
}

// QuoteTask executes an optional pre-submit quote hook when the bound plugin
// revision declares buildQuoteRequest+parseQuoteResponse or quotePrice.
// Stock New API plugins do not declare quote hooks and return (nil, nil)
// immediately without acquiring a worker or performing any network I/O.
func (s *NewAPIPluginService) QuoteTask(ctx context.Context, account *Account, t *GenerationTask) (*GenerationCost, error) {
	if s == nil || s.repo == nil || account == nil || t == nil {
		return nil, nil
	}
	version, err := s.repo.Get(ctx, t.PluginVersionID)
	if err != nil {
		return nil, err
	}
	hasHTTP := slices.Contains(version.Capabilities.Hooks, "buildQuoteRequest") && slices.Contains(version.Capabilities.Hooks, "parseQuoteResponse")
	hasPure := slices.Contains(version.Capabilities.Hooks, "quotePrice")
	if !hasHTTP && !hasPure {
		return nil, nil
	}
	quoteCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var quote *GenerationCost
	err = s.withAuthorizedGenerationSession(quoteCtx, account, t, func(session *newAPITaskSession) error {
		driver := session.scriptDriver()
		if driver.HasQuoteHTTP() {
			if err := session.exchange(quoteCtx); err != nil {
				return err
			}
			descriptor, err := driver.BuildQuoteRequest(quoteCtx, t)
			if err != nil {
				return err
			}
			if descriptor.Method != "" && descriptor.Method != http.MethodGet && descriptor.Method != http.MethodPost {
				return errors.New("quote request must use GET or POST")
			}
			body, status, headers, err := session.request(quoteCtx, descriptor, http.MethodPost, nil, nil)
			if err != nil {
				return err
			}
			q, err := driver.ParseQuoteResponse(quoteCtx, t, generation.ScriptResponse{Status: status, Headers: headers, Body: body})
			if err != nil {
				return err
			}
			quote = q
			return nil
		}
		if driver.HasQuotePure() {
			q, err := driver.QuotePrice(quoteCtx, t)
			if err != nil {
				return err
			}
			quote = q
			return nil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return quote, nil
}

// CancelTask executes a plugin's optional buildCancelRequest/parseCancelResponse
// hooks for an active task with an upstream ID. When buildCancelRequest is not
// exported, supported is false and no upstream I/O occurs. An HTTP 200/204
// without explicit parseCancelResponse confirmation never returns Confirmed=true.
func (s *NewAPIPluginService) CancelTask(ctx context.Context, account *Account, t *GenerationTask) (generation.CancelOutcome, bool, error) {
	if s == nil || s.repo == nil || account == nil || t == nil || t.UpstreamID == "" {
		return generation.CancelOutcome{}, false, nil
	}
	version, err := s.repo.Get(ctx, t.PluginVersionID)
	if err != nil {
		return generation.CancelOutcome{}, false, err
	}
	if version == nil || !slices.Contains(version.Capabilities.Hooks, "buildCancelRequest") {
		return generation.CancelOutcome{}, false, nil
	}
	cancelCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var outcome generation.CancelOutcome
	err = s.withAuthorizedGenerationSession(cancelCtx, account, t, func(session *newAPITaskSession) error {
		driver := session.scriptDriver()
		if !driver.HasCancelHTTP() {
			return nil
		}
		if err := session.exchange(cancelCtx); err != nil {
			return err
		}
		descriptor, err := driver.BuildCancelRequest(cancelCtx, t)
		if err != nil {
			return err
		}
		switch descriptor.Method {
		case "", http.MethodDelete, http.MethodPost, http.MethodPut, http.MethodPatch:
		default:
			return errors.New("cancel request must use DELETE, POST, PUT, or PATCH")
		}
		body, status, headers, err := session.requestRaw(cancelCtx, descriptor, http.MethodDelete)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("upstream cancel request returned HTTP %d", status)
		}
		if !driver.HasCancelParser() {
			outcome = generation.CancelOutcome{Confirmed: false, Status: "CANCEL_REQUESTED"}
			return nil
		}
		if len(bytes.TrimSpace(body)) == 0 {
			body = []byte(`{}`)
		} else if !json.Valid(body) {
			return errors.New("cancel upstream response is not JSON")
		}
		parsed, err := driver.ParseCancelResponse(cancelCtx, t, generation.ScriptResponse{Status: status, Headers: headers, Body: body})
		if err != nil {
			return err
		}
		outcome = parsed
		return nil
	})
	return outcome, true, err
}

func (s *newAPITaskSession) requestRaw(ctx context.Context, descriptor pluginTask.Request, defaultMethod string) ([]byte, int, map[string]string, error) {
	if descriptor.Method == "" {
		descriptor.Method = defaultMethod
	}
	if err := jsplugin.ValidateRequestHeaders(descriptor.Headers); err != nil {
		return nil, 0, nil, err
	}
	if err := jsplugin.ValidateRequestURL(descriptor.URL, s.base, s.hosts); err != nil {
		return nil, 0, nil, err
	}
	if _, err := s.service.gateway.validateOutboundURL(descriptor.URL); err != nil {
		return nil, 0, nil, err
	}
	var body []byte
	var err error
	if len(descriptor.Body) > 0 {
		body, err = descriptor.ResolveBody(ctx, s.openFile)
		if err != nil {
			return nil, 0, nil, err
		}
	}
	digest := sha256.Sum256(body)
	opCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(WithHTTPUpstreamRedirectsDisabled(opCtx), descriptor.Method, descriptor.URL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, nil, err
	}
	req.ContentLength = int64(len(body))
	for key, value := range descriptor.Headers {
		req.Header.Set(key, value)
	}
	if req.Header.Get("Content-Type") == "" && len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if !descriptor.Credentialless {
		if err = s.signDigest(ctx, req, hex.EncodeToString(digest[:])); err != nil {
			return nil, 0, nil, err
		}
	} else {
		req.Header.Del("Authorization")
		req.Header.Del("X-Api-Key")
		req.Header.Del("Cookie")
		req = req.WithContext(WithHTTPUpstreamPublicHostsOnly(req.Context()))
	}
	resp, err := s.service.gateway.httpUpstream.Do(req, s.proxy(), s.account.ID, s.account.Concurrency)
	if err != nil {
		return nil, 0, nil, errors.New("task upstream transport failed")
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, pluginTask.MaxSnapshotBytes+1))
	if err != nil {
		return nil, resp.StatusCode, nil, err
	}
	if len(respBody) > pluginTask.MaxSnapshotBytes {
		return nil, resp.StatusCode, nil, errors.New("task upstream response exceeds limit")
	}
	return respBody, resp.StatusCode, pluginResponseHeaders(resp.Header), nil
}

func (s *newAPITaskSession) scriptDriver() generation.ScriptDriver {
	return generation.ScriptDriver{
		Hooks: generation.ScriptHooks{Has: s.has, Call: func(ctx context.Context, hook string, args ...any) (json.RawMessage, error) {
			return s.call(ctx, hook, nil, args...)
		}},
		Context:     s.context,
		OnResultURL: s.service.setTaskReportResultURL,
	}
}

func nonemptyPluginJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`{}`)
	}
	return raw
}
