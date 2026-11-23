// SPDX-License-Identifier: AGPL-3.0-or-later
// Credential lifecycle follows QuantumNous/new-api at
// 48f9df96ea7c68eae04a0f3687869f232f48929f, using account-bound host storage.
package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
)

type PluginLoginInput struct {
	Flow        string            `json:"flow,omitempty"`
	CallbackURL string            `json:"callback_url,omitempty"`
	Fields      map[string]string `json:"fields,omitempty"`
	Poll        bool              `json:"poll,omitempty"`
}

type pluginCredentialOperationError struct{ cause error }

func (e *pluginCredentialOperationError) Error() string {
	return "plugin credential operation failed; check the plugin, connection and login step"
}
func (e *pluginCredentialOperationError) Unwrap() error { return e.cause }

// Only this explicit projection is sent to the administrator. PKCE, nonce,
// opaque sessions and credential data remain encrypted on the server.
type PluginLoginView struct {
	Flow                    string                 `json:"flow"`
	Type                    string                 `json:"type"`
	URL                     string                 `json:"url,omitempty"`
	Hint                    jsplugin.LocalizedText `json:"hint,omitempty"`
	UserCode                string                 `json:"user_code,omitempty"`
	VerificationURI         string                 `json:"verification_uri,omitempty"`
	VerificationURIComplete string                 `json:"verification_uri_complete,omitempty"`
	Interval                int                    `json:"interval,omitempty"`
	ExpiresAt               int64                  `json:"expires_at"`
	Fields                  []jsplugin.LoginField  `json:"fields,omitempty"`
}

type PluginCredentialView struct {
	AccountID       int64                       `json:"account_id"`
	PluginVersionID int64                       `json:"plugin_version_id"`
	Revision        int64                       `json:"revision"`
	Status          string                      `json:"status"`
	Credential      *jsplugin.CredentialSummary `json:"credential,omitempty"`
	TaskBinding     string                      `json:"task_binding,omitempty"`
	Login           *PluginLoginView            `json:"login,omitempty"`
	Balance         *jsplugin.BalanceResult     `json:"balance,omitempty"`
	Notify          jsplugin.LocalizedText      `json:"notify,omitempty"`
	LastRunAt       *time.Time                  `json:"last_run_at,omitempty"`
	LastError       string                      `json:"last_error,omitempty"`
}

type pluginLoginFlow struct {
	Actor, Auth, State, Nonce, Verifier, Redirect, Issuer string
	Session                                               json.RawMessage
	NextPollAt                                            time.Time
	View                                                  PluginLoginView
}

type pluginCredentialPayload struct {
	Credential  *jsplugin.Credential
	Login       *pluginLoginFlow
	View        PluginCredentialView
	ScheduledAt time.Time
	CanRefresh  bool
	RetryAt     time.Time
	Generation  int64
	TaskBinding string
}

func samePluginCredentialConnection(a, b json.RawMessage) bool {
	var left, right bytes.Buffer
	return json.Compact(&left, a) == nil && json.Compact(&right, b) == nil && bytes.Equal(left.Bytes(), right.Bytes())
}

func (s *NewAPIPluginService) credentialRepository() (PluginCredentialRepository, error) {
	repo, ok := s.repo.(PluginCredentialRepository)
	if !ok {
		return nil, errors.New("plugin credential persistence unavailable")
	}
	return repo, nil
}

func (s *NewAPIPluginService) readCredentialPayload(state *PluginCredentialState) (pluginCredentialPayload, error) {
	payload := pluginCredentialPayload{View: PluginCredentialView{Status: "uninitialized"}}
	if state == nil || state.Encrypted == "" {
		return payload, nil
	}
	plain, err := s.encryptor.Decrypt(state.Encrypted)
	if err != nil || len(plain) > 128<<10 || json.Unmarshal([]byte(plain), &payload) != nil {
		return payload, errors.New("cannot read plugin credential state")
	}
	return payload, nil
}

