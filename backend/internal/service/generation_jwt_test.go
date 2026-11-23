package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func taskJWTFixture(t *testing.T) (*NewAPIPluginService, *Account, *GenerationTask, *rsa.PrivateKey) {
	t.Helper()
	s, account, _ := generationPluginFixture(t, "vertex-ai")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	sealed, err := json.Marshal(vertexServiceAccountKey{Type: "service_account", ProjectID: "fixture-project", ClientEmail: "fixture@example.invalid", PrivateKeyID: "test-key", PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})), TokenURI: "http://127.0.0.1/private"})
	require.NoError(t, err)
	account.Credentials["api_key"] = string(sealed)
	task := credentialFixtureTask(t, account)
	task.Model, task.UpstreamModel = "veo-3.0-generate-001", "veo-3.0-generate-001"
	task.Connection = generationAccountConnection(account)
	task.Request = json.RawMessage(`{"model":"veo-3.0-generate-001","prompt":"Ocean","seconds":"8"}`)
	return s, account, task, key
}

func TestGenerationJWTAuthorizationAndIdentity(t *testing.T) {
	s, account, task, private := taskJWTFixture(t)
	var exchanges atomic.Int32
	s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
		exchanges.Add(1)
		require.Equal(t, vertexDefaultTokenURL, req.URL.String())
		require.True(t, HTTPUpstreamPublicHostsOnly(req.Context()))
		require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
		require.NoError(t, req.ParseForm())
		require.Equal(t, "urn:ietf:params:oauth:grant-type:jwt-bearer", req.PostForm.Get("grant_type"))
		token, err := jwt.Parse(req.PostForm.Get("assertion"), func(token *jwt.Token) (any, error) {
			require.Equal(t, jwt.SigningMethodRS256, token.Method)
			return &private.PublicKey, nil
		}, jwt.WithAudience(vertexDefaultTokenURL), jwt.WithIssuer("fixture@example.invalid"), jwt.WithValidMethods([]string{"RS256"}))
		require.NoError(t, err)
		require.True(t, token.Valid)
		require.Equal(t, vertexCloudPlatformScope, token.Claims.(jwt.MapClaims)["scope"])
		return chainResponse(200, `{"access_token":"task-access","token_type":"Bearer","expires_in":3600}`), nil
	}}
	resolved, err := s.prepareTaskCredential(context.Background(), account, task, true)
	require.NoError(t, err)
	require.Nil(t, account.newAPITaskJWTAuth)
	require.Equal(t, "Bearer task-access", resolved.newAPITaskJWTAuth.Header)
	require.NotEmpty(t, task.CredentialBinding.Connection)
	require.NoError(t, s.withGenerationSession(context.Background(), resolved, task, func(session *newAPITaskSession) error {
		value := session.context(task)
		require.Equal(t, "", value["apiKey"])
		require.Equal(t, "Bearer task-access", value["authHeader"])
		require.Equal(t, "fixture-project", value["auth"].(map[string]any)["projectId"])
		require.Empty(t, session.credential().APIKey)
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "PRIVATE KEY")
		return nil
	}))
	_, err = s.prepareTaskCredential(context.Background(), account, task, false)
	require.NoError(t, err)
	require.EqualValues(t, 1, exchanges.Load(), "polling reuses a bounded token cache")
	public, err := json.Marshal(resolved)
	require.NoError(t, err)
	require.NotContains(t, string(public), "task-access")
	replacement := *account
	replacement.Credentials = maps.Clone(account.Credentials)
	replacement.Credentials["api_key"] = strings.Replace(account.GetCredential("api_key"), "fixture-project", "other-project", 1)
	_, err = s.prepareTaskCredential(context.Background(), &replacement, task, false)
	require.ErrorIs(t, err, ErrGenerationCredential)
	require.EqualValues(t, 1, exchanges.Load())
}

func TestGenerationJWTGatewayAndManagedGrantBypass(t *testing.T) {
	s, account, task, _ := taskJWTFixture(t)
	account.Credentials["newapi_upstream_kind"], account.Credentials["api_key"] = "new_api", "gateway-key"
	task.Connection = generationAccountConnection(account)
	resolved, err := s.prepareTaskCredential(context.Background(), account, task, true)
	require.NoError(t, err)
	require.NoError(t, s.withGenerationSession(context.Background(), resolved, task, func(session *newAPITaskSession) error {
		require.Equal(t, "Bearer gateway-key", session.context(task)["authHeader"])
		require.Equal(t, "gateway-key", session.context(task)["apiKey"])
		return nil
	}))
	account.newAPIProviderCredential, err = (&jsplugin.Credential{Plugin: "vertex-ai", Data: json.RawMessage(`{"token":"managed"}`), Headers: map[string]string{"Authorization": "Bearer managed"}}).Line()
	require.NoError(t, err)
	account.Credentials["newapi_upstream_kind"] = "vendor"
	task.Connection = generationAccountConnection(account)
	version, err := s.repo.Get(context.Background(), 1)
	require.NoError(t, err)
	resolved, err = s.prepareTaskJWT(context.Background(), account, version, task, true)
	require.NoError(t, err)
	require.NoError(t, s.withGenerationSession(context.Background(), resolved, task, func(session *newAPITaskSession) error {
		require.Equal(t, "Bearer managed", session.context(task)["authHeader"])
		require.Equal(t, "", session.context(task)["apiKey"])
		return nil
	}))
}

