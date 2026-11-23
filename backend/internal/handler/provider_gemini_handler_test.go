//go:build unit

package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const handlerProviderGeminiSource = `export const meta={apiVersion:1,key:"gemini-handler-fixture",name:"Gemini handler fixture",version:"1.0.0",author:{name:"Fixture"},models:["vendor-gemini"],
relay:[{incoming_path:"/v1beta/models/{model}:generateContent",upstream_path:"/native/models/{model}:generateContent",models:["vendor-gemini"],auth:{type:"header",name:"x-goog-api-key",value:"{api_key}"}}]};`

type providerGeminiHandlerFixture struct {
	handler   *OpenAIGatewayHandler
	group     *service.Group
	apiKey    *service.APIKey
	slots     *grokMediaSlotsCache
	mu        sync.Mutex
	requests  []*http.Request
	responder func(req *http.Request, accountID int64) (*http.Response, error)
}

func newProviderGeminiHandlerFixture(t *testing.T, accounts ...service.Account) *providerGeminiHandlerFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	loaded, err := jsplugin.CompilePlugin(handlerProviderGeminiSource, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	version := &service.NewAPIPluginVersion{
		ID:           1,
		Source:       handlerProviderGeminiSource,
		Meta:         loaded.Meta,
		Enabled:      true,
		Capabilities: runner.Descriptor{Provider: true},
	}
	group := &service.Group{ID: 31, Platform: service.PlatformOpenAI, RateMultiplier: 1}
	if len(accounts) == 0 {
		accounts = []service.Account{{
			ID:          11,
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeAPIKey,
			Status:      service.StatusActive,
			Schedulable: true,
			Concurrency: 2,
			GroupIDs:    []int64{group.ID},
			Credentials: map[string]any{
				service.NewAPIPluginRevisionKey: 1,
				"base_url":                      "https://api.example.com/prefix",
				"api_key":                       "upstream-gemini-key",
				"model_mapping":                 map[string]any{"public-gemini": "vendor-gemini"},
			},
		}}
	}
	f := &providerGeminiHandlerFixture{
		group: group,
		slots: &grokMediaSlotsCache{accounts: map[string]int64{}, users: map[string]int64{}},
	}
	repo := openAIImagesFailoverAccountRepo{accounts: accounts}
	upstream := &grokMediaSlotUpstream{call: func(req *http.Request, accountID int64) (*http.Response, error) {
		f.mu.Lock()
		f.requests = append(f.requests, req.Clone(context.Background()))
		fn := f.responder
		f.mu.Unlock()
		if fn != nil {
			return fn(req, accountID)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3}}`)),
		}, nil
	}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	concurrency := service.NewConcurrencyService(f.slots)
	gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, &grokMediaSlotBindings{}, cfg, nil, concurrency, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
	plugins, err := service.ProvideNewAPIPluginService(&nativeAdmissionPluginRepo{version: version}, requestHTTPEncryptor{}, gateway, nil, nil, nil, nil)
	require.NoError(t, err)
	t.Cleanup(plugins.Stop)
	for i := range repo.accounts {
		if repo.accounts[i].NewAPIPluginVersionID() > 0 {
			require.NoError(t, plugins.NormalizeAccountBinding(context.Background(), &repo.accounts[i], map[string]any{}))
		}
	}
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	f.handler = NewOpenAIGatewayHandler(gateway, concurrency, billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	f.apiKey = &service.APIKey{
		ID:      20,
		UserID:  10,
		User:    &service.User{ID: 10, Status: service.StatusActive},
		GroupID: &group.ID,
		Group:   group,
	}
	return f
}

func (f *providerGeminiHandlerFixture) call(path, body string, mutate func(c *gin.Context)) (*httptest.ResponseRecorder, bool) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	modelAction := strings.TrimPrefix(path, "/v1beta/models")
	c.Params = gin.Params{{Key: "modelAction", Value: modelAction}}
	c.Set(string(middleware2.ContextKeyAPIKey), f.apiKey)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 10, Concurrency: 5})
	if mutate != nil {
		mutate(c)
	}
	handled := f.handler.TryProviderGemini(c)
	return w, handled
}

func TestTryProviderGeminiTakeoverFallbackAndCompositeMapping(t *testing.T) {
	f := newProviderGeminiHandlerFixture(t)
	reqBody := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`

	t.Run("takeover non-stream JSON", func(t *testing.T) {
		w, handled := f.call("/v1beta/models/public-gemini:generateContent", reqBody, nil)
		require.True(t, handled)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Contains(t, w.Header().Get("Content-Type"), "application/json")
		require.Contains(t, w.Body.String(), `"text":"ok"`)
		f.slots.assertReleased(t)
	})

	t.Run("takeover stream SSE", func(t *testing.T) {
		f.responder = func(*http.Request, int64) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(
					"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"streamed\"}]}}],\"usageMetadata\":{\"promptTokenCount\":4,\"candidatesTokenCount\":2}}\n\n",
				)),
			}, nil
		}
		defer func() { f.responder = nil }()
		w, handled := f.call("/v1beta/models/public-gemini:streamGenerateContent", reqBody, nil)
		require.True(t, handled)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Contains(t, w.Header().Get("Content-Type"), "text/event-stream")
		require.Contains(t, w.Body.String(), "streamed")
		f.slots.assertReleased(t)
	})

	t.Run("takeover via composite upstream model mapping", func(t *testing.T) {
		w, handled := f.call("/v1beta/models/composite-alias:generateContent", reqBody, func(c *gin.Context) {
			compositeGroup := &service.Group{ID: f.group.ID, Platform: service.PlatformComposite, RateMultiplier: 1}
			keyCopy := *f.apiKey
			keyCopy.Group = compositeGroup
			c.Set(string(middleware2.ContextKeyAPIKey), &keyCopy)
			c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), service.CompositeRouteDecision{
				Matched:        true,
				TargetPlatform: service.PlatformOpenAI,
				UpstreamModel:  "public-gemini",
			}))
		})
		require.True(t, handled)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), `"text":"ok"`)
	})

	t.Run("fallback to native gateway when not claimed", func(t *testing.T) {
		cases := []struct {
			name   string
			path   string
			mutate func(c *gin.Context)
		}{
			{
				name: "native gemini group stays on native gateway",
				path: "/v1beta/models/public-gemini:generateContent",
				mutate: func(c *gin.Context) {
					keyCopy := *f.apiKey
					keyCopy.Group = &service.Group{ID: f.group.ID, Platform: service.PlatformGemini}
					c.Set(string(middleware2.ContextKeyAPIKey), &keyCopy)
				},
			},
			{
				name: "antigravity group stays on native gateway",
				path: "/v1beta/models/public-gemini:generateContent",
				mutate: func(c *gin.Context) {
					keyCopy := *f.apiKey
					keyCopy.Group = &service.Group{ID: f.group.ID, Platform: service.PlatformAntigravity}
					c.Set(string(middleware2.ContextKeyAPIKey), &keyCopy)
				},
			},
			{
				name: "countTokens action stays on native gateway",
				path: "/v1beta/models/public-gemini:countTokens",
			},
			{
				name: "unadvertised model stays on native gateway",
				path: "/v1beta/models/gemini-2.5-pro:generateContent",
			},
			{
				name: "unsafe path model name rejected from provider takeover",
				path: "/v1beta/models/../evil:generateContent",
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				w, handled := f.call(tc.path, reqBody, tc.mutate)
				require.False(t, handled)
				require.Zero(t, w.Body.Len())
			})
		}
	})
}