func (s *NewAPIPluginService) saveCredentialPayload(ctx context.Context, state *PluginCredentialState, payload *pluginCredentialPayload) error {
	payload.View.Credential, payload.View.Login = nil, nil
	payload.View.TaskBinding = ""
	if payload.Credential != nil {
		summary := payload.Credential.Summary()
		payload.View.Credential = &summary
		payload.View.TaskBinding = payload.TaskBinding
	}
	if payload.Login != nil {
		payload.View.Login = &payload.Login.View
	}
	payload.View.Revision = state.Revision + 1
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) > 128<<10 {
		return errors.New("plugin credential state exceeds limit")
	}
	state.Encrypted, err = s.encryptor.Encrypt(string(raw))
	if err != nil {
		return err
	}
	state.Public, err = json.Marshal(payload.View)
	if err != nil || len(state.Public) > 32768 {
		return errors.New("plugin credential summary exceeds limit")
	}
	state.NextRunAt = time.Now().Add(24 * time.Hour)
	if payload.RetryAt.After(time.Now()) {
		state.NextRunAt = payload.RetryAt
	} else if payload.View.Status == "revoke_pending" {
		state.NextRunAt = time.Now().Add(time.Minute)
	} else if payload.Login != nil {
		state.NextRunAt = time.Unix(payload.Login.View.ExpiresAt, 0)
	} else if payload.View.Status != "revoked" && payload.View.Status != "login_required" {
		if !payload.ScheduledAt.IsZero() {
			state.NextRunAt = payload.ScheduledAt
		}
		if payload.CanRefresh && payload.Credential != nil && payload.Credential.ExpiresAt > 0 {
			refreshAt := time.Unix(payload.Credential.ExpiresAt, 0).Add(-newAPIProviderTokenMargin)
			if refreshAt.Before(time.Now().Add(time.Minute)) {
				refreshAt = time.Now().Add(time.Minute)
			}
			if refreshAt.Before(state.NextRunAt) {
				state.NextRunAt = refreshAt
			}
		}
	}
	repo, err := s.credentialRepository()
	if err != nil {
		return err
	}
	return repo.FinishPluginCredential(ctx, state)
}

func (s *NewAPIPluginService) PluginCredentialStatus(ctx context.Context, accountID int64, actor, auth string) (*PluginCredentialView, error) {
	ctx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer release()
	if err := pkghttputil.ReserveRequestBodyMemory(ctx, newAPIProviderMemory); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	account, version, _, err := s.savedPluginManagementAccount(ctx, accountID, true)
	if err != nil {
		return nil, err
	}
	repo, err := s.credentialRepository()
	if err != nil {
		return nil, err
	}
	state, err := repo.ReadPluginCredential(ctx, account.ID)
	if err != nil {
		return nil, err
	}
	if state == nil || !samePluginCredentialConnection(state.Connection, pluginCredentialConnection(account)) {
		status := "static"
		if slices.Contains(version.Capabilities.Hooks, "login") && account.GetCredential("api_key") == "" {
			status = "login_required"
		}
		return &PluginCredentialView{AccountID: account.ID, PluginVersionID: version.ID, Status: status}, nil
	}
	payload, err := s.readCredentialPayload(state)
	if err != nil {
		return nil, err
	}
	view := payload.View
	view.Revision = state.Revision
	if payload.Login != nil && (!samePluginLoginActor(payload.Login, actor, auth) || time.Now().Unix() >= payload.Login.View.ExpiresAt) {
		view.Login = nil
	}
	return &view, nil
}

// All mutation calls use the same persistent claim, also used by the two
// background workers. A hook holds no SQL transaction while doing real I/O.
func (s *NewAPIPluginService) ManagePluginCredential(ctx context.Context, accountID, revision int64, actor, auth, action string, input PluginLoginInput) (*PluginCredentialView, error) {
	if action != "login" && action != "continue" && action != "refresh" && action != "revoke" && action != "scheduled" {
		return nil, pluginInputError("unsupported plugin credential operation")
	}
	ctx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer release()
	if err := pkghttputil.ReserveRequestBodyMemory(ctx, newAPIProviderMemory); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	account, version, base, err := s.savedPluginManagementAccount(ctx, accountID, true)
	if err != nil {
		return nil, err
	}
	if !pluginCredentialRuntimeSupported(version) {
		return nil, pluginInputError("credential maintenance requires a supported Task or Provider plugin")
	}
	if onlyDue, _ := ctx.Value(pluginCredentialTimerKey{}).(bool); onlyDue && (account.Status != StatusActive && account.Status != StatusError || account.Status == StatusActive && !account.Schedulable) {
		return nil, ErrPluginCredentialConflict
	}
	if s.gateway.concurrencyService == nil {
		return nil, errors.New("plugin account concurrency unavailable")
	}
	slot, err := s.gateway.concurrencyService.AcquireAccountSlot(ctx, account.ID, account.Concurrency)
	if err != nil || slot == nil || !slot.Acquired {
		return nil, ErrPluginCredentialConflict
	}
	defer slot.ReleaseFunc()
	return s.managePluginCredential(ctx, account, version, base, revision, actor, auth, action, input)
}

