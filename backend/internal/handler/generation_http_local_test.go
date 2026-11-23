//go:build unit

package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Like the repository's local generation suite, this test is opt-in and runs
// production migrations in a new disposable database, never the supplied DB.
func generationHTTPLocalDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("SUB2API_GENERATION_TEST_DSN")
	if dsn == "" {
		t.Skip("SUB2API_GENERATION_TEST_DSN is not configured")
	}
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var name string
	require.NoError(t, admin.QueryRowContext(ctx, `SELECT current_database()`).Scan(&name))
	require.True(t, strings.HasSuffix(name, "_test"), "local generation tests require a database name ending in _test")
	name = fmt.Sprintf("generation_http_%d_test", time.Now().UnixNano())
	_, err = admin.ExecContext(ctx, `CREATE DATABASE `+name)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = admin.Exec(`DROP DATABASE ` + name + ` WITH (FORCE)`) })
	db, err := sql.Open("postgres", dsn+" dbname="+name)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	return db
}

func TestGenerationLocalHTTPGenericVideoLifecycle(t *testing.T) {
	db := generationHTTPLocalDB(t)
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	var userID, keyID, groupID, accountID, otherAccountID int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance,concurrency) VALUES('video-http@example.invalid','fixture',100,10) RETURNING id`).Scan(&userID))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform,allow_image_generation) VALUES('video-http','openai',true) RETURNING id`).Scan(&groupID))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id,quota) VALUES($1,'video-http-key','video-http',$2,10) RETURNING id`, userID, groupID).Scan(&keyID))
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) VALUES('video-http','openai','apikey','{}',8,1) RETURNING id`).Scan(&accountID))
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) VALUES('other-video-http','openai','apikey','{}',8,0) RETURNING id`).Scan(&otherAccountID))

	var submissions, polls, downloads atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer upstream-fixture-key" {
			http.Error(w, "incorrect upstream credential", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/proxy/v1/videos":
			submissions.Add(1)
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				http.Error(w, "multipart required", 400)
				return
			}
			defer r.MultipartForm.RemoveAll()
			if r.FormValue("model") != "vendor-movie-v2" || r.FormValue("seconds") != "8" {
				http.Error(w, "incorrect mapped request", 400)
				return
			}
			if r.FormValue("prompt") == "ambiguous" {
				_, _ = io.WriteString(w, `{"status":"queued"}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "private-" + r.FormValue("prompt"), "status": "queued", "private": "upstream-only-value"})
		case strings.HasSuffix(r.URL.Path, "/content"):
			downloads.Add(1)
			w.Header().Set("Content-Type", "video/mp4")
			http.ServeContent(w, r, "fixture.mp4", time.Time{}, strings.NewReader("simulated private video bytes"))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/proxy/v1/videos/private-"):
			polls.Add(1)
			switch strings.TrimPrefix(r.URL.Path, "/proxy/v1/videos/private-") {
			case "failed":
				_, _ = io.WriteString(w, `{"status":"failed","seconds":null,"error":{"message":"private upstream failure"}}`)
			case "missing-usage":
				_, _ = io.WriteString(w, `{"status":"completed"}`)
			default:
				_, _ = io.WriteString(w, `{"status":"completed","seconds":"6","private":"upstream-only-value"}`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	upstreamURL, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	// Real HTTP stays on the local fixture. Only the explicitly named fixture
	// host is mapped; an accidental connection change cannot hit the network.
	transport := &grokMediaSlotUpstream{call: func(r *http.Request, _ int64) (*http.Response, error) {
		if r.URL.Host != "video-upstream.example.invalid" {
			return nil, fmt.Errorf("unexpected video fixture host")
		}
		request := r.Clone(r.Context())
		request.URL.Scheme, request.URL.Host = upstreamURL.Scheme, upstreamURL.Host
		return upstream.Client().Do(request)
	}}
	accounts := openAIImagesFailoverAccountRepo{accounts: []service.Account{{
		ID: accountID, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 8, GroupIDs: []int64{groupID},
		Credentials: map[string]any{"base_url": "https://video-upstream.example.invalid/proxy/v1", "api_key": "upstream-fixture-key", "model_mapping": map[string]any{"my-video": "vendor-movie-v2"}, service.NewAPIVideoPricingKey: map[string]any{"my-video": map[string]any{"mode": "fixed", "unit": "second", "unit_price": "0.02"}}},
	}, {}}}
	slots := &grokMediaSlotsCache{accounts: map[string]int64{}, users: map[string]int64{}}
	concurrency := service.NewConcurrencyService(slots)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	pluginsRepo := repository.NewNewAPIPluginRepository(db)
	group := &service.Group{ID: groupID, Platform: service.PlatformOpenAI, RateMultiplier: 1, AllowImageGeneration: true}
	key := &service.APIKey{ID: keyID, UserID: userID, User: &service.User{ID: userID}, GroupID: &groupID, Group: group}
	var versionID, otherVersionID int64
	var generations *service.GenerationService
	var plugins *service.NewAPIPluginService
	var server *httptest.Server
	var billing *service.BillingCacheService
	stop := func() {
		if server != nil {
			server.Close()
			server = nil
		}
		if generations != nil {
			generations.Stop()
			generations = nil
		}
		if plugins != nil {
			plugins.Stop()
			plugins = nil
		}
		if billing != nil {
			billing.Stop()
			billing = nil
		}
	}
	t.Cleanup(stop)
	start := func() {
		gateway := service.NewOpenAIGatewayService(accounts, nil, nil, nil, nil, nil, &grokMediaSlotBindings{}, cfg, nil, concurrency, nil, nil, nil, transport, nil, nil, nil, nil, nil, nil, nil, nil)
		tasks := repository.NewGenerationTaskRepository(db)
		var err error
		plugins, err = service.ProvideNewAPIPluginService(pluginsRepo, requestHTTPEncryptor{}, gateway, nil, tasks, nil, nil)
		require.NoError(t, err)
		if versionID == 0 {
			installed, err := pluginsRepo.List(ctx)
			require.NoError(t, err)
			for _, plugin := range installed {
				if plugin.Key == "sub2api-openai-video" {
					versionID = plugin.ID
				}
				if plugin.Key == "sora" {
					otherVersionID = plugin.ID
				}
			}
			require.Positive(t, versionID)
			require.NoError(t, pluginsRepo.Activate(ctx, versionID, true))
			accounts.accounts[0].Credentials[service.NewAPIPluginRevisionKey] = versionID
			require.NoError(t, plugins.NormalizeAccountBinding(ctx, &accounts.accounts[0], map[string]any{}))
			raw, err := json.Marshal(accounts.accounts[0].Credentials)
			require.NoError(t, err)
			_, err = db.Exec(`UPDATE accounts SET credentials=$2 WHERE id=$1`, accountID, raw)
			require.NoError(t, err)
			_, err = db.Exec(`INSERT INTO account_groups(account_id,group_id) VALUES($1,$2)`, accountID, groupID)
			require.NoError(t, err)
			require.Positive(t, otherVersionID)
			require.NoError(t, pluginsRepo.Activate(ctx, otherVersionID, true))
			other := accounts.accounts[0]
			other.ID, other.Priority = otherAccountID, 0
			other.Credentials = maps.Clone(other.Credentials)
			delete(other.Credentials, "newapi_plugin_config_encrypted")
			other.Credentials[service.NewAPIPluginRevisionKey] = otherVersionID
			other.Credentials["model_mapping"] = map[string]any{"my-video": "sora-2"}
			require.NoError(t, plugins.NormalizeAccountBinding(ctx, &other, map[string]any{}))
			otherRaw, err := json.Marshal(other.Credentials)
			require.NoError(t, err)
			_, err = db.Exec(`UPDATE accounts SET credentials=$2 WHERE id=$1`, otherAccountID, otherRaw)
			require.NoError(t, err)
			_, err = db.Exec(`INSERT INTO account_groups(account_id,group_id) VALUES($1,$2)`, otherAccountID, groupID)
			require.NoError(t, err)
			accounts.accounts[1] = other
			policy, err := plugins.Install(ctx, `export const meta={apiVersion:1,key:"task-http-fixture",name:"Task filter",version:"1.0.0",author:{name:"Fixture"}};
export function ingress(ctx,req) {
 if(ctx.path!=="/v1/tasks/sub2api-openai-video" || ctx.model!=="my-video") return {action:"continue"};
 if(req.body.prompt==="deny") return {action:"reject",code:"blocked"};
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/seconds",value:"8"}]}]};
}`, "", nil)
			require.NoError(t, err)
			require.NoError(t, plugins.Activate(ctx, policy.ID, true))
			_, err = plugins.SaveRequestPluginInstance(ctx, service.RequestPluginInstance{PluginVersionID: policy.ID, Name: "Task HTTP filter", Scope: "global", Formats: []string{"task"}, Stages: []string{"ingress"}, Config: json.RawMessage(`{}`), Enabled: true}, userID)
			require.NoError(t, err)
		}
		generations = service.NewGenerationService(tasks, plugins, accounts, gateway, nil, nil)
		billing = service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
		h := NewOpenAIGatewayHandler(gateway, concurrency, billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
		h.generations = generations
		logs := NewTaskLogHandler(generations)
		router := gin.New()
		// Authentication is supplied by the fixture; handlers, scheduling,
		// plugin workers, HTTP transport and financial repository are real.
		router.Use(func(c *gin.Context) {
			identity := *key
			if c.GetHeader("X-Fixture-Owner") == "foreign" {
				identity.UserID, identity.ID = userID+1000, keyID+1000
			}
			if c.GetHeader("X-Fixture-Owner") == "moved" {
				otherGroup := *group
				otherGroup.ID++
				identity.Group, identity.GroupID = &otherGroup, &otherGroup.ID
			}
			if c.GetHeader("X-Fixture-Owner") == "allowlist" {
				restricted := *group
				restricted.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{"another-model"}}
				identity.Group = &restricted
			}
			c.Set(string(middleware.ContextKeyAPIKey), &identity)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: identity.UserID, Concurrency: 10})
		})
		router.POST("/v1/videos", func(c *gin.Context) {
			if !h.TryPluginVideo(c, service.PlatformOpenAI) {
				c.Status(http.StatusNotImplemented)
			}
		})
		router.GET("/v1/videos", h.GenerationVideoList)
		policy := NewRequestPluginHandler(plugins, generations)
		router.POST(service.GenerationTaskSubmitRoute, h.GenerationTaskSubmitIngress, policy.Ingress, middleware.GroupModelAllowlist(), h.GenerationTaskSubmit)
		router.GET("/v1/tasks/:key", h.GenerationTaskGet)
		router.GET("/v1/videos/:task_id", h.GenerationVideoGet)
		router.GET("/v1/videos/:task_id/content", h.GenerationVideoContent)
		router.HEAD("/v1/videos/:task_id/content", h.GenerationVideoContent)
		router.GET("/task-logs", logs.List)
		router.GET("/task-logs/:task_id", logs.Get)
		server = httptest.NewServer(router)
	}
	start()
	originalCredentials := maps.Clone(accounts.accounts[0].Credentials)
	call := func(t *testing.T, method, path, body, idempotency, owner string) (int, http.Header, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer downstream-fixture-key")
		req.Header.Set("Idempotency-Key", idempotency)
		req.Header.Set("X-Fixture-Owner", owner)
		if strings.HasSuffix(path, "/content") {
			req.Header.Set("Range", "bytes=0-8")
		}
		resp, err := server.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, resp.Header, data
	}
	body := `{"model":"my-video","prompt":"success","seconds":"4","size":"1280x720"}`
	taskSubmitPath := "/v1/tasks/sub2api-openai-video"
	for _, bad := range []string{`{"model":"my-video","model":"another-model"}`, `{"model":"my-video","Model":"another-model"}`, `{"model":"my-video","stream":true}`, `{"model":42}`} {
		status, _, data := call(t, http.MethodPost, taskSubmitPath, bad, "invalid", "")
		require.Equal(t, http.StatusBadRequest, status, string(data))
	}
	status, _, data := call(t, http.MethodPost, taskSubmitPath, strings.Replace(body, "success", "deny", 1), "rejected", "")
	require.Equal(t, http.StatusForbidden, status, string(data))
	status, _, data = call(t, http.MethodPost, taskSubmitPath, body, "disallowed", "allowlist")
	require.Equal(t, http.StatusNotFound, status, string(data))
	status, _, data = call(t, http.MethodPost, "/v1/tasks/no-matching-plugin", body, "unbound", "")
	require.Equal(t, http.StatusServiceUnavailable, status, string(data))
	require.Zero(t, submissions.Load())
	status, headers, data := call(t, http.MethodPost, taskSubmitPath, body, "success-once", "")
	require.Equal(t, 200, status, string(data))
	id := gjson.GetBytes(data, "id").String()
	require.True(t, strings.HasPrefix(id, "video_"))
	require.Equal(t, id, gjson.GetBytes(data, "task_id").String())
	require.Equal(t, "/v1/tasks/"+id, headers.Get("Location"))
	var persistedAccount, persistedVersion int64
	require.NoError(t, db.QueryRow(`SELECT account_id,plugin_version_id FROM sub2api_generation_tasks WHERE id=$1`, id).Scan(&persistedAccount, &persistedVersion))
	require.Equal(t, accountID, persistedAccount, "another matching model account with higher priority cannot intercept this plugin route")
	require.Equal(t, versionID, persistedVersion)
	for range 2 {
		status, _, data = call(t, http.MethodPost, taskSubmitPath, body, "success-once", "")
		require.Equal(t, 200, status, string(data))
		require.Equal(t, id, gjson.GetBytes(data, "id").String())
	}
	for _, path := range []string{"/v1/videos", "/v1/tasks/sora"} {
		status, _, data = call(t, http.MethodPost, path, body, "success-once", "")
		require.Equal(t, http.StatusConflict, status, string(data))
	}
	require.NoError(t, pluginsRepo.Activate(ctx, versionID, false))
	status, _, data = call(t, http.MethodPost, taskSubmitPath, body, "success-once", "")
	require.Equal(t, http.StatusOK, status, string(data))
	require.Equal(t, id, gjson.GetBytes(data, "task_id").String())
	require.NoError(t, pluginsRepo.Activate(ctx, versionID, true))
	status, _, data = call(t, http.MethodPost, taskSubmitPath, body, "success-once", "moved")
	require.Equal(t, http.StatusConflict, status, string(data))
	assertFunds := func(t *testing.T, balance, frozen string, entries int) {
		t.Helper()
		var gotBalance, gotFrozen string
		var count int
		require.NoError(t, db.QueryRow(`SELECT balance::text,frozen_balance::text FROM users WHERE id=$1`, userID).Scan(&gotBalance, &gotFrozen))
		require.InDelta(t, gjson.Parse(balance).Float(), gjson.Parse(gotBalance).Float(), 1e-8)
		require.InDelta(t, gjson.Parse(frozen).Float(), gjson.Parse(gotFrozen).Float(), 1e-8)
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM usage_logs WHERE user_id=$1`, userID).Scan(&count))
		require.Equal(t, entries, count)
	}
	assertFunds(t, "99.84", "0.16", 0)
	require.Eventually(t, func() bool {
		var upstreamID string
		return db.QueryRow(`SELECT upstream_id FROM sub2api_generation_tasks WHERE id=$1`, id).Scan(&upstreamID) == nil && upstreamID != ""
	}, 10*time.Second, 50*time.Millisecond)
	stop()
	// Recover using fresh services, repository instances and JS processes. An
	// account default URL edit cannot replace the accepted task connection.
	accounts.accounts[0].Credentials = maps.Clone(accounts.accounts[0].Credentials)
	accounts.accounts[0].Credentials["base_url"] = "https://changed.example.invalid"
	accounts.accounts[0].Credentials[service.NewAPIVideoPricingKey] = map[string]any{"my-video": map[string]any{"mode": "fixed", "unit": "second", "unit_price": "9"}}
	start()
	waitTask := func(t *testing.T, taskID, state, billingState string) {
		t.Helper()
		require.Eventually(t, func() bool {
			// Only shorten the polling delay. All leases, transitions and billing
			// effects still run through the production durable worker.
			_, err := db.Exec(`UPDATE sub2api_generation_tasks SET next_run_at=NOW() WHERE id=$1 AND lease_until IS NULL AND billing_status<>'settled'`, taskID)
			if err != nil {
				return false
			}
			var gotState, gotBilling string
			return db.QueryRow(`SELECT status,billing_status FROM sub2api_generation_tasks WHERE id=$1`, taskID).Scan(&gotState, &gotBilling) == nil && gotState == state && gotBilling == billingState
		}, 15*time.Second, 100*time.Millisecond)
	}
	waitTask(t, id, service.GenerationSucceeded, service.GenerationBillingSettled)
	assertFunds(t, "99.88", "0", 1)
	var charged float64
	var duration, settlements int
	require.NoError(t, db.QueryRow(`SELECT actual_cost,video_duration_seconds FROM usage_logs WHERE request_id=$1`, id).Scan(&charged, &duration))
	require.InDelta(t, 0.12, charged, 1e-8)
	require.Equal(t, 6, duration)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sub2api_generation_events WHERE task_id=$1 AND event_type='funds_settled'`, id).Scan(&settlements))
	require.Equal(t, 1, settlements)
	require.EqualValues(t, 1, submissions.Load())
	status, _, data = call(t, http.MethodGet, "/v1/videos/"+id, "", "", "")
	require.Equal(t, 200, status, string(data))
	require.Equal(t, "completed", gjson.GetBytes(data, "status").String())
	require.Equal(t, "my-video", gjson.GetBytes(data, "model").String())
	require.NotContains(t, string(data), "private-success")
	status, _, data = call(t, http.MethodGet, "/v1/tasks/"+id, "", "", "")
	require.Equal(t, http.StatusOK, status, string(data))
	require.Equal(t, "SUCCESS", gjson.GetBytes(data, "status").String())
	require.NotContains(t, string(data), "private-success")
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		status, _, data = call(t, method, "/v1/videos/"+id+"/content", "", "", "")
		require.Equal(t, http.StatusPartialContent, status, string(data))
		if method == http.MethodGet {
			require.Equal(t, "simulated", string(data))
		} else {
			require.Empty(t, data)
		}
	}
	require.EqualValues(t, 2, downloads.Load())
	for _, path := range []string{"/v1/videos", "/task-logs", "/task-logs/" + id} {
		before := polls.Load()
		status, headers, data = call(t, http.MethodGet, path, "", "", "")
		require.Equal(t, 200, status, string(data))
		require.Contains(t, string(data), id)
		require.Equal(t, "private, no-store", headers.Get("Cache-Control"))
		for _, secret := range []string{"upstream-fixture-key", "upstream-only-value", "private-success", "vendor-movie-v2"} {
			require.NotContains(t, string(data), secret)
		}
		require.Equal(t, before, polls.Load(), "reading logs does not poll the upstream")
	}
	for _, path := range []string{"/v1/videos/" + id, "/v1/videos/" + id + "/content", "/task-logs/" + id} {
		status, _, data = call(t, http.MethodGet, path, "", "", "foreign")
		require.Equal(t, 404, status, string(data))
	}
	status, _, data = call(t, http.MethodGet, "/task-logs?user_id="+fmt.Sprint(userID), "", "", "foreign")
	require.Equal(t, 200, status, string(data))
	require.NotContains(t, string(data), id, "a user cannot override the log ownership filter")
	status, _, data = call(t, http.MethodPost, taskSubmitPath, body, "success-once", "")
	require.Equal(t, 200, status, string(data))
	require.Equal(t, id, gjson.GetBytes(data, "id").String())
	status, _, data = call(t, http.MethodPost, taskSubmitPath, strings.Replace(body, "success", "changed", 1), "success-once", "")
	require.Equal(t, http.StatusConflict, status, string(data))
	assertFunds(t, "99.88", "0", 1)
	require.EqualValues(t, 1, submissions.Load())
	stop()
	accounts.accounts[0].Credentials = originalCredentials
	accounts.accounts = accounts.accounts[:1] // Existing /v1/videos scenarios exercise the original single-channel setup.
	_, err = db.Exec(`DELETE FROM account_groups WHERE account_id=$1`, otherAccountID)
	require.NoError(t, err)
	start()
	for i, tc := range []struct{ prompt, state, billing string }{
		{"failed", service.GenerationFailed, service.GenerationBillingSettled},
		{"missing-usage", service.GenerationSucceeded, service.GenerationBillingReview},
		{"ambiguous", service.GenerationUncertain, service.GenerationBillingReview},
	} {
		t.Run(tc.prompt, func(t *testing.T) {
			before := submissions.Load()
			body := `{"model":"my-video","prompt":"` + tc.prompt + `","seconds":"8"}`
			status, _, data := call(t, http.MethodPost, "/v1/videos", body, tc.prompt+"-once", "")
			require.Equal(t, 200, status, string(data))
			id := gjson.GetBytes(data, "id").String()
			waitTask(t, id, tc.state, tc.billing)
			require.Equal(t, before+1, submissions.Load())
			status, _, data = call(t, http.MethodPost, "/v1/videos", body, tc.prompt+"-once", "")
			require.Equal(t, 200, status, string(data))
			require.Equal(t, id, gjson.GetBytes(data, "id").String())
			if tc.billing == service.GenerationBillingReview {
				assertFunds(t, "99.72", "0.16", i+1)
				_, err := repository.NewGenerationTaskRepository(db).Claim(ctx, "must-not-repeat-paid-submit", time.Minute)
				require.ErrorIs(t, err, service.ErrGenerationTaskNotFound, "review tasks must not be claimed for automatic resubmission")
				status, _, data = call(t, http.MethodGet, "/task-logs/"+id, "", "", "")
				require.Equal(t, 200, status, string(data))
				require.Equal(t, service.GenerationBillingReview, gjson.GetBytes(data, "data.billing_status").String())
				if tc.state == service.GenerationSucceeded {
					status, _, data = call(t, http.MethodGet, "/v1/videos/"+id, "", "", "")
					require.Equal(t, 200, status, string(data))
					require.Equal(t, "completed", gjson.GetBytes(data, "status").String())
					status, _, data = call(t, http.MethodGet, "/v1/videos/"+id+"/content", "", "", "")
					require.Equal(t, http.StatusPartialContent, status, string(data))
					require.Equal(t, "simulated", string(data))
				}
				// A deliberate operator decision returns the reservation through
				// the same worker and ledger, never by directly editing balances.
				require.NoError(t, generations.Reconcile(ctx, id, userID, "refund", "", "fixture operator confirmed no charge", nil))
				waitTask(t, id, service.GenerationFailed, service.GenerationBillingSettled)
			}
			assertFunds(t, "99.88", "0", i+2)
			require.Equal(t, before+1, submissions.Load())
		})
	}
	stop()
	slots.assertReleased(t)
}

