package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/stretchr/testify/require"
)

type genericVideoHTTP struct {
	*generationFixtureHTTP
	bodies [][]byte
}

func (h *genericVideoHTTP) Do(req *http.Request, proxy string, account int64, concurrency int) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
	}
	h.bodies = append(h.bodies, body)
	return h.generationFixtureHTTP.Do(req, proxy, account, concurrency)
}

func genericVideoFixture(t *testing.T) (*NewAPIPluginService, *Account, *genericVideoHTTP) {
	t.Helper()
	s, account, upstream := generationPluginFixture(t, "sub2api-openai-video")
	s.encryptor = pluginTokenEncryptor{}
	account.Credentials["model_mapping"] = map[string]any{"my-video": "vendor-movie-v2"}
	account.Credentials[newAPIPluginVideoModelsKey] = []string{"*"}
	httpClient := &genericVideoHTTP{generationFixtureHTTP: upstream}
	s.gateway.httpUpstream = httpClient
	return s, account, httpClient
}

func genericVideoTask(t *testing.T, s *NewAPIPluginService, account *Account, ctx context.Context, body string) *GenerationTask {
	t.Helper()
	decoded, action, err := s.DecodeVideo(ctx, account, 1, "my-video", json.RawMessage(body))
	require.NoError(t, err)
	return &GenerationTask{ID: "video_" + strings.Repeat("c", 40), UserID: 1, APIKeyID: 2, GroupID: 3,
		AccountID: account.ID, PluginVersionID: 1, Model: "my-video", UpstreamModel: account.GetMappedModel("my-video"),
		Action: action, Request: decoded, CreatedAt: time.Now().UTC()}
}

func genericVideoMultipart(t *testing.T, request *http.Request, data []byte) map[string]string {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	reader := multipart.NewReader(bytes.NewReader(data), params["boundary"])
	fields := map[string]string{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return fields
		}
		require.NoError(t, err)
		_, duplicate := fields[part.FormName()]
		require.False(t, duplicate)
		value, err := io.ReadAll(part)
		require.NoError(t, err)
		fields[part.FormName()] = string(value)
	}
}

func TestGenericVideoPluginSubmitPollAndContent(t *testing.T) {
	for _, tc := range []struct{ base, prefix string }{
		{"https://api.example.com", "/v1/videos"},
		{"https://api.example.com/v1/", "/v1/videos"},
		{"https://api.example.com/proxy", "/proxy/v1/videos"},
		{"https://api.example.com/proxy/v1", "/proxy/v1/videos"},
	} {
		t.Run(tc.base, func(t *testing.T) {
			s, account, upstream := genericVideoFixture(t)
			account.Credentials["base_url"] = tc.base
			ctx := context.Background()
			task := genericVideoTask(t, s, account, ctx, `{"model":"my-video","prompt":"ocean","duration":8,"size":"1280x720","metadata":{"resolution":"720p"}}`)
			require.Equal(t, "text_to_video", task.Action)
			var err error
			task.Usage, err = s.EstimateTask(ctx, account, task)
			require.NoError(t, err)
			require.JSONEq(t, `{"seconds":8}`, string(task.Usage))
			upstream.response = `{"id":"private-id:123","status":"queued","private":"do not expose"}`
			require.NoError(t, s.SubmitTask(ctx, account, task))
			require.Equal(t, "private-id:123", task.UpstreamID)
			request := upstream.requests[0]
			require.Equal(t, tc.prefix, request.URL.Path)
			require.Equal(t, "Bearer fixture-key", request.Header.Get("Authorization"))
			fields := genericVideoMultipart(t, request, upstream.bodies[0])
			require.Equal(t, "vendor-movie-v2", fields["model"])
			require.Equal(t, "8", fields["seconds"])
			require.NotContains(t, fields, "duration")
			require.JSONEq(t, `{"resolution":"720p"}`, fields["metadata"])
			for _, status := range []string{"queued", "in_progress", "completed"} {
				upstream.response = `{"id":"private-id:123","status":"` + status + `","seconds":"6","progress":50,"private":"do not expose"}`
				require.NoError(t, s.PollTask(ctx, account, task))
				require.Equal(t, tc.prefix+"/private-id:123", upstream.requests[len(upstream.requests)-1].URL.Path)
			}
			require.Equal(t, GenerationSucceeded, task.Status)
			require.Equal(t, GenerationDeliveryReady, task.DeliveryStatus)
			require.JSONEq(t, `{"seconds":6,"_host_final_usage":{"seconds":6}}`, string(task.Usage))
			view, err := s.RenderVideo(ctx, account, task)
			require.NoError(t, err)
			require.Equal(t, task.ID, view["id"])
			require.Equal(t, "my-video", view["model"])
			require.Equal(t, "completed", view["status"])
			public, err := json.Marshal(view)
			require.NoError(t, err)
			require.NotContains(t, string(public), "private-id")
			require.NotContains(t, string(public), "do not expose")
			// Existing tasks retain their accepted connection after account edits.
			task.Connection = generationAccountConnection(account)
			account.Credentials["base_url"] = "https://changed.example.com"
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				response, err := s.OpenTaskContent(ctx, account, task, method, "video", http.Header{"Range": {"bytes=0-99"}, "Authorization": {"Bearer downstream-secret"}})
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
				request = upstream.requests[len(upstream.requests)-1]
				require.Equal(t, "api.example.com", request.URL.Host)
				require.Equal(t, tc.prefix+"/private-id:123/content", request.URL.Path)
				require.Equal(t, method, request.Method)
				require.Equal(t, "bytes=0-99", request.Header.Get("Range"))
				require.Equal(t, "Bearer fixture-key", request.Header.Get("Authorization"))
			}
		})
	}
}