func (s *NewAPIPluginService) managePluginCredential(ctx context.Context, account *Account, version *NewAPIPluginVersion, base string, revision int64, actor, auth, action string, input PluginLoginInput) (*PluginCredentialView, error) {
	ctx, cancel := context.WithTimeout(ctx, newAPIProviderCredentialTimeout)
	defer cancel()
	repo, err := s.credentialRepository()
	if err != nil {
		return nil, err
	}
	onlyDue, _ := ctx.Value(pluginCredentialTimerKey{}).(bool)
	state, err := repo.ClaimPluginCredential(ctx, &PluginCredentialState{AccountID: account.ID, PluginVersionID: version.ID, Connection: pluginCredentialConnection(account), OnlyIfDue: onlyDue}, revision)
	if err != nil {
		return nil, err
	}
	if onlyDue && account.Status == StatusError && !pluginCredentialNativeDisabled(account, state) {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = repo.ReleasePluginCredential(cleanup, state)
		return nil, ErrPluginCredentialConflict
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = repo.ReleasePluginCredential(cleanup, state)
	}()
	payload, err := s.readCredentialPayload(state)
	if err != nil {
		return nil, err
	}
	payload.View.AccountID, payload.View.PluginVersionID = account.ID, version.ID
	payload.CanRefresh = slices.Contains(version.Capabilities.Hooks, "refresh")
	config := json.RawMessage(`{}`)
	if sealed := account.GetCredential(newAPIPluginConfigEncryptedKey); sealed != "" {
		plain, err := s.encryptor.Decrypt(sealed)
		if err != nil {
			return nil, errors.New("cannot decrypt plugin configuration")
		}
		config = json.RawMessage(plain)
	}
	if action == "continue" && (revision <= 0 || payload.Login == nil || !samePluginLoginActor(payload.Login, actor, auth) || input.Flow != payload.Login.State || time.Now().Unix() >= payload.Login.View.ExpiresAt) {
		return nil, pluginInputError("login flow expired or belongs to another session")
	}
	// Validate and exchange static keys before taking a worker for the actual
	// lifecycle hook. Never wait on the pool while already holding a worker.
	err = s.pool.With(ctx, version.Source, func(client *runner.Client) error {
		var err error
		config, err = client.ValidateConfig(ctx, config, false)
		return err
	})
	if err != nil {
		return nil, errors.New("invalid plugin credential configuration")
	}
	var token json.RawMessage
	if action == "scheduled" && payload.Credential == nil && (account.GetCredential("api_key") != "" || !slices.Contains(version.Capabilities.Hooks, "login")) && slices.Contains(version.Capabilities.Hooks, "exchangeKey") {
		token, err = s.providerTokens.get(ctx, newAPIProviderTokenKey(version, account, base, config), func(hookCtx context.Context) (jsplugin.ExchangedToken, error) {
			return s.exchangeProviderKey(hookCtx, version, account, base, config)
		})
		if err != nil {
			return nil, err
		}
	}
	before, _ := json.Marshal(payload)
	pendingBefore := state.NativeDisablePending
	previousStatus := payload.View.Status
	scheduledNotification := false
	var unstored *jsplugin.CredentialResult
	defer func() {
		if unstored != nil {
			s.discardUnstoredPluginGrant(ctx, account, version, base, config, *unstored)
		}
	}()
	err = s.pool.With(ctx, version.Source, func(client *runner.Client) error {
		var err error
		session := s.providerCredentialSession(client, version, account, base, config)
		credential := providerCredentialContext(account, base, config, token)
		if payload.Credential != nil {
			credential.Credential, credential.APIKey = payload.Credential.Data, ""
		}
		switch action {
		case "login", "continue":
			if !slices.Contains(version.Capabilities.Hooks, "login") || actor == "" || auth == "" {
				return pluginInputError("plugin login is unavailable")
			}
			var values any
			if action == "login" {
				payload.Login, err = s.newPluginLoginFlow(ctx, actor, auth, version.Key)
			} else {
				values, err = pluginLoginStepInput(payload.Login, input)
			}
			if err != nil {
				return err
			}
			flow := payload.Login
			challenge := sha256.Sum256([]byte(flow.Verifier))
			login := jsplugin.LoginContext{State: flow.State, Nonce: flow.Nonce, CodeVerifier: flow.Verifier, CodeChallenge: base64.RawURLEncoding.EncodeToString(challenge[:]), RedirectURI: flow.Redirect, Session: flow.Session, Config: config}
			valuesOut, err := client.Typed(ctx, session.fetch, "CallLogin", base, login, credential.Credential, values)
			if err != nil {
				return err
			}
			if len(valuesOut) != 1 {
				return errors.New("plugin login hook failed")
			}
			var step jsplugin.LoginStep
			if json.Unmarshal(valuesOut[0], &step) != nil {
				return errors.New("invalid plugin login result")
			}
			if step.Type == jsplugin.LoginStepDone {
				if step.Result == nil {
					return errors.New("plugin login did not return a credential")
				}
				unstored = step.Result
				if err := setPluginCredentialResult(&payload, version.Key, *step.Result, false); err != nil {
					return err
				}
				payload.Login = nil
				payload.ScheduledAt = time.Now()
			} else {
				updatePluginLoginFlow(flow, step)
				payload.View.Status = "logging_in"
			}
		case "refresh":
			if payload.Credential == nil || payload.View.Status == "revoked" || payload.View.Status == "revoke_pending" || !slices.Contains(version.Capabilities.Hooks, "refresh") {
				return pluginInputError("no refreshable plugin credential")
			}
			values, err := client.Typed(ctx, session.fetch, "CallRefresh", credential)
			if err != nil {
				return err
			}
			if len(values) != 1 {
				return errors.New("plugin refresh hook failed")
			}
			var result jsplugin.CredentialResult
			if json.Unmarshal(values[0], &result) != nil {
				return errors.New("invalid plugin refresh result")
			}
			if err := setPluginCredentialResult(&payload, version.Key, result, true); err != nil {
				return err
			}
		case "revoke":
			if payload.Credential != nil && slices.Contains(version.Capabilities.Hooks, "revoke") {
				if _, err := client.Typed(ctx, session.fetch, "CallRevoke", credential); err != nil {
					payload.View.Status, payload.View.LastError = "revoke_pending", "upstream_revoke_failed"
					payload.Login = nil
					return nil // Persist local denial before retrying remote revoke.
				}
			}
			payload.Credential, payload.Login = nil, nil
			payload.View.Status = "revoked"
		case "scheduled":
			if payload.Login != nil {
				if time.Now().Unix() >= payload.Login.View.ExpiresAt {
					payload.Login = nil
					if payload.Credential == nil {
						payload.View.Status = "login_required"
					} else {
						payload.View.Status = "active"
					}
				}
				return nil
			}
			if payload.View.Status == "revoked" || payload.View.Status == "revoke_pending" || payload.View.Status == "login_required" {
				return nil
			}
			if payload.Credential == nil && slices.Contains(version.Capabilities.Hooks, "login") && account.GetCredential("api_key") == "" {
				payload.View.Status = "login_required"
				return nil
			}
			if payload.CanRefresh && payload.Credential != nil && payload.Credential.ExpiresWithin(time.Now(), newAPIProviderTokenMargin) {
				values, err := client.Typed(ctx, session.fetch, "CallRefresh", credential)
				if err != nil || len(values) != 1 {
					return errors.New("plugin refresh hook failed")
				}
				var result jsplugin.CredentialResult
				if json.Unmarshal(values[0], &result) != nil {
					return errors.New("invalid plugin refresh result")
				}
				if err := setPluginCredentialResult(&payload, version.Key, result, true); err != nil {
					return err
				}
				if payload.View.Status == "login_required" {
					return nil
				}
				credential.Credential = payload.Credential.Data
			}
			if !slices.Contains(version.Capabilities.Hooks, "scheduled") {
				payload.ScheduledAt = time.Now().Add(24 * time.Hour)
				return nil
			}
			keyStatus := "enabled"
			nativeDisabled := pluginCredentialNativeDisabled(account, state)
			if nativeDisabled || pluginCredentialAutoDisable(account) && payload.View.Status == "auto_disabled" {
				keyStatus = "auto_disabled"
			}
			reason := "timer"
			disabledReason := "plugin_maintenance"
			if nativeDisabled {
				disabledReason = state.NativeDisabledReason
				if state.NativeDisablePending {
					reason = "disabled"
					now := time.Now().UTC()
					state.LastDisabledRunAt = &now
				}
			}
			values, err := client.Typed(ctx, session.fetch, "CallScheduled", jsplugin.ScheduledContext{CredentialContext: credential, Reason: reason, KeyStatus: keyStatus, DisabledReason: disabledReason})
			if err != nil || len(values) != 1 {
				return errors.New("plugin scheduled hook failed")
			}
			var result jsplugin.ScheduledResult
			if json.Unmarshal(values[0], &result) != nil {
				return errors.New("invalid plugin scheduled result")
			}
			state.NativeDisablePending = false
			state.RecoverNative = nativeDisabled && pluginCredentialAutoDisable(account) && result.KeyStatus == "enabled"
			if result.Credential != nil {
				if payload.Credential == nil {
					return errors.New("scheduled cannot replace a static key")
				}
				payload.Credential.Data = result.Credential
				if _, err := payload.Credential.Line(); err != nil {
					return err
				}
				payload.Generation++
			}
			if result.Balance != nil {
				payload.View.Balance = result.Balance
			}
			payload.View.Notify = result.Notify
			if result.KeyStatus == "auto_disabled" && pluginCredentialAutoDisable(account) {
				payload.View.Status = "auto_disabled"
			} else if result.KeyStatus == "enabled" || payload.View.Status == "uninitialized" {
				payload.View.Status = "active"
			}
			payload.ScheduledAt = time.Now().Add(result.Next)
			scheduledNotification = true
		}
		return nil
	})
	if err != nil {
		state.RecoverNative = false
		state.NativeDisablePending = pendingBefore
		// Keep the last committed credential and login session, not partially
		// mutated hook output. Only a fixed diagnostic and retry time are public.
		if json.Unmarshal(before, &payload) == nil {
			now := time.Now().UTC()
			payload.View.LastRunAt, payload.View.LastError = &now, action+"_failed"
			payload.RetryAt = now.Add(15 * time.Minute)
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			_ = s.saveCredentialPayload(cleanup, state, &payload)
			cancel()
		}
		return nil, &pluginCredentialOperationError{cause: err}
	}
	payload.RetryAt = time.Time{}
	now := time.Now().UTC()
	payload.View.LastRunAt = &now
	if payload.View.Status != "revoke_pending" {
		payload.View.LastError = ""
	}
	if err := s.saveCredentialPayload(ctx, state, &payload); err != nil {
		return nil, err
	}
	unstored = nil
	if scheduledNotification && s.credentialNotifier != nil {
		sum := sha256.Sum256([]byte(state.Encrypted))
		noticeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err := s.credentialNotifier.notify(noticeCtx, pluginCredentialNotice{AccountID: account.ID, AccountName: account.Name, PluginKey: version.Key, Identity: hex.EncodeToString(sum[:]), PreviousStatus: previousStatus, Status: payload.View.Status, NativeRecovered: state.RecoverNative, Message: payload.View.Notify})
		cancel()
		if err != nil {
			slog.Warn("plugin_credential_notification_failed", "account_id", account.ID, "plugin_version_id", version.ID)
		}
	}
	return &payload.View, nil
}

