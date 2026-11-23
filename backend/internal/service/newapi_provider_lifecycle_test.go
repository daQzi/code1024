package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type credentialFixtureRepo struct {
	*requestPluginFixtureRepo
	mu        sync.Mutex
	state     *PluginCredentialState
	account   *Account
	fail      bool
	conflicts chan struct{}
}

func cloneCredentialState(state *PluginCredentialState) *PluginCredentialState {
	if state == nil {
		return nil
	}
	copy := *state
	copy.Connection = append(json.RawMessage(nil), state.Connection...)
	copy.Public = append(json.RawMessage(nil), state.Public...)
	return &copy
}
func (r *credentialFixtureRepo) ReadPluginCredential(_ context.Context, id int64) (*PluginCredentialState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == nil || r.state.AccountID != id {
		return nil, nil
	}
	return cloneCredentialState(r.state), nil
}
func (r *credentialFixtureRepo) ClaimPluginCredential(_ context.Context, expected *PluginCredentialState, revision int64) (*PluginCredentialState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.account.ID != expected.AccountID || !samePluginCredentialConnection(expected.Connection, pluginCredentialConnection(r.account)) {
		return nil, ErrPluginCredentialConflict
	}
	if r.state == nil || !samePluginCredentialConnection(r.state.Connection, expected.Connection) {
		r.state = cloneCredentialState(expected)
		r.state.Revision = 1
	}
	if r.state.LeaseToken != "" || revision != 0 && revision != r.state.Revision || expected.OnlyIfDue && r.state.NextRunAt.After(time.Now()) {
		if r.conflicts != nil {
			select {
			case r.conflicts <- struct{}{}:
			default:
			}
		}
		return nil, ErrPluginCredentialConflict
	}
	r.state.LeaseToken = uuid.NewString()
	return cloneCredentialState(r.state), nil
}
func (r *credentialFixtureRepo) FinishPluginCredential(_ context.Context, state *PluginCredentialState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail || r.state == nil || state.LeaseToken == "" || r.state.LeaseToken != state.LeaseToken || !samePluginCredentialConnection(state.Connection, pluginCredentialConnection(r.account)) {
		return ErrPluginCredentialConflict
	}
	r.state = cloneCredentialState(state)
	r.state.Revision++
	r.state.LeaseToken = ""
	return nil
}
func (r *credentialFixtureRepo) ReleasePluginCredential(_ context.Context, state *PluginCredentialState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != nil && r.state.LeaseToken == state.LeaseToken {
		r.state.LeaseToken = ""
	}
	return nil
}
func (r *credentialFixtureRepo) DuePluginCredentials(context.Context) ([]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == nil || !r.state.NextRunAt.After(time.Now()) {
		return []int64{r.account.ID}, nil
	}
	return nil, nil
}

func (r *credentialFixtureRepo) PluginCredentialInUse(_ context.Context, _ int64, account string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == nil {
		return false, nil
	}
	var view PluginCredentialView
	if json.Unmarshal(r.state.Public, &view) != nil {
		return false, nil
	}
	return view.Credential != nil && view.Credential.AccountID == account, nil
}

const lifecycleHooks = `
export function login(ctx,input){
 if(!ctx.login.codeVerifier || !ctx.login.nonce || ctx.login.codeChallengeMethod!=="S256" || ctx.config.label!=="saved")throw Error("missing host context");
 if(!input)return {type:"fields",fields:[{name:"password",label:"Password",type:"password",required:true}],session:{private:"opaque-login-secret"}};
 if(ctx.login.session.private!=="opaque-login-secret" || input.fields.password!=="fixture-password")throw Error("bad login secret");
 return {type:"done",credential:{token:"grant-1",refresh:"refresh-secret"},headers:{Authorization:"Bearer grant-1"},query:{grant:"grant-1"},accountId:"upstream-identity",label:"Fixture account",expiresAt:Math.floor(Date.now()/1000)+3600};
}
export function refresh(ctx){
 if(ctx.apiKey!=="" || !ctx.credential.refresh)throw Error("wrong refresh context");
 const resp=utils.fetch({url:ctx.baseUrl+"/refresh",method:"POST",body:JSON.stringify(ctx.credential)});
 if(resp.status!==200)throw Error("private-refresh-secret");
 return resp.body;
}
export function revoke(ctx){if(ctx.apiKey!=="" || !ctx.credential)throw Error("bad revoke context"); const r=utils.fetch({url:ctx.baseUrl+"/revoke",method:"POST",body:JSON.stringify(ctx.credential)});if(r.status!==200)throw Error("private-revoke-secret");}
export function scheduled(ctx){
 if(ctx.apiKey!=="" || !ctx.credential)throw Error("bad maintenance context");
 return {next:120,balance:{amount:7,currency:"USD"},notify:{en:"Maintenance completed"}};
}`

