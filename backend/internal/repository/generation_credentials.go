package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// The account row is already locked by admission. Serialize the credential
// projection too: revocation or a replacement login cannot race the hold.
func bindGenerationCredential(ctx context.Context, tx *sql.Tx, task *service.GenerationTask) error {
	binding := task.CredentialBinding
	if len(binding.Connection) == 0 {
		return nil // Static-only or pre-upgrade task; no managed grant existed.
	}
	var matches bool
	var pluginKey string
	var public []byte
	err := tx.QueryRowContext(ctx, `SELECT `+pluginCredentialConnectionSQL+`=$2::jsonb AND COALESCE(NULLIF(a.credentials->>'newapi_upstream_kind',''),'vendor')=$4,v.plugin_key FROM accounts a
 JOIN sub2api_js_plugin_versions v ON v.id=$3 JOIN sub2api_js_plugins p USING(plugin_key)
 WHERE a.id=$1 AND a.deleted_at IS NULL AND a.credentials->>'newapi_plugin_version_id'=($3::bigint)::text
 AND p.enabled=true AND v.capabilities->>'task'='true'`, task.AccountID, binding.Connection, task.PluginVersionID, task.Connection.Kind).Scan(&matches, &pluginKey)
	if err != nil || !matches {
		return service.ErrGenerationCredential
	}
	err = tx.QueryRowContext(ctx, `SELECT public FROM sub2api_plugin_credential_state WHERE account_id=$1 AND plugin_version_id=$2 AND connection=$3::jsonb FOR SHARE`, task.AccountID, task.PluginVersionID, binding.Connection).Scan(&public)
	if err == sql.ErrNoRows && binding.Mode != "login" {
		return nil
	}
	if err != nil {
		if err == sql.ErrNoRows {
			return service.ErrGenerationCredential
		}
		return err
	}
	var view service.PluginCredentialView
	if json.Unmarshal(public, &view) != nil || slices.Contains([]string{"revoked", "revoke_pending", "login_required"}, view.Status) {
		return service.ErrGenerationCredential
	}
	if view.Status == "auto_disabled" {
		var disabled bool
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(credentials->'newapi_plugin_auto_disable'='true'::jsonb,false) FROM accounts WHERE id=$1`, task.AccountID).Scan(&disabled); err != nil {
			return err
		}
		if disabled {
			return service.ErrGenerationCredential
		}
	}
	if binding.Mode == "login" {
		if view.TaskBinding == "" && view.Credential != nil && view.Credential.AccountID != "" {
			view.TaskBinding = service.HashUsageRequestPayload([]byte(pluginKey + "\x00" + view.Credential.AccountID))
		}
		if view.TaskBinding != binding.Identity || view.Credential == nil || view.Credential.NeedsLogin || view.Credential.ExpiresAt > 0 && view.Credential.ExpiresAt <= time.Now().Unix() {
			return service.ErrGenerationCredential
		}
	} else if view.Credential != nil {
		return service.ErrGenerationCredential
	}
	return nil
}