// Some vendors revoke every session for an identity. As in the pinned host,
// a failed/duplicate login must not revoke an identity already held by any
// saved plugin account. A failed lookup is treated as in-use.
func (s *NewAPIPluginService) discardUnstoredPluginGrant(ctx context.Context, account *Account, version *NewAPIPluginVersion, base string, config json.RawMessage, grant jsplugin.CredentialResult) {
	if !slices.Contains(version.Capabilities.Hooks, "revoke") || len(grant.Credential) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), newAPIProviderCredentialTimeout)
	defer cancel()
	if grant.AccountID != "" {
		repo, err := s.credentialRepository()
		if err != nil {
			return
		}
		inUse, err := repo.PluginCredentialInUse(ctx, version.ID, grant.AccountID)
		if err != nil || inUse {
			return
		}
	}
	_ = s.pool.With(ctx, version.Source, func(client *runner.Client) error {
		session := s.providerCredentialSession(client, version, account, base, config)
		_, err := client.Typed(ctx, session.fetch, "CallRevoke", jsplugin.CredentialContext{BaseURL: base, Credential: grant.Credential, Config: config})
		return err
	})
}

func samePluginLoginActor(flow *pluginLoginFlow, actor, auth string) bool {
	return flow != nil && flow.Actor == actor && auth != "" && subtle.ConstantTimeCompare([]byte(flow.Auth), []byte(auth)) == 1
}