func lifecycleFixture(t *testing.T, source string) (*NewAPIPluginService, *Account, *credentialFixtureRepo) {
	t.Helper()
	s, account, _ := providerBalanceFixture(t, source)
	plain, err := s.encryptor.Decrypt(account.GetCredential(newAPIPluginConfigEncryptedKey))
	require.NoError(t, err)
	s.encryptor = &liveAttestationAES{key: sha256.Sum256([]byte("fixture-only-credential-encryption"))}
	account.Credentials[newAPIPluginConfigEncryptedKey], err = s.encryptor.Encrypt(plain)
	require.NoError(t, err)
	s.gateway.cfg.Server.FrontendURL = "https://gateway.example.invalid"
	r := &credentialFixtureRepo{requestPluginFixtureRepo: s.repo.(*requestPluginFixtureRepo), account: account}
	s.repo = r
	return s, account, r
}

func fixtureLogin(t *testing.T, s *NewAPIPluginService, account *Account) *PluginCredentialView {
	t.Helper()
	view, err := s.ManagePluginCredential(context.Background(), account.ID, 0, "admin-1", "session-digest", "login", PluginLoginInput{})
	require.NoError(t, err)
	require.NotNil(t, view.Login)
	view, err = s.ManagePluginCredential(context.Background(), account.ID, view.Revision, "admin-1", "session-digest", "continue", PluginLoginInput{Flow: view.Login.Flow, Fields: map[string]string{"password": "fixture-password"}})
	require.NoError(t, err)
	require.Equal(t, "active", view.Status)
	return view
}

func TestPluginCredentialLoginIsolationEncryptionAndRestart(t *testing.T) {
	s, account, repo := lifecycleFixture(t, providerFixtureSource+lifecycleHooks)
	delete(account.Credentials, "api_key")
	ctx := context.Background()
	view, err := s.PluginCredentialStatus(ctx, account.ID, "admin-1", "session-digest")
	require.NoError(t, err)
	require.Equal(t, "login_required", view.Status)
	_, err = s.loadProviderLoginCredential(ctx, account, repo.versions[1], account.GetCredential("base_url"), "")
	require.Error(t, err)
	view, err = s.ManagePluginCredential(ctx, account.ID, 0, "admin-1", "session-digest", "login", PluginLoginInput{})
	require.NoError(t, err)
	require.Equal(t, "fields", view.Login.Type)
	require.Equal(t, "password", view.Login.Fields[0].Type)
	for _, identity := range [][2]string{{"admin-2", "session-digest"}, {"admin-1", "other-session"}} {
		hidden, err := s.PluginCredentialStatus(ctx, account.ID, identity[0], identity[1])
		require.NoError(t, err)
		require.Nil(t, hidden.Login)
		_, err = s.ManagePluginCredential(ctx, account.ID, view.Revision, identity[0], identity[1], "continue", PluginLoginInput{Flow: view.Login.Flow, Fields: map[string]string{"password": "fixture-password"}})
		require.Error(t, err)
	}
	_, err = s.ManagePluginCredential(ctx, account.ID, view.Revision, "admin-1", "session-digest", "continue", PluginLoginInput{Flow: "wrong", Fields: map[string]string{"password": "fixture-password"}})
	require.Error(t, err)
	_, err = s.ManagePluginCredential(ctx, account.ID, view.Revision, "admin-1", "session-digest", "continue", PluginLoginInput{Flow: view.Login.Flow, Fields: map[string]string{"unknown": "secret"}})
	require.Error(t, err)
	// Invalid hook/input errors persist only a fixed diagnostic, consuming CAS.
	view, err = s.PluginCredentialStatus(ctx, account.ID, "admin-1", "session-digest")
	require.NoError(t, err)
	view, err = s.ManagePluginCredential(ctx, account.ID, view.Revision, "admin-1", "session-digest", "continue", PluginLoginInput{Flow: view.Login.Flow, Fields: map[string]string{"password": "fixture-password"}})
	require.NoError(t, err)
	require.Nil(t, view.Login)
	require.Equal(t, "upstream-identity", view.Credential.AccountID)
	state, err := repo.ReadPluginCredential(ctx, account.ID)
	require.NoError(t, err)
	for _, secret := range []string{"fixture-password", "refresh-secret", "grant-1", "opaque-login-secret"} {
		require.NotContains(t, state.Encrypted, secret)
		require.NotContains(t, string(state.Public), secret)
	}
	resolved, err := s.loadProviderLoginCredential(ctx, account, repo.versions[1], account.GetCredential("base_url"), "")
	require.NoError(t, err)
	require.NotEmpty(t, resolved.newAPIProviderCredential)
	encoded, err := json.Marshal(resolved)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "grant-1", "decrypted credentials cannot enter account DTO/cache JSON")
	// Fresh service has no in-memory token or login flow cache.
	restarted := &NewAPIPluginService{repo: repo, encryptor: s.encryptor, pool: s.pool, gateway: s.gateway}
	read, err := restarted.PluginCredentialStatus(ctx, account.ID, "admin-1", "session-digest")
	require.NoError(t, err)
	require.Equal(t, view.Revision, read.Revision)
	require.Equal(t, view.Credential, read.Credential)
	// Metadata saves use real randomized encryption and must keep identity.
	connection := pluginCredentialConnection(account)
	require.NoError(t, s.NormalizeAccountBinding(ctx, account, map[string]any{}))
	require.JSONEq(t, string(connection), string(pluginCredentialConnection(account)))
	account.Credentials["base_url"] = "https://different.example.invalid"
	read, err = restarted.PluginCredentialStatus(ctx, account.ID, "admin-1", "session-digest")
	require.NoError(t, err)
	require.Equal(t, "login_required", read.Status)
}

