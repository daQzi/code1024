package repository

import (
	"context"
	"errors"
	"slices"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Both the native error and pending hook event commit in this single statement.
// A stale upstream response cannot disable an edited or manually paused account.
func (r *accountRepository) SetPluginCredentialError(ctx context.Context, event service.PluginCredentialDisable, message string) error {
	if event.AccountID <= 0 || event.PluginVersionID <= 0 || !slices.Contains([]string{"auth_error", "custom_error_code", "stream_timeout_error"}, event.Reason) {
		return errors.New("invalid plugin credential disable event")
	}
	_, err := r.sql.ExecContext(ctx, `WITH changed AS (
 UPDATE accounts a SET status='error',schedulable=false,error_message=$4,updated_at=clock_timestamp()
 WHERE a.id=$1 AND a.deleted_at IS NULL AND a.status='active' AND a.schedulable=true
 AND a.platform='openai' AND a.type='apikey' AND a.credentials->>'newapi_plugin_version_id'=($2::bigint)::text
 AND `+pluginCredentialConnectionSQL+`=$3::jsonb
 RETURNING a.id,a.updated_at
), saved AS (
 INSERT INTO sub2api_plugin_credential_state(account_id,plugin_version_id,connection,native_disabled_at,native_disable_pending,native_disabled_reason)
 SELECT c.id,$2::bigint,$3::jsonb,c.updated_at,true,$5 FROM changed c
 JOIN sub2api_js_plugin_versions v ON v.id=$2::bigint JOIN sub2api_js_plugins p ON p.plugin_key=v.plugin_key
 WHERE p.enabled=true AND v.capabilities->>'provider'='true' AND v.capabilities->'hooks' ? 'scheduled'
 ON CONFLICT(account_id) DO UPDATE SET native_disabled_at=EXCLUDED.native_disabled_at,native_disable_pending=true,native_disabled_reason=EXCLUDED.native_disabled_reason,
 next_run_at=GREATEST(NOW(),COALESCE(sub2api_plugin_credential_state.last_disabled_run_at+INTERVAL '1 minute',NOW())),
 revision=sub2api_plugin_credential_state.revision+1,lease_token=NULL,lease_until=NULL,updated_at=NOW()
 WHERE sub2api_plugin_credential_state.connection=EXCLUDED.connection
)
INSERT INTO scheduler_outbox(event_type,account_id,group_id,payload) SELECT $6,id,NULL,NULL FROM changed`, event.AccountID, event.PluginVersionID, event.Connection, message, event.Reason, service.SchedulerOutboxEventAccountChanged)
	if err == nil {
		r.syncSchedulerAccountSnapshotDetached(ctx, event.AccountID)
	}
	return err
}