func (s *NewAPIPluginService) newPluginLoginFlow(ctx context.Context, actor, auth, key string) (*pluginLoginFlow, error) {
	values := make([]string, 3)
	for i := range values {
		var raw [32]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, err
		}
		values[i] = base64.RawURLEncoding.EncodeToString(raw[:])
	}
	base := ""
	if s.marketSettings != nil {
		base, _ = s.marketSettings.GetValue(ctx, SettingKeyFrontendURL)
	}
	if base == "" && s.gateway.cfg != nil {
		base = s.gateway.cfg.Server.FrontendURL
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "https" && (u.Scheme != "http" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1")) {
		return nil, errors.New("configure the site's public frontend URL before plugin login")
	}
	redirect := strings.TrimRight(base, "/") + "/plugin-login/callback/" + url.PathEscape(key)
	return &pluginLoginFlow{Actor: actor, Auth: auth, State: values[0], Nonce: values[1], Verifier: values[2], Redirect: redirect, View: PluginLoginView{Flow: values[0], ExpiresAt: time.Now().Add(15 * time.Minute).Unix()}}, nil
}

func pluginLoginStepInput(flow *pluginLoginFlow, input PluginLoginInput) (any, error) {
	switch flow.View.Type {
	case jsplugin.LoginStepDevice, jsplugin.LoginStepPending:
		if !input.Poll || input.CallbackURL != "" || len(input.Fields) > 0 || time.Now().Before(flow.NextPollAt) {
			return nil, pluginInputError("wait until the next login poll")
		}
		return map[string]any{"poll": true}, nil
	case jsplugin.LoginStepFields:
		if input.Poll || input.CallbackURL != "" || len(input.Fields) > 32 {
			return nil, pluginInputError("invalid login fields")
		}
		size := 0
		for name, value := range input.Fields {
			if !slices.ContainsFunc(flow.View.Fields, func(f jsplugin.LoginField) bool { return f.Name == name }) {
				return nil, pluginInputError("unknown login field")
			}
			size += len(name) + len(value)
		}
		for _, field := range flow.View.Fields {
			if field.Required && strings.TrimSpace(input.Fields[field.Name]) == "" {
				return nil, pluginInputError("required login field is empty")
			}
		}
		if size > 64<<10 {
			return nil, pluginInputError("login input exceeds limit")
		}
		return map[string]any{"fields": input.Fields}, nil
	case jsplugin.LoginStepRedirect, jsplugin.LoginStepPaste:
		if input.Poll || len(input.Fields) > 0 || len(input.CallbackURL) > 64<<10 {
			return nil, pluginInputError("invalid login callback")
		}
		u, err := url.Parse(input.CallbackURL)
		if err != nil || u.User != nil || u.Fragment != "" || u.Scheme != "https" && u.Scheme != "http" {
			return nil, pluginInputError("invalid login callback")
		}
		params, err := url.ParseQuery(u.RawQuery)
		// Match the pinned host: some paste/device providers omit state and
		// rely on the host-generated PKCE verifier. Present state must match.
		_, hasState := params["state"]
		if err != nil || len(params) == 0 || len(params) > 64 || hasState && params.Get("state") != flow.State || flow.Issuer != "" && params.Get("iss") != flow.Issuer {
			return nil, pluginInputError("login callback state or issuer does not match")
		}
		if _, denied := params["error"]; denied {
			return nil, pluginInputError("the upstream refused plugin login")
		}
		callback := make(map[string]string, len(params))
		for name, values := range params {
			if len(values) != 1 {
				return nil, pluginInputError("duplicate login callback parameter")
			}
			callback[name] = values[0]
		}
		return map[string]any{"callback": callback}, nil
	}
	return nil, pluginInputError("unsupported login step")
}