func TestPluginCredentialRefreshSingleFlightAndCurrentAuth(t *testing.T) {
	source := providerFixtureSource + lifecycleHooks
	source = strings.Replace(source, `return {headers:{"X-Body-Digest":req.bodySha256,"X-Config":ctx.config.label},query:{signed:"yes"}}`, `if(ctx.apiKey!=="" || !ctx.credential)throw Error("wrong signing credential");return {headers:{"X-Grant":ctx.credential.token}}`, 1)
	s, account, repo := lifecycleFixture(t, source)
	fixtureLogin(t, s, account)
	var refreshes atomic.Int32
	started, allow := make(chan struct{}), make(chan struct{})
	s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/prefix/refresh", req.URL.Path)
		if refreshes.Add(1) == 1 {
			close(started)
		}
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-allow:
		}
		return chainResponse(200, `{"credential":{"token":"grant-2","refresh":"refresh-secret"},"headers":{"Authorization":"Bearer grant-2"},"query":{"grant":"grant-2"},"expiresAt":0}`), nil
	}}
	initial, err := s.loadProviderLoginCredential(context.Background(), account, repo.versions[1], account.GetCredential("base_url"), "")
	require.NoError(t, err)
	results := make(chan *Account, 6)
	errorsCh := make(chan error, 6)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			resolved, err := s.loadProviderLoginCredential(context.Background(), account, repo.versions[1], account.GetCredential("base_url"), initial.newAPIProviderCredential)
			results <- resolved
			errorsCh <- err
		})
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not start")
	}
	close(allow)
	wg.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		require.NoError(t, err)
	}
	for resolved := range results {
		require.Contains(t, resolved.newAPIProviderCredential, "grant-2")
	}
	require.EqualValues(t, 1, refreshes.Load())
	exchange, err := s.buildProviderRequest(context.Background(), account, newAPIProviderChat, "vendor-text", []byte(`{"model":"vendor-text","messages":[]}`))
	require.NoError(t, err)
	require.Equal(t, "Bearer grant-2", exchange.request.Header.Get("Authorization"))
	require.Empty(t, exchange.request.Header.Get("X-Vendor-Key"))
	require.Equal(t, "grant-2", exchange.request.Header.Get("X-Grant"))
	require.Equal(t, "grant-2", exchange.request.URL.Query().Get("grant"))
}

