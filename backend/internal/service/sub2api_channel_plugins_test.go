package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func channelTask(t *testing.T, s *NewAPIPluginService, a *Account, model, body string) *GenerationTask {
	t.Helper()
	decoded, action, err := s.DecodeVideo(context.Background(), a, 1, model, json.RawMessage(body))
	require.NoError(t, err)
	return &GenerationTask{ID: "video_" + strings.Repeat("7", 40), PluginVersionID: 1, Model: model, UpstreamModel: a.GetMappedModel(model), Action: action, Request: decoded, State: json.RawMessage(`{}`), Data: json.RawMessage(`{}`), CreatedAt: time.Now()}
}

func TestSub2APIChannelsFinalBillLifecycle(t *testing.T) {
	for _, key := range []string{"sub2api-dreamina", "sub2api-official5"} {
		t.Run(key, func(t *testing.T) {
			s, a, h := generationPluginFixture(t, key)
			model := "seedance-2.0"
			if key == "sub2api-official5" {
				model = "cc-seedance-fixture"
			}
			task := channelTask(t, s, a, model, fmt.Sprintf(`{"model":%q,"prompt":"Ocean","seconds":"8","size":"1280x720"}`, model))
			var err error
			task.Usage, err = s.EstimateTask(context.Background(), a, task)
			require.NoError(t, err)
			h.status = 202
			h.response = `{"code":0,"data":{"id":"upstream-123"}}`
			require.NoError(t, s.SubmitTask(context.Background(), a, task))
			require.Equal(t, task.ID, h.requests[0].Header.Get("X-Request-ID"))
			require.Equal(t, "Bearer fixture-key", h.requests[0].Header.Get("Authorization"))
			sent, err := io.ReadAll(h.requests[0].Body)
			require.NoError(t, err)
			require.Contains(t, string(sent), `"duration":8`)
			require.NotContains(t, string(sent), `"seconds"`)
			if key == "sub2api-official5" {
				require.Contains(t, string(sent), `"provider_mode":"standard"`)
				require.NotContains(t, string(sent), "generate_audio")
			}
			h.status = 200
			for _, state := range []string{"queued", "running"} {
				h.response = fmt.Sprintf(`{"code":0,"data":{"id":"upstream-123","model":%q,"status":%q,"waiting_for_credits":true}}`, model, state)
				require.NoError(t, s.PollTask(context.Background(), a, task))
				require.Equal(t, GenerationRunning, task.Status)
			}
			// Generated but the original has not been saved yet.
			h.response = `{"code":0,"data":{"id":"upstream-123","status":"succeeded","billing_status":"frozen"}}`
			require.NoError(t, s.PollTask(context.Background(), a, task))
			require.Equal(t, GenerationRunning, task.Status)
			h.response = `{"code":0,"data":{"id":"upstream-123","status":"succeeded","video_url":"https://untrusted.example/not-used.mp4","billing_status":"frozen","final_cost":0}}`
			require.NoError(t, s.PollTask(context.Background(), a, task))
			require.Equal(t, GenerationSucceeded, task.Status)
			require.Equal(t, GenerationDeliveryReady, task.DeliveryStatus)
			require.True(t, generationBillingPending(task))
			finished := *task.FinishedAt
			view, err := s.RenderVideo(context.Background(), a, task)
			require.NoError(t, err)
			require.Equal(t, task.ID, view["id"])
			require.Equal(t, "completed", view["status"])
			response, err := s.OpenTaskContent(context.Background(), a, task, http.MethodHead, "video", http.Header{"Range": []string{"bytes=0-9"}})
			require.NoError(t, err)
			response.Body.Close()
			download := h.requests[len(h.requests)-1]
			require.Equal(t, "api.example.com", download.URL.Host)
			require.Equal(t, "/api/v1/video-generations/upstream-123/content", download.URL.Path)
			require.Equal(t, "Bearer fixture-key", download.Header.Get("Authorization"))
			require.Equal(t, "bytes=0-9", download.Header.Get("Range"))
			h.response = `{"code":0,"data":{"id":"upstream-123","status":"succeeded","video_url":"https://untrusted.example/not-used.mp4","billing_status":"billed","final_cost":"1.23456789"}}`
			require.NoError(t, s.PollTask(context.Background(), a, task))
			require.False(t, generationBillingPending(task))
			require.Equal(t, finished, *task.FinishedAt)
			var facts map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(task.Usage, &facts))
			cost := generationObservedCost(facts["_host_final_usage"])
			require.NotNil(t, cost)
			require.Equal(t, "1.23456789", cost.Amount.String())
			require.Equal(t, "CNY", cost.Currency)
			posts := 0
			for _, r := range h.requests {
				if r.Method == http.MethodPost {
					posts++
				}
			}
			require.Equal(t, 1, posts)
			// Explicit final zero is valid; missing final cost is never free.
			for _, sample := range []struct {
				cost    string
				pending bool
			}{{`,"final_cost":0`, false}, {``, true}, {`,"final_cost":null`, true}, {`,"final_cost":-1`, true}} {
				h.response = `{"code":0,"data":{"id":"upstream-123","status":"succeeded","video_url":"https://example.com/video","billing_status":"billed"` + sample.cost + `}}`
				require.NoError(t, s.PollTask(context.Background(), a, task))
				require.Equal(t, sample.pending, generationBillingPending(task))
			}
			h.response = `{"code":0,"data":{"id":"upstream-123","status":"failed","refund_status":"refunded"}}`
			require.NoError(t, s.PollTask(context.Background(), a, task))
			require.Equal(t, GenerationFailed, task.Status)
			require.False(t, generationBillingPending(task))
		})
	}
}