func TestGenerationJWTOriginalVertexTaskAndEmbeddedContent(t *testing.T) {
	s, account, task, _ := taskJWTFixture(t)
	const operation = "projects/fixture-project/locations/us-central1/publishers/google/models/veo-3.0-generate-001/operations/one"
	var exchanges, submissions, queries int
	s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == vertexDefaultTokenURL {
			exchanges++
			return chainResponse(200, `{"access_token":"vertex-access","expires_in":3600}`), nil
		}
		if req.Header.Get("Authorization") != "Bearer vertex-access" || req.Header.Get("X-Goog-User-Project") != "fixture-project" {
			return nil, errors.New("missing task authorization")
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		if strings.Contains(string(body), "PRIVATE KEY") || strings.Contains(string(body), "client_email") {
			return nil, errors.New("private service account reached task HTTP")
		}
		switch {
		case strings.HasSuffix(req.URL.Path, ":predictLongRunning"):
			submissions++
			return chainResponse(200, `{"name":"`+operation+`"}`), nil
		case strings.HasSuffix(req.URL.Path, ":fetchPredictOperation"):
			queries++
			return chainResponse(200, `{"name":"`+operation+`","done":true,"response":{"videos":[{"mimeType":"video/mp4","bytesBase64Encoded":"dmlkZW8="}]}}`), nil
		}
		return nil, errors.New("unexpected Vertex Task operation")
	}}
	ctx := context.Background()
	_, err := s.prepareTaskCredential(ctx, account, task, true)
	require.NoError(t, err)
	task.Usage, err = s.EstimateTask(ctx, account, task)
	require.NoError(t, err)
	require.NoError(t, s.SubmitTask(ctx, account, task))
	require.NotEmpty(t, task.UpstreamID)
	require.NoError(t, s.PollTask(ctx, account, task))
	require.Equal(t, GenerationSucceeded, task.Status)
	require.Equal(t, GenerationDeliveryReady, task.DeliveryStatus)
	resp, err := s.OpenTaskContent(ctx, account, task, http.MethodGet, "video", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	video, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "video", string(video))
	require.Equal(t, 1, exchanges)
	require.Equal(t, 1, submissions)
	require.Equal(t, 2, queries)
}

func TestGenerationJWTSharedExchangeAndCancellation(t *testing.T) {
	s, account, task, _ := taskJWTFixture(t)
	started, resume := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-resume:
			return chainResponse(200, `{"access_token":"shared","expires_in":3600}`), nil
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() {
		copy := *task
		_, err := s.prepareTaskCredential(ctx, account, &copy, true)
		first <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("exchange did not start")
	}
	cancel()
	require.ErrorIs(t, <-first, context.Canceled)
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := *task
			_, err := s.prepareTaskCredential(context.Background(), account, &copy, true)
			errors <- err
		}()
	}
	close(resume)
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, calls.Load())
}

func TestGenerationJWTRejectsUnusableTokensWithoutDisclosure(t *testing.T) {
	s, account, task, _ := taskJWTFixture(t)
	for _, response := range []string{`{"access_token":"private-error","expires_in":0}`, `{"access_token":"line\nbreak","expires_in":3600}`, `{"access_token":"private-error","expires_in":9223372036854775807}`, `{"access_token":"private-error","token_type":"Basic","expires_in":3600}`, strings.Repeat("x", (32<<10)+1)} {
		t.Run(response[:min(len(response), 35)], func(t *testing.T) {
			s.gateway.httpUpstream = &providerChainHTTP{send: func(*http.Request) (*http.Response, error) { return chainResponse(200, response), nil }}
			_, err := s.prepareTaskCredential(context.Background(), account, task, true)
			require.ErrorIs(t, err, errGenerationJWT)
			require.NotContains(t, err.Error(), "private-error")
			require.Empty(t, s.providerTokens.tokens)
		})
	}
	s.gateway.httpUpstream = &providerChainHTTP{send: func(*http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "POST", URL: vertexDefaultTokenURL, Err: errors.New("private assertion")}
	}}
	_, err := s.prepareTaskCredential(context.Background(), account, task, true)
	require.ErrorIs(t, err, errGenerationJWT)
	require.NotContains(t, err.Error(), "private assertion")
	s.gateway.httpUpstream = &providerChainHTTP{send: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("private-error"))}, nil
	}}
	_, err = s.prepareTaskCredential(context.Background(), account, task, true)
	require.ErrorIs(t, err, errGenerationJWT)
}
