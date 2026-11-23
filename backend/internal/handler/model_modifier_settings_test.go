//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/modelmodifier"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type modelModifierHTTPSettings struct {
	service.SettingRepository
	mu  sync.Mutex
	raw *string
	err error
}

func (r *modelModifierHTTPSettings) GetValue(context.Context, string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return "", r.err
	}
	if r.raw == nil {
		return "", service.ErrSettingNotFound
	}
	return *r.raw, nil
}

func (r *modelModifierHTTPSettings) CompareAndSwapSetting(_ context.Context, _ string, previous *string, value string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if (previous == nil) != (r.raw == nil) || previous != nil && *previous != *r.raw {
		return false, nil
	}
	r.raw = &value
	return true, nil
}

func TestModelModifierSettingsHTTPAndUnavailableAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { require.NoError(t, modelmodifier.Configure(nil)) })
	settings := &modelModifierHTTPSettings{}
	plugins, err := service.ProvideNewAPIPluginService(&nativeAdmissionPluginRepo{}, requestHTTPEncryptor{}, &service.OpenAIGatewayService{}, nil, nil, settings, nil)
	require.NoError(t, err)
	defer plugins.Stop()
	h := NewRequestPluginHandler(plugins, nil)
	router := gin.New()
	router.GET("/settings", h.ModelModifiers)
	router.PUT("/settings", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Next()
	}, h.SaveModelModifiers)
	forwarded := false
	router.POST("/v1/embeddings", h.Ingress, func(c *gin.Context) { forwarded = true; c.Status(http.StatusNoContent) })
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	w := request("GET", "/settings", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
	var initial struct {
		Data service.ModelModifierSettings `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &initial))
	input := service.ModelModifierSettings{Exemptions: []string{"real@effort:high"}, Revision: initial.Data.Revision}
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	w = request("PUT", "/settings", string(raw))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "real@effort:high", modelmodifier.Base("real@effort:high"))
	w = request("PUT", "/settings", string(raw))
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	settings.mu.Lock()
	settings.err = errors.New("private database failure")
	settings.mu.Unlock()
	// Other nodes may retain a valid policy for at most the documented TTL.
	// Once expired, even protocols without Transform hooks must fail closed.
	time.Sleep(2100 * time.Millisecond)
	w = request("POST", "/v1/embeddings", `{"model":"public-model","input":"hello"}`)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "model_policy_unavailable")
	require.NotContains(t, w.Body.String(), "private database failure")
	require.False(t, forwarded)
}