func updatePluginLoginFlow(flow *pluginLoginFlow, step jsplugin.LoginStep) {
	if step.Session != nil {
		flow.Session = step.Session
	}
	flow.Issuer = step.Issuer
	interval := step.Interval
	if interval <= 0 {
		interval = flow.View.Interval
	}
	if interval <= 0 {
		interval = 5
	}
	expires := flow.View.ExpiresAt
	if step.ExpiresIn > 0 && time.Now().Add(time.Duration(step.ExpiresIn)*time.Second).Unix() < expires {
		expires = time.Now().Add(time.Duration(step.ExpiresIn) * time.Second).Unix()
	}
	flow.View = PluginLoginView{Flow: flow.State, Type: step.Type, URL: step.URL, Hint: step.Hint, UserCode: step.UserCode, VerificationURI: step.VerificationURI, VerificationURIComplete: step.VerificationURIComplete, Fields: step.Fields, Interval: interval, ExpiresAt: expires}
	flow.NextPollAt = time.Now().Add(time.Duration(interval) * time.Second)
}

func setPluginCredentialResult(payload *pluginCredentialPayload, key string, result jsplugin.CredentialResult, refreshing bool) error {
	if result.Relogin {
		if payload.Credential == nil {
			return errors.New("no credential to mark for login")
		}
		payload.Credential.NeedsLogin = true
		payload.View.Status = "login_required"
		return nil
	}
	if payload.Credential != nil && payload.Credential.AccountID != "" && result.AccountID != "" && payload.Credential.AccountID != result.AccountID {
		return errors.New("plugin login or refresh changed the upstream identity")
	}
	next := &jsplugin.Credential{Plugin: key, Data: result.Credential, Headers: result.Headers, Query: result.Query, ExpiresAt: result.ExpiresAt, AccountID: result.AccountID, Label: result.Label}
	if refreshing && payload.Credential != nil {
		if next.AccountID == "" {
			next.AccountID = payload.Credential.AccountID
		}
		if next.Label == "" {
			next.Label = payload.Credential.Label
		}
	}
	if next.ExpiresAt != 0 && next.ExpiresAt <= time.Now().Unix() {
		return errors.New("plugin returned an expired credential")
	}
	if _, err := next.Line(); err != nil {
		return err
	}
	payload.Credential, payload.View.Status = next, "active"
	if refreshing && payload.TaskBinding != "" {
		// Keep the login identity through token and metadata refreshes.
	} else if next.AccountID != "" {
		payload.TaskBinding = HashUsageRequestPayload([]byte(key + "\x00" + next.AccountID))
	} else if !refreshing {
		// Anonymous grants have no upstream account identifier. A host-owned
		// login identity survives refresh but changes on the next login.
		flow, err := newPluginCredentialTaskBinding()
		if err != nil {
			return err
		}
		payload.TaskBinding = flow
	}
	payload.Generation++
	return nil
}