type failingReadCloser struct {
	first []byte
	sent  bool
}

func (r *failingReadCloser) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		n := copy(p, r.first)
		return n, nil
	}
	return 0, errors.New("simulated mid-stream network drop")
}

func (r *failingReadCloser) Close() error { return nil }

func TestTryProviderGeminiErrorFormats(t *testing.T) {
	reqBody := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`

	t.Run("non-stream upstream 502 returns Google error JSON after failover exhaustion", func(t *testing.T) {
		f := newProviderGeminiHandlerFixture(t)
		f.responder = func(*http.Request, int64) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"upstream down"}}`)),
			}, nil
		}
		w, handled := f.call("/v1beta/models/public-gemini:generateContent", reqBody, nil)
		require.True(t, handled)
		require.Equal(t, http.StatusBadGateway, w.Code, w.Body.String())
		require.Contains(t, w.Header().Get("Content-Type"), "application/json")
		require.Equal(t, int64(502), gjson.GetBytes(w.Body.Bytes(), "error.code").Int())
		require.NotEmpty(t, gjson.GetBytes(w.Body.Bytes(), "error.status").String())
		require.NotEmpty(t, gjson.GetBytes(w.Body.Bytes(), "error.message").String())
		f.slots.assertReleased(t)
	})

	t.Run("stream pre-first-chunk upstream 429 returns Google error JSON with application/json", func(t *testing.T) {
		f := newProviderGeminiHandlerFixture(t)
		f.responder = func(*http.Request, int64) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"quota hit"}}`)),
			}, nil
		}
		w, handled := f.call("/v1beta/models/public-gemini:streamGenerateContent", reqBody, nil)
		require.True(t, handled)
		require.Equal(t, http.StatusTooManyRequests, w.Code, w.Body.String())
		require.Contains(t, w.Header().Get("Content-Type"), "application/json")
		require.NotContains(t, w.Header().Get("Content-Type"), "text/event-stream")
		require.Equal(t, int64(429), gjson.GetBytes(w.Body.Bytes(), "error.code").Int())
		require.Equal(t, "RESOURCE_EXHAUSTED", gjson.GetBytes(w.Body.Bytes(), "error.status").String())
		f.slots.assertReleased(t)
	})

	t.Run("stream pre-first-chunk invalid SSE error returns application/json not text/event-stream", func(t *testing.T) {
		f := newProviderGeminiHandlerFixture(t)
		f.responder = func(*http.Request, int64) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("data: not-valid-json\n\n")),
			}, nil
		}
		w, handled := f.call("/v1beta/models/public-gemini:streamGenerateContent", reqBody, nil)
		require.True(t, handled)
		require.Equal(t, http.StatusBadGateway, w.Code, w.Body.String())
		require.Contains(t, w.Header().Get("Content-Type"), "application/json")
		require.NotContains(t, w.Header().Get("Content-Type"), "text/event-stream")
		require.Equal(t, int64(502), gjson.GetBytes(w.Body.Bytes(), "error.code").Int())
		f.slots.assertReleased(t)
	})

	t.Run("stream mid-stream disconnect emits Gemini SSE error frame", func(t *testing.T) {
		f := newProviderGeminiHandlerFixture(t)
		f.responder = func(*http.Request, int64) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: &failingReadCloser{
					first: []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"partial\"}]}}]}\n\n"),
				},
			}, nil
		}
		w, handled := f.call("/v1beta/models/public-gemini:streamGenerateContent", reqBody, nil)
		require.True(t, handled)
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Header().Get("Content-Type"), "text/event-stream")
		require.Contains(t, w.Body.String(), "partial")
		require.Contains(t, w.Body.String(), `data: {"error":{"code":502,"status":"UNAVAILABLE"`)
		f.slots.assertReleased(t)
	})
}