func TestSub2APIChannelReferencesAndValidation(t *testing.T) {
	s, a, h := generationPluginFixture(t, "sub2api-dreamina")
	task := channelTask(t, s, a, "seedance-2.0", `{"model":"seedance-2.0","prompt":"Reference [@Image 1]","seconds":4,"metadata":{"task_mode":"references","content":[{"type":"image_url","role":"reference_image","url":"https://cdn.example.com/image.png"},{"type":"image_url","role":"reference_image","storage_object_id":"object2"}]}}`)
	require.Contains(t, string(task.Request), "object2")
	require.Less(t, strings.Index(string(task.Request), "image.png"), strings.Index(string(task.Request), "object2"))
	for _, body := range []string{
		`{"prompt":"x","seconds":3}`, `{"prompt":"x","seconds":4,"metadata":{"resolution":"480p"}}`,
		`{"prompt":"x","seconds":4,"seed":1}`, `{"prompt":"x","seconds":4,"metadata":{"content":[{"type":"image_url","url":"file:///tmp/x"}]}}`,
		`{"prompt":"x","input_reference":"data:image/png;base64,aaaa"}`, `{"prompt":"x","metadata":{"task_mode":"references"}}`,
	} {
		_, _, err := s.DecodeVideo(context.Background(), a, 1, "seedance-2.0", json.RawMessage(body))
		require.Error(t, err, body)
	}
	require.Empty(t, h.requests, "decode validation must not submit requests")
	s, a, _ = generationPluginFixture(t, "sub2api-official5")
	for _, body := range []string{`{"prompt":"x","input_reference":"https://cdn.example.com/image.png"}`, `{"prompt":"x","metadata":{"content":[{"type":"image_url","url":"https://cdn.example.com/image.png"}]}}`, `{"prompt":"x","generate_audio":false}`, `{"prompt":"x","metadata":{"task_mode":"first_last_frame"}}`} {
		_, _, err := s.DecodeVideo(context.Background(), a, 1, "cc-seedance-fixture", json.RawMessage(body))
		require.Error(t, err, body)
	}
	task = channelTask(t, s, a, "cc-seedance-fixture", `{"prompt":"x","metadata":{"content":[{"type":"image_url","storage_object_id":"uploaded-object"}]}}`)
	require.Contains(t, string(task.Request), `"task_mode":"references"`)
	a.Credentials[newAPIPluginVideoModelsKey] = []string{"cc-seedance-*"}
	a.Credentials["model_mapping"] = map[string]any{"my-video": "cc-seedance-fixture"}
	require.True(t, a.SupportsNewAPIVideoModel("my-video"))
	require.Equal(t, []string{"my-video"}, a.NewAPIVideoModels())
	require.False(t, a.SupportsNewAPIVideoModel("cc-seedance-*"))
}