func providerCredentialContext(account *Account, base string, config, token json.RawMessage) jsplugin.CredentialContext {
	ctx := jsplugin.CredentialContext{BaseURL: base, APIKey: account.GetCredential("api_key"), Config: config, Token: token}
	if credential, ok := jsplugin.ParseCredential(account.newAPIProviderCredential); ok {
		ctx.Credential, ctx.APIKey = credential.Data, ""
	}
	return ctx
}

func providerCredentialCanRenew(version *NewAPIPluginVersion, account *Account) bool {
	if account.newAPIProviderCredential != "" {
		return slices.Contains(version.Capabilities.Hooks, "refresh")
	}
	return slices.Contains(version.Capabilities.Hooks, "exchangeKey")
}

func pluginCredentialAutoDisable(account *Account) bool {
	enabled, _ := account.Credentials["newapi_plugin_auto_disable"].(bool)
	return enabled
}

func applyProviderLoginAuth(req *http.Request, account *Account) bool {
	if account == nil {
		return false
	}
	credential, ok := jsplugin.ParseCredential(account.newAPIProviderCredential)
	if !ok {
		return false
	}
	for name, value := range credential.Headers {
		req.Header.Set(name, value)
	}
	query := req.URL.Query()
	for name, value := range credential.Query {
		query.Set(name, value)
	}
	req.URL.RawQuery = query.Encode()
	return true
}

