// SPDX-License-Identifier: AGPL-3.0-or-later
// Task credential semantics follow QuantumNous/new-api at
// 48f9df96ea7c68eae04a0f3687869f232f48929f, relay/channel/task/jsplugin/credential.go.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
)

type GenerationCredentialBinding = generation.CredentialBinding

var ErrGenerationCredential = generation.ErrCredential

func newPluginCredentialTaskBinding() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func pluginCredentialRuntimeSupported(version *NewAPIPluginVersion) bool {
	return version != nil && (version.Capabilities.Task && (len(pluginVideoModels(version.Meta)) > 0 || sunoTaskVersion(version)) || version.Capabilities.Provider && len(pluginProviderBinding(version.Meta, version.Capabilities).Routes) > 0)
}

func SameGenerationCredential(a, b GenerationCredentialBinding) bool {
	return generation.SameCredential(a, b)
}

// bind=true is used exactly once, before pricing and the hold. Later calls may
// refresh that same login, but cannot adopt a different upstream identity.
func (s *NewAPIPluginService) prepareTaskCredential(ctx context.Context, account *Account, task *GenerationTask, bind bool) (*Account, error) {
	if account == nil || task == nil || task.CredentialBinding.Validate() != nil {
		return nil, ErrGenerationCredential
	}
	version, err := s.repo.Get(ctx, task.PluginVersionID)
	if err != nil {
		return nil, err
	}
	managed := account.GetCredential("newapi_upstream_kind") != "new_api" && (slices.Contains(version.Capabilities.Hooks, "login") || slices.Contains(version.Capabilities.Hooks, "scheduled"))
	if !managed {
		if task.CredentialBinding.Mode == "login" {
			return nil, ErrGenerationCredential
		}
		if bind {
			task.CredentialBinding = GenerationCredentialBinding{Mode: "static"}
		}
		return s.prepareTaskJWT(ctx, account, version, task, bind)
	}
	// Unlike static-key rotation, changing a managed connection cannot transfer
	// an old task to a login on the replacement connection.
	if !version.Enabled || account.NewAPIPluginVersionID() != version.ID || generationAccountConnection(account) != task.Connection || account.GetCredential(newAPIPluginConfigEncryptedKey) != task.ConfigEncrypted {
		return nil, ErrGenerationCredential
	}
	ctx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer release()
	if err := pkghttputil.ReserveRequestBodyMemory(ctx, newAPIProviderMemory); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, newAPIProviderSetupTimeout)
	defer cancel()
	resolved, err := s.loadProviderLoginCredential(ctx, account, version, task.Connection.BaseURL, "")
	if err != nil {
		return nil, err
	}
	binding := GenerationCredentialBinding{Mode: "static", Connection: pluginCredentialConnection(account)}
	if grant, exists := jsplugin.ParseCredential(resolved.newAPIProviderCredential); exists {
		if grant.Plugin != version.Key {
			return nil, ErrGenerationCredential
		}
		repo, err := s.credentialRepository()
		if err != nil {
			return nil, err
		}
		state, err := repo.ReadPluginCredential(ctx, account.ID)
		if err != nil || state == nil || !samePluginCredentialConnection(state.Connection, binding.Connection) {
			return nil, ErrGenerationCredential
		}
		payload, err := s.readCredentialPayload(state)
		if err != nil || payload.Credential == nil {
			return nil, ErrGenerationCredential
		}
		line, err := payload.Credential.Line()
		if err != nil || line != resolved.newAPIProviderCredential {
			return nil, ErrPluginCredentialConflict
		}
		binding.Mode, binding.Identity = "login", payload.TaskBinding
		// Provider grants saved before Task support can identify named accounts
		// without mutation. Anonymous old grants require a new managed login.
		if binding.Identity == "" && grant.AccountID != "" {
			binding.Identity = HashUsageRequestPayload([]byte(version.Key + "\x00" + grant.AccountID))
		}
	}
	if binding.Validate() != nil {
		return nil, ErrGenerationCredential
	}
	if bind {
		if plan, _ := ctx.Value(generationVideoPlanKey{}).(*generationVideoPlan); plan != nil && len(plan.origins) > 0 && !SameGenerationCredential(plan.credential, binding) {
			return nil, ErrGenerationOrigin
		}
		task.CredentialBinding = binding
	} else if !SameGenerationCredential(task.CredentialBinding, binding) || len(task.CredentialBinding.Connection) > 0 && !samePluginCredentialConnection(task.CredentialBinding.Connection, binding.Connection) {
		return nil, ErrGenerationCredential
	}
	return s.prepareTaskJWT(ctx, resolved, version, task, bind)
}

func (s *newAPITaskSession) taskCredentialContext(value map[string]any) {
	if s.account.GetCredential("newapi_upstream_kind") == "new_api" {
		auth := "Bearer " + s.account.GetCredential("api_key")
		value["authHeader"], value["auth"] = auth, map[string]any{"authHeader": auth}
	} else if grant, ok := jsplugin.ParseCredential(s.account.newAPIProviderCredential); ok {
		value["apiKey"], value["credential"] = "", grant.Data
		auth := ""
		for name, header := range grant.Headers {
			if strings.EqualFold(name, "Authorization") {
				auth = header
			}
		}
		value["authHeader"] = auth
		value["auth"] = map[string]any{"authHeader": auth}
	} else if s.version.Meta.Auth.Type == "oauth2_jwt" {
		// A service account private key is host input, never a Task hook input.
		value["apiKey"] = ""
		auth := map[string]any{"authHeader": ""}
		if resolved := s.account.newAPITaskJWTAuth; resolved != nil {
			auth["authHeader"], auth["projectId"] = resolved.Header, resolved.ProjectID
		}
		value["authHeader"], value["auth"] = auth["authHeader"], auth
	} else {
		auth := s.account.GetCredential("api_key")
		value["authHeader"], value["auth"] = auth, map[string]any{"authHeader": auth}
	}
}