func TestPluginCredentialMaintenanceAndRevokeRetry(t *testing.T) {
	s, account, repo := lifecycleFixture(t, providerFixtureSource+lifecycleHooks)
	view := fixtureLogin(t, s, account)
	view, err := s.ManagePluginCredential(context.Background(), account.ID, view.Revision, "admin-1", "session-digest", "scheduled", PluginLoginInput{})
	require.NoError(t, err)
	require.Equal(t, 7.0, *view.Balance.Amount)
	require.Equal(t, "Maintenance completed", view.Notify["en"])
	state, err := repo.ReadPluginCredential(context.Background(), account.ID)
	require.NoError(t, err)
	require.InDelta(t, 120, time.Until(state.NextRunAt).Seconds(), 3)
	var calls atomic.Int32
	s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/prefix/revoke", req.URL.Path)
		if calls.Add(1) == 1 {
			return chainResponse(500, `{"error":"private-revoke-secret"}`), nil
		}
		return chainResponse(200, `{}`), nil
	}}
	view, err = s.ManagePluginCredential(context.Background(), account.ID, view.Revision, "admin-1", "session-digest", "revoke", PluginLoginInput{})
	require.NoError(t, err)
	require.Equal(t, "revoke_pending", view.Status)
	_, err = s.loadProviderLoginCredential(context.Background(), account, repo.versions[1], account.GetCredential("base_url"), "")
	require.Error(t, err, "failed remote revocation immediately denies new requests")
	view, err = s.ManagePluginCredential(context.Background(), account.ID, view.Revision, "admin-1", "session-digest", "revoke", PluginLoginInput{})
	require.NoError(t, err)

	require.Equal(t, "revoked", view.Status)
	require.Nil(t, view.Credential)
	_, err = s.ManagePluginCredential(context.Background(), account.ID, view.Revision, "admin-1", "session-digest", "refresh", PluginLoginInput{})
	require.Error(t, err)
}

func TestPluginCredentialCallbackAndPollingValidation(t *testing.T) {
	flow := &pluginLoginFlow{State: "host-state", Issuer: "https://issuer.example", View: PluginLoginView{Type: "paste"}}
	for _, callback := range []string{"javascript:alert(1)", "https://cb/?code=a&state=other&iss=https%3A%2F%2Fissuer.example", "https://cb/?code=a&code=b", "https://cb/?code=a&state=host-state", "https://cb/?error=denied&iss=https%3A%2F%2Fissuer.example", "https://user:pass@cb/?code=a", "https://cb/?code=a#access_token=secret"} {
		_, err := pluginLoginStepInput(flow, PluginLoginInput{CallbackURL: callback})
		require.Error(t, err, callback)
	}
	for _, callback := range []string{"https://cb/?code=a&iss=https%3A%2F%2Fissuer.example", "https://cb/?code=a&state=host-state&iss=https%3A%2F%2Fissuer.example"} {
		_, err := pluginLoginStepInput(flow, PluginLoginInput{CallbackURL: callback})
		require.NoError(t, err)
	}
	for _, kind := range []string{"device", "pending"} {
		flow.View.Type, flow.NextPollAt = kind, time.Now().Add(time.Minute)
		_, err := pluginLoginStepInput(flow, PluginLoginInput{Poll: true})
		require.Error(t, err)
		flow.NextPollAt = time.Now().Add(-time.Second)
		_, err = pluginLoginStepInput(flow, PluginLoginInput{Poll: true})
		require.NoError(t, err)
	}
}