func TestSub2APIChannelIdentityAndSegmentDelivery(t *testing.T) {
	s, a, h := generationPluginFixture(t, "sub2api-official5")
	task := channelTask(t, s, a, "cc-seedance-fixture", `{"prompt":"x"}`)
	h.response = `{"code":0,"data":{"id":"up-1"}}`
	require.NoError(t, s.SubmitTask(context.Background(), a, task))
	for _, body := range []string{`{"code":0,"data":{"id":"other","status":"succeeded"}}`, `{"code":0,"data":{"id":"up-1","model":"other","status":"running"}}`, `{"code":1,"data":{"id":"up-1","status":"succeeded"}}`} {
		h.response = body
		require.Error(t, s.PollTask(context.Background(), a, task))
		require.Equal(t, GenerationRunning, task.Status)
	}
	h.response = `{"code":0,"data":{"id":"up-1","status":"succeeded","video_url":"https://example.com/segment","delivery_kind":"segment","assembly_status":"not_assembled","billing_status":"billed","final_cost":1}}`
	require.NoError(t, s.PollTask(context.Background(), a, task))
	require.Equal(t, GenerationSucceeded, task.Status)
	require.NotEqual(t, GenerationDeliveryReady, task.DeliveryStatus)
	view, err := s.RenderVideo(context.Background(), a, task)
	require.NoError(t, err)
	require.Equal(t, "final_video_unavailable", view["error"].(map[string]any)["code"])
	_, err = s.OpenTaskContent(context.Background(), a, task, http.MethodGet, "video", nil)
	require.Error(t, err)
}

func TestSub2APIFinalBillPricingAndWorkerWait(t *testing.T) {
	cap, rate, markup := decimal.NewFromInt(10), decimal.RequireFromString("0.14"), decimal.RequireFromString("1.2")
	rule := GenerationPriceRule{Mode: "cost_plus", SourceUnit: "upstream_cost", SourceCurrency: "CNY", ExchangeRate: &rate, Markup: &markup, Maximum: &cap}
	price, err := NewGenerationPriceSnapshot(rule, decimal.NewFromInt(1), nil, decimal.NewFromInt(2))
	require.NoError(t, err)
	require.Equal(t, "20", price.Hold.String())
	require.Nil(t, price.Quote)
	repo := &generationSettlementFixture{}
	svc := &GenerationService{repo: repo}
	task := &GenerationTask{Status: GenerationSucceeded, BillingStatus: GenerationBillingPending, Price: price, Usage: json.RawMessage(`{"_host_final_usage":{"upstream_cost":{"amount":"2","currency":"CNY","unit":"upstream_cost","final":true,"source":"sub2api_final_cost"}}}`)}
	require.NoError(t, svc.settle(context.Background(), task))
	require.True(t, repo.settled)
	require.Equal(t, "0.672", repo.amount.String())
	repo.settled = false
	task.BillingStatus = GenerationBillingPending
	task.Usage = json.RawMessage(`{"_host_final_usage":{"upstream_cost":{"amount":"2","currency":"USD","unit":"upstream_cost","final":true,"source":"sub2api_final_cost"}}}`)
	require.NoError(t, svc.settle(context.Background(), task))
	require.False(t, repo.settled)
	require.Equal(t, GenerationBillingReview, task.BillingStatus)
	past := time.Now().Add(-3 * time.Hour)
	task.FinishedAt = &past
	task.Usage = json.RawMessage(`{"_host_final_usage":{"billing_pending":true}}`)
	require.NoError(t, svc.process(context.Background(), task))
	require.Equal(t, "upstream_billing_timeout", task.ErrorCode)
	require.False(t, repo.settled)
	task.ReconciledAmount = &rate
	require.False(t, generationBillingPending(task))
	require.NoError(t, svc.process(context.Background(), task))
	require.True(t, repo.settled)
	require.Equal(t, "0.14", repo.amount.String())
	rule.Maximum = nil
	require.Error(t, rule.Validate())
}

