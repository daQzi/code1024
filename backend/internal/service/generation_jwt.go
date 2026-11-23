// SPDX-License-Identifier: AGPL-3.0-or-later
// Task oauth2_jwt follows QuantumNous/new-api at
// 48f9df96ea7c68eae04a0f3687869f232f48929f, relay/channel/task/jsplugin/auth.go.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/golang-jwt/jwt/v5"
)

type newAPITaskJWTAuth struct {
	Header    string
	ProjectID string
}

var errGenerationJWT = errors.New("task service account authorization failed")

// Resolve before taking a JS worker. The bounded shared exchange survives an
// individual caller cancelling, but has its own memory and network deadline.
// The result is request-local and cannot appear in account JSON or task rows.
func (s *NewAPIPluginService) prepareTaskJWT(ctx context.Context, account *Account, version *NewAPIPluginVersion, task *GenerationTask, bind bool) (*Account, error) {
	if version.Meta.Auth.Type != "oauth2_jwt" || account.GetCredential("newapi_upstream_kind") == "new_api" || account.newAPIProviderCredential != "" {
		return account, nil
	}
	if s.gateway == nil || s.gateway.httpUpstream == nil || len(account.GetCredential("api_key")) > jsplugin.MaxCredentialBytes {
		return nil, errGenerationJWT
	}
	connection := pluginCredentialConnection(account)
	if !bind && !samePluginCredentialConnection(task.CredentialBinding.Connection, connection) {
		return nil, ErrGenerationCredential
	}
	if bind {
		binding := GenerationCredentialBinding{Mode: "static", Connection: connection}
		if plan, _ := ctx.Value(generationVideoPlanKey{}).(*generationVideoPlan); plan != nil && len(plan.origins) > 0 && !SameGenerationCredential(plan.credential, binding) {
			return nil, ErrGenerationOrigin
		}
		task.CredentialBinding = binding
	}
	key, err := parseVertexServiceAccountJSON([]byte(account.GetCredential("api_key")))
	if err != nil || key.Type != "" && key.Type != "service_account" || len(key.ProjectID) > 256 || len(key.ClientEmail) > 320 || len(key.PrivateKeyID) > 256 {
		return nil, errGenerationJWT
	}
	// Do not trust token_uri or the native Vertex test override. Task grants
	// always use Google's fixed endpoint, the account proxy and public-only I/O.
	key.TokenURI = vertexDefaultTokenURL
	account, err = s.providerCredentialAccount(ctx, account)
	if err != nil {
		return nil, errGenerationJWT
	}
	config := json.RawMessage(strconv.Quote(task.ConfigEncrypted))
	cacheKey := "task-oauth2-jwt:" + newAPIProviderTokenKey(version, account, task.Connection.BaseURL, config)
	raw, err := s.providerTokens.get(ctx, cacheKey, func(exchangeCtx context.Context) (jsplugin.ExchangedToken, error) {
		return s.exchangeTaskJWT(exchangeCtx, account, key)
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errGenerationJWT
	}
	var auth newAPITaskJWTAuth
	if json.Unmarshal(raw, &auth) != nil || auth.ProjectID != key.ProjectID || auth.Header == "" {
		return nil, errGenerationJWT
	}
	copy := *account
	copy.newAPITaskJWTAuth = &auth
	return &copy, nil
}

func (s *NewAPIPluginService) exchangeTaskJWT(ctx context.Context, account *Account, key *vertexServiceAccountKey) (jsplugin.ExchangedToken, error) {
	fail := func() (jsplugin.ExchangedToken, error) { return jsplugin.ExchangedToken{}, errGenerationJWT }
	ctx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer release()
	if err := pkghttputil.ReserveRequestBodyMemory(ctx, newAPIProviderMemory); err != nil {
		return fail()
	}
	if _, err := s.gateway.validateOutboundURL(vertexDefaultTokenURL); err != nil {
		return fail()
	}
	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(key.PrivateKey))
	if err != nil || privateKey.N.BitLen() < 2048 || privateKey.N.BitLen() > 8192 {
		return fail()
	}
	now := time.Now()
	assertion := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": key.ClientEmail, "scope": vertexCloudPlatformScope, "aud": vertexDefaultTokenURL,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	if key.PrivateKeyID != "" {
		assertion.Header["kid"] = key.PrivateKeyID
	}
	signed, err := assertion.SignedString(privateKey)
	if err != nil {
		return fail()
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {signed}}
	requestCtx := WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamPublicHostsOnly(ctx))
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, vertexDefaultTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fail()
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.gateway.httpUpstream.Do(req, vertexServiceAccountProxyURL(account), account.ID, account.Concurrency)
	if err != nil {
		return fail()
	}
	defer func() { _ = resp.Body.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = resp.Body.Close() })
	defer stop()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fail()
	}
	const limit = 32 << 10
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	var token vertexTokenResponse
	if err != nil || len(body) > limit || json.Unmarshal(body, &token) != nil || token.AccessToken == "" || len(token.AccessToken) > 16<<10 || token.ExpiresIn <= 0 || token.ExpiresIn > 86400 || token.TokenType != "" && !strings.EqualFold(token.TokenType, "Bearer") {
		return fail()
	}
	auth := newAPITaskJWTAuth{Header: "Bearer " + token.AccessToken, ProjectID: key.ProjectID}
	if jsplugin.ValidateRequestHeaders(map[string]string{"Authorization": auth.Header}) != nil {
		return fail()
	}
	raw, err := json.Marshal(auth)
	if err != nil {
		return fail()
	}
	// Retain the pinned host's 25-minute ceiling while respecting shorter real
	// expirations. The shared cache also applies its normal refresh margin.
	ttl := min(time.Duration(token.ExpiresIn)*time.Second, 25*time.Minute)
	return jsplugin.ExchangedToken{Token: raw, ExpiresAt: now.Add(ttl).Unix()}, nil
}