func TestPluginCredentialAdmissionFailureAndNoRefreshHook(t *testing.T) {
	for _, scenario := range []string{"memory", "account-busy", "no-refresh-hook", "still-valid-refresh-failure", "expired-refresh-failure", "relogin"} {
		t.Run(scenario, func(t *testing.T) {
			source := providerFixtureSource + lifecycleHooks
			if scenario == "no-refresh-hook" {
				source = strings.Replace(source, "export function refresh(", "function unusedRefresh(", 1)
			}
			s, account, repo := lifecycleFixture(t, source)
			ctx := context.Background()
			if scenario == "memory" {
				ctx, release := pkghttputil.WithRequestBodyMemoryBudget(ctx, pkghttputil.NewBodyMemoryBudget(1))
				defer release()
				_, err := s.ManagePluginCredential(ctx, account.ID, 0, "admin-1", "session-digest", "login", PluginLoginInput{})
				require.ErrorIs(t, err, pkghttputil.ErrRequestBodyMemoryBudget)
				require.Nil(t, repo.state)
				return
			}
			if scenario == "account-busy" {
				s.gateway.concurrencyService = NewConcurrencyService(&stubConcurrencyCacheForTest{acquireResult: false})
				_, err := s.ManagePluginCredential(ctx, account.ID, 0, "admin-1", "session-digest", "login", PluginLoginInput{})
				require.ErrorIs(t, err, ErrPluginCredentialConflict)
				require.Nil(t, repo.state)
				return
			}
			fixtureLogin(t, s, account)
			state, _ := repo.ReadPluginCredential(ctx, account.ID)
			payload, err := s.readCredentialPayload(state)
			require.NoError(t, err)
			payload.Credential.ExpiresAt = time.Now().Add(20 * time.Second).Unix()
			if scenario == "no-refresh-hook" || scenario == "expired-refresh-failure" {
				payload.Credential.ExpiresAt = time.Now().Add(-time.Second).Unix()
			}
			state, err = repo.ClaimPluginCredential(ctx, state, state.Revision)
			require.NoError(t, err)
			require.NoError(t, s.saveCredentialPayload(ctx, state, &payload))
			var refreshes int
			s.gateway.httpUpstream = &providerChainHTTP{send: func(*http.Request) (*http.Response, error) {
				refreshes++
				if scenario == "relogin" {
					return chainResponse(200, `{"relogin":true}`), nil
				}
				return chainResponse(500, `{"private":"refresh-secret"}`), nil
			}}
			resolved, err := s.loadProviderLoginCredential(ctx, account, repo.versions[1], account.GetCredential("base_url"), "")
			if scenario == "still-valid-refresh-failure" {
				require.NoError(t, err)
				require.NotNil(t, resolved)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "refresh-secret")
			}
			if scenario == "no-refresh-hook" {
				require.Zero(t, refreshes)
			} else {
				require.Equal(t, 1, refreshes)
			}
			view, err := s.PluginCredentialStatus(ctx, account.ID, "admin-1", "session-digest")
			require.NoError(t, err)
			if scenario == "relogin" {
				require.Equal(t, "login_required", view.Status)
			} else if strings.Contains(scenario, "failure") {
				require.Equal(t, "refresh_failed", view.LastError)
			}
		})
	}
}

func TestPluginCredentialRefreshIdentityAndPrivateProjection(t *testing.T) {
	payload := &pluginCredentialPayload{Credential: &jsplugin.Credential{Plugin: "fixture", AccountID: "original", Data: json.RawMessage(`{}`)}}
	err := setPluginCredentialResult(payload, "fixture", jsplugin.CredentialResult{AccountID: "other", Credential: json.RawMessage(`{}`)}, true)
	require.Error(t, err)
	require.Equal(t, "original", payload.Credential.AccountID)
	account := &Account{Platform: "openai", Type: "apikey", Credentials: map[string]any{NewAPIPluginRevisionKey: 1, "api_key": "private-key", "base_url": "https://private-address.invalid", newAPIPluginConfigEncryptedKey: "private-config-cipher"}}
	identity := string(pluginCredentialConnection(account))
	for _, value := range []string{"private-key", "private-address", "private-config"} {
		require.NotContains(t, identity, value)
	}
	require.True(t, json.Valid([]byte(identity)))
}

func TestPluginCredentialKeylessRelayAndManagementCurrentStepRenewal(t *testing.T) {
	for _, entry := range []string{"chat", "balance", "models"} {
		t.Run(entry, func(t *testing.T) {
			source := providerFixtureSource + lifecycleHooks + providerBalanceHooks
			if entry == "models" {
				source = providerModelsSource(false) + lifecycleHooks
			}
			s, account, _ := lifecycleFixture(t, source)
			delete(account.Credentials, "api_key")
			fixtureLogin(t, s, account)
			var paths []string
			var refreshes, polls int
			s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
				paths = append(paths, req.URL.Path)
				if req.URL.Path == "/prefix/refresh" {
					refreshes++
					return chainResponse(200, `{"credential":{"token":"grant-2","refresh":"refresh-secret"},"headers":{"Authorization":"Bearer grant-2"},"query":{"grant":"grant-2"}}`), nil
				}
				require.Empty(t, req.Header.Get("X-Vendor-Key"))
				require.Empty(t, req.URL.Query().Get("catalog_key"))
				require.Equal(t, fmt.Sprintf("Bearer grant-%d", refreshes+1), req.Header.Get("Authorization"))
				require.Equal(t, fmt.Sprintf("grant-%d", refreshes+1), req.URL.Query().Get("grant"))
				if req.URL.Path == "/prefix/balance/start" {
					return chainResponse(200, `{"next":"page2"}`), nil
				}
				polls++
				if polls == 1 {
					return chainResponse(401, `{}`), nil
				}
				switch entry {
				case "chat":
					return chainResponse(200, `{"id":"chat-fixture","object":"chat.completion","model":"vendor-text","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}}`), nil
				case "balance":
					return chainResponse(200, `{"amount":8,"expected":1}`), nil
				default:
					return chainResponse(200, `{"data":[{"id":"catalog-model"}]}`), nil
				}
			}}
			switch entry {
			case "chat":
				body := `{"model":"public-text","messages":[{"role":"user","content":"hello"}]}`
				c, recorder := providerContext(newAPIProviderChat, body)
				result, err := s.gateway.ForwardAsChatCompletions(c.Request.Context(), c, account, []byte(body), "", "")
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, recorder.Code)
				require.Equal(t, 12, result.Usage.InputTokens)
				require.Equal(t, "public-text", result.Model)
			case "balance":
				balance, err := s.QueryNewAPIPluginBalance(context.Background(), account.ID)
				require.NoError(t, err)
				require.Equal(t, 8.0, *balance.Balance.Amount)
				require.Equal(t, []string{"/prefix/balance/start", "/prefix/balance/page2", "/prefix/refresh", "/prefix/balance/page2"}, paths)
			case "models":
				models, err := s.QueryNewAPIPluginModels(context.Background(), account.ID)
				require.NoError(t, err)
				require.Equal(t, []string{"catalog-model"}, models.Models)
			}
			require.Equal(t, 1, refreshes)
			require.Equal(t, 2, polls)
		})
	}
}