func TestGenericVideoPluginJSONAndFileSubmission(t *testing.T) {
	s, account, upstream := genericVideoFixture(t)
	ctx := context.Background()
	task := genericVideoTask(t, s, account, ctx, `{"prompt":"ocean","seconds":"8","metadata":{"resolution":"720p"}}`)
	task.ConfigEncrypted = `ENC:{"submission_format":"json"}`
	upstream.response = `{"id":"json-task"}`
	require.NoError(t, s.SubmitTask(ctx, account, task))
	require.Equal(t, "application/json", upstream.requests[0].Header.Get("Content-Type"))
	require.JSONEq(t, `{"model":"vendor-movie-v2","prompt":"ocean","seconds":"8","metadata":{"resolution":"720p"}}`, string(upstream.bodies[0]))

	files := &generationMemoryFiles{file: GenerationFile{ID: "vfile_" + strings.Repeat("a", 40), Filename: "fixture.png", MimeType: "image/png", Size: 3, SHA256: strings.Repeat("b", 64)}, data: "png"}
	s.files = files
	httpClient := &generationFileHTTP{}
	s.gateway.httpUpstream = httpClient
	file := GenerationFileSnapshot{ID: files.file.ID, SHA256: files.file.SHA256, File: pluginTask.File{Ref: "request_file:input_reference", Field: "input_reference", Filename: files.file.Filename, MimeType: files.file.MimeType, Size: files.file.Size}}
	ctx = context.WithValue(ctx, generationDecodeFilesKey{}, []GenerationFileSnapshot{file})
	task = genericVideoTask(t, s, account, ctx, `{"prompt":"ocean","seconds":"8"}`)
	require.Equal(t, "image_to_video", task.Action)
	task.ConfigEncrypted = `ENC:{"submission_format":"json"}`
	task.Files = []GenerationFileSnapshot{file}
	require.NoError(t, s.SubmitTask(ctx, account, task))
	fields := genericVideoMultipart(t, httpClient.request, httpClient.body)
	require.Equal(t, "png", fields["input_reference"])
	require.Equal(t, "vendor-movie-v2", fields["model"])
	// A foreign key cannot reuse the accepted private file reference.
	task.APIKeyID = 99
	err := s.SubmitTask(ctx, account, task)
	var submission *GenerationSubmissionError
	require.ErrorAs(t, err, &submission)
	require.False(t, submission.Uncertain)
}

func TestGenericVideoPluginRejectsInvalidRequests(t *testing.T) {
	s, account, upstream := genericVideoFixture(t)
	for _, body := range []string{
		`{"seconds":8}`, `{"prompt":" "}`, `{"prompt":"ocean","seconds":true}`,
		`{"prompt":"ocean","seconds":[8]}`, `{"prompt":"ocean","seconds":null}`,
		`{"prompt":"ocean","seconds":0}`, `{"prompt":"ocean","seconds":3601}`,
		`{"prompt":"ocean","seconds":"abc"}`, `{"prompt":"ocean","seconds":8,"duration":6}`,
		`{"prompt":"ocean","metadata":[]}`,
	} {
		_, _, err := s.DecodeVideo(context.Background(), account, 1, "my-video", json.RawMessage(body))
		require.Error(t, err, body)
	}
	for _, fields := range [][]string{{"unknown_file"}, {"input_reference", "input_reference"}} {
		files := make([]GenerationFileSnapshot, len(fields))
		for i, name := range fields {
			files[i].File = pluginTask.File{Ref: "request_file:" + name, Field: name, Filename: "fixture.png", MimeType: "image/png", Size: 3}
		}
		ctx := context.WithValue(context.Background(), generationDecodeFilesKey{}, files)
		_, _, err := s.DecodeVideo(ctx, account, 1, "my-video", json.RawMessage(`{"prompt":"ocean","seconds":8}`))
		require.Error(t, err)
	}
	require.Empty(t, upstream.requests, "invalid requests never reach a paid endpoint")
}