func TestGenerationLocalHTTPSunoTaskLifecycle(t *testing.T) {
	db := generationHTTPLocalDB(t)
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	var userID, keyID, groupID, accountID int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance,concurrency) VALUES('suno-http@example.invalid','fixture',50,10) RETURNING id`).Scan(&userID))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform,rate_multiplier,video_rate_independent,video_rate_multiplier,allow_image_generation) VALUES('suno-http','openai',1,true,5,true) RETURNING id`).Scan(&groupID))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id,quota) VALUES($1,'suno-http-key','suno-http',$2,10) RETURNING id`, userID, groupID).Scan(&keyID))
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) VALUES('suno-http','openai','apikey','{}',8,1) RETURNING id`).Scan(&accountID))

	var submissions, polls, downloads atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/media/song-1.mp3" {
			if r.Header.Get("Authorization") != "" {
				http.Error(w, "credentialless download leaked auth header", http.StatusBadRequest)
				return
			}
			downloads.Add(1)
			w.Header().Set("Content-Type", "audio/mpeg")
			http.ServeContent(w, r, "song.mp3", time.Time{}, strings.NewReader("simulated suno audio bytes"))
			return
		}
		if r.Header.Get("Authorization") != "Bearer suno-fixture-key" {
			http.Error(w, "incorrect upstream credential", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/suno/submit/MUSIC":
			submissions.Add(1)
			raw, _ := io.ReadAll(r.Body)
			prompt := gjson.GetBytes(raw, "gpt_description_prompt").String()
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "success", "message": "", "data": "private-music-" + prompt})
		case r.Method == http.MethodPost && r.URL.Path == "/suno/submit/LYRICS":
			submissions.Add(1)
			raw, _ := io.ReadAll(r.Body)
			prompt := gjson.GetBytes(raw, "prompt").String()
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "success", "message": "", "data": "private-lyrics-" + prompt})
		case r.Method == http.MethodPost && r.URL.Path == "/suno/fetch":
			polls.Add(1)
			raw, _ := io.ReadAll(r.Body)
			id := gjson.GetBytes(raw, "ids.0").String()
			switch {
			case strings.HasPrefix(id, "private-music-fail"):
				_ = json.NewEncoder(w).Encode(map[string]any{"code": "success", "data": []map[string]any{{"task_id": id, "status": "FAILURE", "fail_reason": "private suno rejection"}}})
			case strings.HasPrefix(id, "private-music-"):
				_ = json.NewEncoder(w).Encode(map[string]any{"code": "success", "data": []map[string]any{{"task_id": id, "status": "SUCCESS", "data": []map[string]any{{"id": "song-1", "title": "Ocean", "audio_url": "https://suno-media.example.invalid/media/song-1.mp3"}}}}})
			case strings.HasPrefix(id, "private-lyrics-"):
				_ = json.NewEncoder(w).Encode(map[string]any{"code": "success", "data": []map[string]any{{"task_id": id, "status": "SUCCESS", "data": []map[string]any{{"id": "lyric-1", "title": "Dawn", "text": "Golden sky"}}}}})
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	upstreamURL, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	transport := &grokMediaSlotUpstream{call: func(r *http.Request, _ int64) (*http.Response, error) {
		if r.URL.Host != "suno-upstream.example.invalid" && r.URL.Host != "suno-media.example.invalid" {
			return nil, fmt.Errorf("unexpected suno fixture host %s", r.URL.Host)
		}
		request := r.Clone(r.Context())
		request.URL.Scheme, request.URL.Host = upstreamURL.Scheme, upstreamURL.Host
		return upstream.Client().Do(request)
	}}
	accounts := openAIImagesFailoverAccountRepo{accounts: []service.Account{{
		ID: accountID, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 8, GroupIDs: []int64{groupID},
		Credentials: map[string]any{
			"base_url": "https://suno-upstream.example.invalid",
			"api_key":  "suno-fixture-key",
			service.NewAPIVideoPricingKey: map[string]any{
				"suno_music":  map[string]any{"mode": "expression", "expression": `u("clips") * 0.2`, "maximum": "1"},
				"suno_lyrics": map[string]any{"mode": "expression", "expression": `u("clips") * 0.1`, "maximum": "0.5"},
			},
		},
	}}}
	slots := &grokMediaSlotsCache{accounts: map[string]int64{}, users: map[string]int64{}}
	concurrency := service.NewConcurrencyService(slots)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	pluginsRepo := repository.NewNewAPIPluginRepository(db)
	group := &service.Group{ID: groupID, Platform: service.PlatformOpenAI, RateMultiplier: 1, VideoRateIndependent: true, VideoRateMultiplier: 5, AllowImageGeneration: true}
	key := &service.APIKey{ID: keyID, UserID: userID, User: &service.User{ID: userID}, GroupID: &groupID, Group: group}
	gateway := service.NewOpenAIGatewayService(accounts, nil, nil, nil, nil, nil, &grokMediaSlotBindings{}, cfg, nil, concurrency, nil, nil, nil, transport, nil, nil, nil, nil, nil, nil, nil, nil)
	tasks := repository.NewGenerationTaskRepository(db)
	plugins, err := service.ProvideNewAPIPluginService(pluginsRepo, requestHTTPEncryptor{}, gateway, nil, tasks, nil, nil)
	require.NoError(t, err)
	t.Cleanup(plugins.Stop)
	installed, err := pluginsRepo.List(ctx)
	require.NoError(t, err)
	var sunoVersionID int64
	for _, plugin := range installed {
		if plugin.Key == "sunoapi" {
			sunoVersionID = plugin.ID
			break
		}
	}
	require.Positive(t, sunoVersionID)
	require.NoError(t, pluginsRepo.Activate(ctx, sunoVersionID, true))
	accounts.accounts[0].Credentials[service.NewAPIPluginRevisionKey] = sunoVersionID
	require.NoError(t, plugins.NormalizeAccountBinding(ctx, &accounts.accounts[0], map[string]any{}))
	rawCreds, err := json.Marshal(accounts.accounts[0].Credentials)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE accounts SET credentials=$2 WHERE id=$1`, accountID, rawCreds)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO account_groups(account_id,group_id) VALUES($1,$2)`, accountID, groupID)
	require.NoError(t, err)

	generations := service.NewGenerationService(tasks, plugins, accounts, gateway, nil, nil)
	t.Cleanup(generations.Stop)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	h := NewOpenAIGatewayHandler(gateway, concurrency, billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	h.generations = generations
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), key)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})
	})
	policy := NewRequestPluginHandler(plugins, generations)
	router.POST(service.GenerationTaskSubmitRoute, h.GenerationTaskSubmitIngress, policy.Ingress, middleware.GroupModelAllowlist(), h.GenerationTaskSubmit)
	router.POST("/v1/responses", policy.Ingress, middleware.GroupModelAllowlist(), h.Responses)
	router.GET("/v1/responses/:response_id", h.GenerationResponsesGet)
	router.POST("/v1/tasks/:key/native/*subpath", h.GenerationTaskNativeSubmit)
	router.GET("/v1/tasks/:key/native/*subpath", h.GenerationTaskNativeQuery)
	router.GET("/v1/tasks/:key", h.GenerationTaskGet)
	router.GET("/v1/tasks/:key/artifacts", h.GenerationTaskArtifacts)
	router.GET("/v1/tasks/:key/artifacts/:artifact_key/content", h.GenerationTaskArtifactContent)
	router.HEAD("/v1/tasks/:key/artifacts/:artifact_key/content", h.GenerationTaskArtifactContent)
	router.GET("/v1/videos", h.GenerationVideoList)
	router.GET("/v1/videos/:task_id", h.GenerationVideoGet)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	call := func(t *testing.T, method, path, body, idempotency string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		require.NoError(t, err)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if idempotency != "" {
			req.Header.Set("Idempotency-Key", idempotency)
		}
		resp, err := server.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		out, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, out
	}
	waitTask := func(t *testing.T, id, wantStatus, wantBilling string) {
		t.Helper()
		var lastStatus, lastBilling, lastErr string
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			_, _ = db.Exec(`UPDATE sub2api_generation_tasks SET next_run_at=NOW() WHERE id=$1 AND lease_until IS NULL AND billing_status<>'settled'`, id)
			if err := db.QueryRow(`SELECT status,billing_status,error_code FROM sub2api_generation_tasks WHERE id=$1`, id).Scan(&lastStatus, &lastBilling, &lastErr); err == nil && lastStatus == wantStatus && lastBilling == wantBilling {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("suno task %s did not reach %s/%s (got %s/%s err=%q)", id, wantStatus, wantBilling, lastStatus, lastBilling, lastErr)
	}

	// 1. Music task: submit, batch poll, list artifacts, credentialless audio download, settle 0.2 (non-video rate).
	musicBody := `{"model":"suno_music","input":"ocean"}`
	status, data := call(t, http.MethodPost, "/v1/tasks/sunoapi", musicBody, "music-1")
	require.Equal(t, http.StatusOK, status, string(data))
	musicID := gjson.GetBytes(data, "id").String()
	require.NotEmpty(t, musicID)
	waitTask(t, musicID, service.GenerationSucceeded, service.GenerationBillingSettled)

	status, data = call(t, http.MethodGet, "/v1/tasks/"+musicID+"/artifacts", "", "")
	require.Equal(t, http.StatusOK, status, string(data))
	contentURL := gjson.GetBytes(data, "artifacts.0.content_url").String()
	require.Contains(t, contentURL, "/v1/tasks/"+musicID+"/artifacts/audio-")
	status, data = call(t, http.MethodGet, contentURL, "", "")
	require.Equal(t, http.StatusOK, status, string(data))
	require.Equal(t, "simulated suno audio bytes", string(data))
	status, data = call(t, http.MethodHead, contentURL, "", "")
	require.Equal(t, http.StatusOK, status, string(data))
	require.Empty(t, data)
	require.EqualValues(t, 2, downloads.Load())

	// Suno tasks stay excluded from /v1/videos list and item reads, and TaskLogContent rejects video-only download.
	status, data = call(t, http.MethodGet, "/v1/videos", "", "")
	require.Equal(t, http.StatusOK, status, string(data))
	require.NotContains(t, string(data), musicID)
	status, _ = call(t, http.MethodGet, "/v1/videos/"+musicID, "", "")
	require.Equal(t, http.StatusNotFound, status)
	_, _, err = generations.TaskLogContent(ctx, musicID, userID, false, http.Header{})
	require.Error(t, err)

	var billingMode string
	var videoCount int
	var actualCost float64
	require.NoError(t, db.QueryRow(`SELECT billing_mode,video_count,actual_cost FROM usage_logs WHERE request_id=$1`, musicID).Scan(&billingMode, &videoCount, &actualCost))
	require.Equal(t, "per_request", billingMode)
	require.Zero(t, videoCount)
	require.InDelta(t, 0.2, actualCost, 1e-8)

	// Idempotent replay returns original task without resubmission; modified body conflicts.
	status, data = call(t, http.MethodPost, "/v1/tasks/sunoapi", musicBody, "music-1")
	require.Equal(t, http.StatusOK, status, string(data))
	require.Equal(t, musicID, gjson.GetBytes(data, "id").String())
	status, _ = call(t, http.MethodPost, "/v1/tasks/sunoapi", `{"model":"suno_music","input":"changed"}`, "music-1")
	require.Equal(t, http.StatusConflict, status)

	// 2. Lyrics task: returns sanitized lyrics view, settles 0.1.
	status, data = call(t, http.MethodPost, "/v1/tasks/sunoapi", `{"model":"suno_lyrics","input":"dawn"}`, "lyrics-1")
	require.Equal(t, http.StatusOK, status, string(data))
	lyricsID := gjson.GetBytes(data, "id").String()
	waitTask(t, lyricsID, service.GenerationSucceeded, service.GenerationBillingSettled)
	status, data = call(t, http.MethodGet, "/v1/tasks/"+lyricsID, "", "")
	require.Equal(t, http.StatusOK, status, string(data))
	require.Contains(t, string(data), `"lyrics":[{"title":"Dawn","text":"Golden sky"}]`)
	require.NotContains(t, string(data), "private-lyrics")

	// 3. Failure task refunds reservation cleanly (total spent remains 0.30).
	status, data = call(t, http.MethodPost, "/v1/tasks/sunoapi", `{"model":"suno_music","input":"fail"}`, "fail-1")
	require.Equal(t, http.StatusOK, status, string(data))
	failID := gjson.GetBytes(data, "id").String()
	waitTask(t, failID, service.GenerationFailed, service.GenerationBillingSettled)
	var balance float64
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, userID).Scan(&balance))
	require.InDelta(t, 49.70, balance, 1e-6)
	require.EqualValues(t, 3, submissions.Load())
	require.GreaterOrEqual(t, polls.Load(), int64(3))

	// Exercise the unmodified upstream JS over the public Responses API.
	status, data = call(t, http.MethodPost, "/v1/responses", `{"model":"suno_lyrics","input":"dawn","background":true,"stream":true}`, "")
	require.Equal(t, http.StatusBadRequest, status, string(data))
	require.EqualValues(t, 3, submissions.Load())
	status, data = call(t, http.MethodPost, "/v1/responses", `{"model":"suno_lyrics","input":"dawn","background":true}`, "responses-1")
	require.Equal(t, http.StatusOK, status, string(data))
	responseID := gjson.GetBytes(data, "id").String()
	require.True(t, strings.HasPrefix(responseID, "resp_"), string(data))
	require.True(t, gjson.GetBytes(data, "background").Bool(), string(data))
	responsesTaskID := gjson.GetBytes(data, "metadata.task_id").String()
	require.NotEmpty(t, responsesTaskID)
	waitTask(t, responsesTaskID, service.GenerationSucceeded, service.GenerationBillingSettled)
	status, data = call(t, http.MethodGet, "/v1/responses/"+responseID, "", "")
	require.Equal(t, http.StatusOK, status, string(data))
	require.Equal(t, "completed", gjson.GetBytes(data, "status").String(), string(data))
	require.True(t, gjson.GetBytes(data, "background").Bool(), string(data))
	require.Contains(t, string(data), "Golden sky")

	// The stock native query consumes only this user's public IDs, never vendor IDs.
	status, data = call(t, http.MethodGet, "/v1/tasks/sunoapi/native/suno/fetch/"+responsesTaskID, "", "")
	require.Equal(t, http.StatusOK, status, string(data))
	require.Equal(t, responsesTaskID, gjson.GetBytes(data, "data.task_id").String(), string(data))
	status, data = call(t, http.MethodPost, "/v1/tasks/sunoapi/native/suno/fetch", fmt.Sprintf(`{"ids":[%q]}`, responsesTaskID), "")
	require.Equal(t, http.StatusOK, status, string(data))
	require.Equal(t, responsesTaskID, gjson.GetBytes(data, "data.0.task_id").String(), string(data))
	status, _ = call(t, http.MethodGet, "/v1/tasks/other-plugin/native/suno/fetch/"+responsesTaskID, "", "")
	require.Equal(t, http.StatusNotFound, status)

	status, data = call(t, http.MethodPost, "/v1/responses", `{"model":"suno_lyrics","input":"dawn"}`, "responses-sync")
	require.Equal(t, http.StatusOK, status, string(data))
	require.Equal(t, "completed", gjson.GetBytes(data, "status").String(), string(data))
	require.Contains(t, string(data), "Golden sky")
	waitTask(t, gjson.GetBytes(data, "metadata.task_id").String(), service.GenerationSucceeded, service.GenerationBillingSettled)
	status, data = call(t, http.MethodPost, "/v1/responses", `{"model":"suno_lyrics","input":"dawn","stream":true}`, "responses-stream")
	require.Equal(t, http.StatusOK, status, string(data))
	require.Contains(t, string(data), "event: response.created")
	require.Contains(t, string(data), "event: response.completed")
	require.Contains(t, string(data), "Golden sky")
	createdLine := strings.SplitN(strings.SplitN(string(data), "data: ", 2)[1], "\n", 2)[0]
	waitTask(t, gjson.Get(createdLine, "response.metadata.task_id").String(), service.GenerationSucceeded, service.GenerationBillingSettled)
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, userID).Scan(&balance))
	require.InDelta(t, 49.40, balance, 1e-6)
	require.EqualValues(t, 6, submissions.Load())
	slots.assertReleased(t)
}