func TestPluginCredentialStaticScheduledExchangeAndPausePolicy(t *testing.T) {
	for _, autoDisable := range []bool{false, true} {
		t.Run(fmt.Sprint(autoDisable), func(t *testing.T) {
			source := providerExchangeSource(providerFixtureSource) + `export function scheduled(ctx){if(!ctx.apiKey || ctx.credential || ctx.token.value!=="scheduled-token")throw Error("wrong static maintenance context");return {next:300,keyStatus:"auto_disabled",balance:{amount:0,currency:"USD"}};}`
			s, account, repo := lifecycleFixture(t, source)
			account.Credentials["newapi_plugin_auto_disable"] = autoDisable
			var exchanges int
			s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
				require.Equal(t, "/prefix/token", req.URL.Path)
				exchanges++
				return chainResponse(200, `{"token":{"value":"scheduled-token"}}`), nil
			}}
			view, err := s.ManagePluginCredential(context.Background(), account.ID, 0, "admin-1", "session-digest", "scheduled", PluginLoginInput{})
			require.NoError(t, err)
			require.Equal(t, 1, exchanges)
			if autoDisable {
				require.Equal(t, "auto_disabled", view.Status)
			} else {
				require.Equal(t, "active", view.Status)
			}
			_, err = s.loadProviderLoginCredential(context.Background(), account, repo.versions[1], account.GetCredential("base_url"), "")
			if autoDisable {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPluginCredentialFailedRefreshWaitersNeverReuseRejectedOrExpiredGrant(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprint(expired), func(t *testing.T) {
			s, account, repo := lifecycleFixture(t, providerFixtureSource+lifecycleHooks)
			fixtureLogin(t, s, account)
			ctx := context.Background()
			state, _ := repo.ReadPluginCredential(ctx, account.ID)
			payload, err := s.readCredentialPayload(state)
			require.NoError(t, err)
			if expired {
				payload.Credential.ExpiresAt = time.Now().Add(-time.Second).Unix()
				state, err = repo.ClaimPluginCredential(ctx, state, state.Revision)
				require.NoError(t, err)
				require.NoError(t, s.saveCredentialPayload(ctx, state, &payload))
			}
			used, err := payload.Credential.Line()
			require.NoError(t, err)
			started, finish := make(chan struct{}), make(chan struct{})
			repo.conflicts = make(chan struct{}, 1)
			s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
				close(started)
				select {
				case <-finish:
				case <-req.Context().Done():
					return nil, req.Context().Err()
				}
				return chainResponse(500, `{}`), nil
			}}
			results := make(chan error, 2)
			renew := func() {
				_, err := s.loadProviderLoginCredential(ctx, account, repo.versions[1], account.GetCredential("base_url"), used)
				results <- err
			}
			go renew()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("refresh never started")
			}
			go renew()
			select {
			case <-repo.conflicts:
			case <-time.After(5 * time.Second):
				t.Fatal("second replica never waited")
			}
			close(finish)
			require.Error(t, <-results)
			require.Error(t, <-results)
		})
	}
}