func TestGenericVideoPluginSubmissionUncertaintyAndUnknownStatus(t *testing.T) {
	s, account, upstream := genericVideoFixture(t)
	task := genericVideoTask(t, s, account, context.Background(), `{"prompt":"ocean","seconds":8}`)
	upstream.err = errors.New("transport timeout")
	err := s.SubmitTask(context.Background(), account, task)
	var submission *GenerationSubmissionError
	require.ErrorAs(t, err, &submission)
	require.True(t, submission.Uncertain)
	require.Len(t, upstream.requests, 1, "the driver must not blindly retry a paid submit")
	upstream.err, upstream.response = nil, `{"status":"queued"}`
	err = s.SubmitTask(context.Background(), account, task)
	require.ErrorAs(t, err, &submission)
	require.True(t, submission.Uncertain, "a 2xx response without an id needs reconciliation")
	task.UpstreamID = "accepted-task"
	upstream.response = `{"id":"accepted-task","status":"new-vendor-status"}`
	require.NoError(t, s.PollTask(context.Background(), account, task))
	require.Equal(t, GenerationBillingReview, task.BillingStatus)
	require.NotEqual(t, GenerationSucceeded, task.Status)
}

func TestGenericVideoPluginHoldSettlementAndReplay(t *testing.T) {
	s, account, upstream := genericVideoFixture(t)
	account.Credentials[NewAPIVideoPricingKey] = map[string]any{"my-video": map[string]any{"mode": "fixed", "unit": "second", "unit_price": "0.02"}}
	repo := &generationReplayFixture{}
	svc := &GenerationService{repo: repo, plugins: s}
	key := &APIKey{ID: 2, UserID: 1, User: &User{ID: 1}, Group: &Group{ID: 3, RateMultiplier: 1}}
	body := json.RawMessage(`{"model":"my-video","prompt":"ocean","seconds":"8"}`)
	task, err := svc.Create(context.Background(), key, nil, account, "my-video", body, "create-once")
	require.NoError(t, err)
	require.Equal(t, "vendor-movie-v2", task.UpstreamModel)
	require.Equal(t, "0.16", task.Price.Hold.String())
	require.Empty(t, upstream.requests)
	upstream.response = `{"id":"paid-task"}`
	require.NoError(t, s.SubmitTask(context.Background(), account, task))
	upstream.response = `{"id":"paid-task","status":"completed","seconds":"6"}`
	require.NoError(t, s.PollTask(context.Background(), account, task))
	settlement := &generationSettlementFixture{}
	require.NoError(t, (&GenerationService{repo: settlement}).settle(context.Background(), task))
	require.True(t, settlement.settled)
	require.Equal(t, "0.12", settlement.amount.String(), "settle the observed duration, not the hold estimate")
	repo.task, svc.plugins = task, nil
	replayed, err := svc.Create(context.Background(), key, nil, &Account{}, "my-video", body, "create-once")
	require.NoError(t, err)
	require.Same(t, task, replayed)
	require.Len(t, upstream.requests, 2, "replay does not create another paid task")
	_, err = svc.Create(context.Background(), key, nil, &Account{}, "my-video", json.RawMessage(`{"model":"my-video","prompt":"different","seconds":"8"}`), "create-once")
	require.ErrorIs(t, err, ErrGenerationIdempotencyConflict)
}

func TestGenericVideoPluginPricingBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, rule, request, result, hold, amount string
		admissionError, review                    bool
	}{
		{name: "missing price", request: `{"prompt":"ocean","seconds":8}`, admissionError: true},
		{name: "missing seconds cannot estimate", rule: `{"mode":"fixed","unit":"second","unit_price":"0.02"}`, request: `{"prompt":"ocean"}`, admissionError: true},
		{name: "missing final seconds needs review", rule: `{"mode":"fixed","unit":"second","unit_price":"0.02"}`, request: `{"prompt":"ocean","seconds":8}`, result: `{"status":"completed"}`, hold: "0.16", review: true},
		{name: "null final seconds needs review", rule: `{"mode":"fixed","unit":"second","unit_price":"0.02"}`, request: `{"prompt":"ocean","seconds":8}`, result: `{"status":"completed","seconds":null}`, hold: "0.16", review: true},
		{name: "invalid final seconds needs review", rule: `{"mode":"fixed","unit":"second","unit_price":"0.02"}`, request: `{"prompt":"ocean","seconds":8}`, result: `{"status":"completed","seconds":"not-a-number"}`, hold: "0.16", review: true},
		{name: "empty final seconds needs review", rule: `{"mode":"fixed","unit":"second","unit_price":"0.02"}`, request: `{"prompt":"ocean","seconds":8}`, result: `{"status":"completed","seconds":""}`, hold: "0.16", review: true},
		{name: "zero final seconds needs review", rule: `{"mode":"fixed","unit":"second","unit_price":"0.02"}`, request: `{"prompt":"ocean","seconds":8}`, result: `{"status":"completed","seconds":0}`, hold: "0.16", review: true},
		{name: "oversized final seconds needs review", rule: `{"mode":"fixed","unit":"second","unit_price":"0.02"}`, request: `{"prompt":"ocean","seconds":8}`, result: `{"status":"completed","seconds":3601}`, hold: "0.16", review: true},
		{name: "failed task refunds", rule: `{"mode":"fixed","unit":"second","unit_price":"0.02"}`, request: `{"prompt":"ocean","seconds":8}`, result: `{"status":"failed"}`, hold: "0.16", amount: "0"},
		{name: "failed task ignores invalid seconds", rule: `{"mode":"fixed","unit":"second","unit_price":"0.02"}`, request: `{"prompt":"ocean","seconds":8}`, result: `{"status":"failed","seconds":null}`, hold: "0.16", amount: "0"},
		{name: "failed task ignores valid seconds", rule: `{"mode":"fixed","unit":"second","unit_price":"0.02"}`, request: `{"prompt":"ocean","seconds":8}`, result: `{"status":"failed","seconds":6}`, hold: "0.16", amount: "0"},
		{name: "per request needs no seconds", rule: `{"mode":"fixed","unit":"request","unit_price":"0.1"}`, request: `{"prompt":"ocean"}`, result: `{"status":"completed"}`, hold: "0.1", amount: "0.1"},
		{name: "explicit upstream price and markup", rule: `{"mode":"cost_plus","source_currency":"USD","source_unit":"second","source_unit_price":"0.01","exchange_rate":"1","markup":"1.5"}`, request: `{"prompt":"ocean","seconds":8}`, result: `{"status":"completed","seconds":6}`, hold: "0.12", amount: "0.09"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, account, upstream := genericVideoFixture(t)
			if tc.rule != "" {
				var rule map[string]any
				require.NoError(t, json.Unmarshal([]byte(tc.rule), &rule))
				account.Credentials[NewAPIVideoPricingKey] = map[string]any{"my-video": rule}
			}
			repo := &generationReplayFixture{}
			svc := &GenerationService{repo: repo, plugins: s}
			key := &APIKey{ID: 2, UserID: 1, User: &User{ID: 1}, Group: &Group{ID: 3, RateMultiplier: 1}}
			task, err := svc.Create(context.Background(), key, nil, account, "my-video", json.RawMessage(tc.request), "once")
			if tc.admissionError {
				require.Error(t, err)
				require.Nil(t, repo.held)
				require.Empty(t, upstream.requests)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.hold, task.Price.Hold.String())
			upstream.response = `{"id":"priced-task"}`
			require.NoError(t, s.SubmitTask(context.Background(), account, task))
			upstream.response = tc.result
			require.NoError(t, s.PollTask(context.Background(), account, task))
			if strings.Contains(tc.result, `"status":"failed"`) {
				require.Equal(t, GenerationFailed, task.Status)
			} else {
				require.Equal(t, GenerationSucceeded, task.Status)
				require.Equal(t, GenerationDeliveryReady, task.DeliveryStatus)
			}
			settlement := &generationSettlementFixture{}
			require.NoError(t, (&GenerationService{repo: settlement}).settle(context.Background(), task))
			if tc.review {
				require.False(t, settlement.settled)
				require.Equal(t, GenerationBillingReview, task.BillingStatus)
			} else {
				require.True(t, settlement.settled)
				require.Equal(t, tc.amount, settlement.amount.String())
			}
		})
	}
}