func (s *NewAPIPluginService) loadProviderLoginCredential(ctx context.Context, account *Account, version *NewAPIPluginVersion, base string, refreshUsed string) (*Account, error) {
	return s.loadProviderLoginCredentialOnce(ctx, account, version, base, refreshUsed, true)
}

func (s *NewAPIPluginService) loadProviderLoginCredentialOnce(ctx context.Context, account *Account, version *NewAPIPluginVersion, base, refreshUsed string, mayRefresh bool) (*Account, error) {
	if !slices.Contains(version.Capabilities.Hooks, "login") && !slices.Contains(version.Capabilities.Hooks, "scheduled") {
		return account, nil
	}
	repo, err := s.credentialRepository()
	if err != nil {
		return nil, err
	}
	state, err := repo.ReadPluginCredential(ctx, account.ID)
	if err != nil {
		return nil, err
	}
	if state == nil || !samePluginCredentialConnection(state.Connection, pluginCredentialConnection(account)) {
		if slices.Contains(version.Capabilities.Hooks, "login") && account.GetCredential("api_key") == "" {
			return nil, errors.New("plugin credential requires login")
		}
		return account, nil
	}
	payload, err := s.readCredentialPayload(state)
	if err != nil {
		return nil, err
	}
	if slices.Contains([]string{"revoked", "revoke_pending", "login_required"}, payload.View.Status) || payload.View.Status == "auto_disabled" && pluginCredentialAutoDisable(account) {
		return nil, errors.New("plugin credential is unavailable")
	}
	if payload.Credential == nil {
		if slices.Contains(version.Capabilities.Hooks, "login") && account.GetCredential("api_key") == "" {
			return nil, errors.New("plugin credential requires login")
		}
		return account, nil
	}
	line, err := payload.Credential.Line()
	if err != nil {
		return nil, err
	}
	if mayRefresh && slices.Contains(version.Capabilities.Hooks, "refresh") && (payload.Credential.ExpiresWithin(time.Now(), newAPIProviderTokenMargin) || refreshUsed != "" && refreshUsed == line) {
		if _, err := s.managePluginCredential(ctx, account, version, base, state.Revision, "", "", "refresh", PluginLoginInput{}); err != nil {
			if !errors.Is(err, ErrPluginCredentialConflict) {
				// Match the pinned host: a proactive refresh failure may keep
				// a still-valid grant. A rejection or expired grant never does.
				if refreshUsed == "" && !payload.Credential.ExpiresWithin(time.Now(), 0) {
					copy := *account
					copy.newAPIProviderCredential = line
					return &copy, nil
				}
				return nil, err
			}
			// Another instance owns this revision. Wait only for its bounded
			// hook, then consume its persisted result without refreshing twice.
			waitCtx, cancel := context.WithTimeout(ctx, newAPIProviderCredentialTimeout)
			defer cancel()
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-waitCtx.Done():
					return nil, ErrPluginCredentialConflict
				case <-ticker.C:
					updated, readErr := repo.ReadPluginCredential(waitCtx, account.ID)
					if readErr != nil {
						return nil, readErr
					}
					if updated != nil && updated.Revision != state.Revision {
						if refreshUsed != "" {
							completed, err := s.readCredentialPayload(updated)
							if err != nil || completed.Generation <= payload.Generation {
								return nil, errors.New("plugin credential renewal did not complete")
							}
						}
						return s.loadProviderLoginCredentialOnce(waitCtx, account, version, base, "", false)
					}
				}
			}
		}
		return s.loadProviderLoginCredentialOnce(ctx, account, version, base, "", false)
	}
	if payload.Credential.NeedsLogin || payload.Credential.ExpiresWithin(time.Now(), 0) {
		return nil, errors.New("plugin credential has expired")
	}
	copy := *account
	copy.newAPIProviderCredential = line
	return &copy, nil
}