func TestSub2APIWorkerPollsPendingBillWithoutResubmission(t *testing.T) {
	plugins, account, httpClient := generationPluginFixture(t, "sub2api-official5")
	task := channelTask(t, plugins, account, "cc-seedance-fixture", `{"prompt":"Ocean"}`)
	httpClient.response = `{"code":0,"data":{"id":"worker-task"}}`
	require.NoError(t, plugins.SubmitTask(context.Background(), account, task))
	httpClient.response = `{"code":0,"data":{"id":"worker-task","status":"succeeded","video_url":"https://example.com/video","billing_status":"frozen"}}`
	require.NoError(t, plugins.PollTask(context.Background(), account, task))
	task.CreatedAt = time.Now().Add(-2 * time.Hour)
	finished := time.Now().Add(-30 * time.Minute)
	task.FinishedAt = &finished
	unit := decimal.NewFromInt(1)
	price, err := NewGenerationPriceSnapshot(GenerationPriceRule{Mode: "fixed", Unit: "request", UnitPrice: &unit}, unit, nil, unit)
	require.NoError(t, err)
	task.Price = price
	repo := &generationSettlementFixture{}
	plugins.gateway.concurrencyService = NewConcurrencyService(nil) // Fixture concurrency is unlimited.
	svc := &GenerationService{repo: repo, plugins: plugins, accounts: channelAccountFixture{account: account}, gateway: plugins.gateway}
	require.NoError(t, svc.process(context.Background(), task))
	require.False(t, repo.settled)
	require.True(t, generationBillingPending(task))
	httpClient.response = `{"code":0,"data":{"id":"worker-task","status":"succeeded","video_url":"https://example.com/video","billing_status":"billed","final_cost":1}}`
	require.NoError(t, svc.process(context.Background(), task))
	require.False(t, generationBillingPending(task))
	require.False(t, repo.settled)
	require.NoError(t, svc.process(context.Background(), task))
	require.True(t, repo.settled)
	posts := 0
	for _, req := range httpClient.requests {
		if req.Method == http.MethodPost {
			posts++
		}
	}
	require.Equal(t, 1, posts)
	require.Equal(t, finished, *task.FinishedAt)
}

type channelAccountFixture struct {
	AccountRepository
	account *Account
}

func (r channelAccountFixture) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

type channelRedirectHTTP struct {
	HTTPUpstream
	requests []*http.Request
	location string
}

func (h *channelRedirectHTTP) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	h.requests = append(h.requests, req)
	status := 200
	headers := http.Header{}
	if len(h.requests) == 1 {
		status = 302
		headers.Set("Location", h.location)
	}
	return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader("video fixture"))}, nil
}
func TestSub2APIContentRedirectNeverForwardsCredentials(t *testing.T) {
	s, a, _ := generationPluginFixture(t, "sub2api-dreamina")
	task := &GenerationTask{ID: "video_" + strings.Repeat("8", 40), PluginVersionID: 1, UpstreamID: "123", Data: json.RawMessage(`{"code":0,"data":{"video_url":"https://ignored.example/video"}}`)}
	h := &channelRedirectHTTP{location: "https://storage.example/video?signature=fixture"}
	s.gateway.httpUpstream = h
	response, err := s.OpenTaskContent(context.Background(), a, task, "GET", "video", http.Header{"Range": []string{"bytes=0-4"}, "Cookie": []string{"private"}, "Authorization": []string{"downstream-private"}})
	require.NoError(t, err)
	response.Body.Close()
	require.Len(t, h.requests, 2)
	require.Equal(t, "Bearer fixture-key", h.requests[0].Header.Get("Authorization"))
	require.Empty(t, h.requests[1].Header.Get("Authorization"))
	require.Empty(t, h.requests[1].Header.Get("Cookie"))
	require.Equal(t, "bytes=0-4", h.requests[1].Header.Get("Range"))
	for _, target := range []string{"http://storage.example/video", "https://user:secret@storage.example/video", "https://storage.example/video#fragment"} {
		h.requests = nil
		h.location = target
		_, err = s.OpenTaskContent(context.Background(), a, task, "GET", "video", nil)
		require.Error(t, err)
		require.Len(t, h.requests, 1)
	}
}
