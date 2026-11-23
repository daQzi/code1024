package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

const pluginCredentialConnectionSQL = `jsonb_build_array(a.platform,a.type,a.proxy_id,a.credentials->'newapi_plugin_version_id',
 CASE WHEN jsonb_typeof(a.credentials->'base_url')='string' THEN encode(sha256(convert_to(a.credentials->>'base_url','UTF8')),'hex') END,
 CASE WHEN jsonb_typeof(a.credentials->'api_key')='string' THEN encode(sha256(convert_to(a.credentials->>'api_key','UTF8')),'hex') END,
 CASE WHEN jsonb_typeof(a.credentials->'newapi_plugin_config_encrypted')='string' THEN encode(sha256(convert_to(a.credentials->>'newapi_plugin_config_encrypted','UTF8')),'hex') END)`
const pluginCredentialStateColumns = `account_id,plugin_version_id,connection,revision,encrypted,public,next_run_at,COALESCE(lease_token::text,''),native_disabled_at,native_disable_pending,native_disabled_reason,last_disabled_run_at`

const pluginCredentialNativeMaintenanceSQL = `a.status='error' AND a.schedulable=false AND EXISTS(SELECT 1 FROM sub2api_plugin_credential_state native WHERE native.account_id=a.id AND native.connection=` + pluginCredentialConnectionSQL + ` AND native.native_disabled_at=a.updated_at)`

func readPluginCredentialState(row interface{ Scan(...any) error }) (*service.PluginCredentialState, error) {
	s := &service.PluginCredentialState{}
	err := row.Scan(&s.AccountID, &s.PluginVersionID, &s.Connection, &s.Revision, &s.Encrypted, &s.Public, &s.NextRunAt, &s.LeaseToken, &s.NativeDisabledAt, &s.NativeDisablePending, &s.NativeDisabledReason, &s.LastDisabledRunAt)
	return s, err
}

func (r *newAPIPluginRepository) ReadPluginCredential(ctx context.Context, id int64) (*service.PluginCredentialState, error) {
	s, err := readPluginCredentialState(r.db.QueryRowContext(ctx, `SELECT `+pluginCredentialStateColumns+` FROM sub2api_plugin_credential_state WHERE account_id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return s, err
}

func (r *newAPIPluginRepository) ClaimPluginCredential(ctx context.Context, expected *service.PluginCredentialState, revision int64) (*service.PluginCredentialState, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var connection []byte
	err = tx.QueryRowContext(ctx, `SELECT `+pluginCredentialConnectionSQL+` FROM accounts a WHERE a.id=$1 AND a.deleted_at IS NULL AND a.credentials->>'newapi_plugin_version_id'=$2::text AND (NOT $3 OR a.status='active' AND a.schedulable=true OR (`+pluginCredentialNativeMaintenanceSQL+`)) FOR SHARE`, expected.AccountID, expected.PluginVersionID, expected.OnlyIfDue).Scan(&connection)
	if err != nil {
		return nil, service.ErrPluginCredentialConflict
	}
	var equal bool
	if err = tx.QueryRowContext(ctx, `SELECT $1::jsonb=$2::jsonb`, connection, expected.Connection).Scan(&equal); err != nil || !equal {
		return nil, service.ErrPluginCredentialConflict
	}
	// Reset only when the actual account binding changed. Old in-flight hooks
	// lose their claim token and can never commit to the replacement identity.
	_, err = tx.ExecContext(ctx, `INSERT INTO sub2api_plugin_credential_state(account_id,plugin_version_id,connection)
VALUES($1,$2,$3::jsonb) ON CONFLICT(account_id) DO UPDATE SET
 plugin_version_id=EXCLUDED.plugin_version_id,connection=EXCLUDED.connection,revision=sub2api_plugin_credential_state.revision+1,
 encrypted='',public='{}'::jsonb,lease_token=NULL,lease_until=NULL,next_run_at=NOW(),updated_at=NOW()
WHERE sub2api_plugin_credential_state.connection<>EXCLUDED.connection`, expected.AccountID, expected.PluginVersionID, expected.Connection)
	if err != nil {
		return nil, err
	}
	state, err := readPluginCredentialState(tx.QueryRowContext(ctx, `UPDATE sub2api_plugin_credential_state SET lease_token=$2,lease_until=NOW()+INTERVAL '60 seconds'
WHERE account_id=$1 AND (lease_until IS NULL OR lease_until<NOW()) AND ($3::bigint=0 OR revision=$3) AND (NOT $4 OR next_run_at<=NOW())
RETURNING `+pluginCredentialStateColumns, expected.AccountID, uuid.NewString(), revision, expected.OnlyIfDue))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrPluginCredentialConflict
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	state.OnlyIfDue = expected.OnlyIfDue
	return state, nil
}

func (r *newAPIPluginRepository) FinishPluginCredential(ctx context.Context, state *service.PluginCredentialState) error {
	var public map[string]json.RawMessage
	if len(state.Encrypted) > 262144 || len(state.Public) > 32768 || json.Unmarshal(state.Public, &public) != nil || public == nil {
		return errors.New("invalid plugin credential state")
	}
	// Serialize account edits, credential commit and optional native recovery.
	// No database lock is held during hook network I/O.
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var matches bool
	var key string
	err = tx.QueryRowContext(ctx, `SELECT `+pluginCredentialConnectionSQL+`=$2::jsonb,v.plugin_key FROM accounts a JOIN sub2api_js_plugin_versions v ON v.id=$3 WHERE a.id=$1 AND a.deleted_at IS NULL AND (NOT $4 OR a.status='active' AND a.schedulable=true OR (`+pluginCredentialNativeMaintenanceSQL+`)) FOR UPDATE OF a`, state.AccountID, state.Connection, state.PluginVersionID, state.OnlyIfDue).Scan(&matches, &key)
	if err != nil || !matches {
		return service.ErrPluginCredentialConflict
	}
	var view struct {
		Credential *struct {
			AccountID string `json:"account_id"`
		} `json:"credential"`
	}
	if json.Unmarshal(state.Public, &view) != nil {
		return errors.New("invalid plugin credential summary")
	}
	if view.Credential != nil && view.Credential.AccountID != "" {
		var identity string
		if err = tx.QueryRowContext(ctx, pluginCredentialIdentitySQL, key, view.Credential.AccountID).Scan(&identity); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, identity); err != nil {
			return err
		}
		var retiring bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sub2api_plugin_credential_revocations WHERE identity_fingerprint=$1 AND status='pending' AND lease_until>NOW())`, identity).Scan(&retiring); err != nil {
			return err
		}
		if retiring {
			return service.ErrPluginCredentialConflict
		}
		var duplicate bool
		if err = tx.QueryRowContext(ctx, pluginCredentialInUseSQL, state.PluginVersionID, view.Credential.AccountID, state.AccountID).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate {
			return service.ErrPluginCredentialIdentity
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE sub2api_plugin_credential_state SET encrypted=$3,public=$4::jsonb,next_run_at=$5,revision=revision+1,lease_token=NULL,lease_until=NULL,updated_at=NOW(),
native_disable_pending=$7,last_disabled_run_at=$8,
native_disabled_at=CASE WHEN $9 THEN NULL ELSE native_disabled_at END,native_disabled_reason=CASE WHEN $9 THEN '' ELSE native_disabled_reason END
WHERE account_id=$1 AND lease_token=$2::uuid AND lease_until>NOW() AND connection=$6::jsonb`, state.AccountID, state.LeaseToken, state.Encrypted, state.Public, state.NextRunAt, state.Connection, state.NativeDisablePending, state.LastDisabledRunAt, state.RecoverNative)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return service.ErrPluginCredentialConflict
	}
	if state.RecoverNative {
		result, err = tx.ExecContext(ctx, `UPDATE accounts SET status='active',schedulable=true,error_message='',updated_at=clock_timestamp()
WHERE id=$1 AND status='error' AND schedulable=false AND updated_at=$2 AND credentials->'newapi_plugin_auto_disable'='true'::jsonb
AND EXISTS(SELECT 1 FROM sub2api_js_plugin_versions v JOIN sub2api_js_plugins p USING(plugin_key) WHERE v.id=$3 AND p.enabled=true)`, state.AccountID, state.NativeDisabledAt, state.PluginVersionID)
		if err != nil {
			return err
		}
		n, err = result.RowsAffected()
		if err != nil || n != 1 {
			return service.ErrPluginCredentialConflict
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO scheduler_outbox(event_type,account_id,group_id,payload) VALUES($1,$2,NULL,NULL)`, service.SchedulerOutboxEventAccountChanged, state.AccountID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const pluginCredentialIdentitySQL = `SELECT encode(sha256(convert_to(jsonb_build_array($1::text,$2::text)::text,'UTF8')),'hex')`

const pluginCredentialInUseSQL = `SELECT EXISTS(SELECT 1 FROM sub2api_plugin_credential_state s
JOIN accounts a ON a.id=s.account_id AND a.deleted_at IS NULL
JOIN sub2api_js_plugin_versions v ON v.id=s.plugin_version_id
WHERE v.plugin_key=(SELECT plugin_key FROM sub2api_js_plugin_versions WHERE id=$1)
AND s.public->'credential'->>'account_id'=$2 AND s.account_id<>$3 AND s.connection=` + pluginCredentialConnectionSQL + `)`

func (r *newAPIPluginRepository) PluginCredentialInUse(ctx context.Context, versionID int64, account string) (bool, error) {
	if versionID <= 0 || account == "" || len(account) > 4096 {
		return true, errors.New("invalid plugin credential identity")
	}
	var exists bool
	err := r.db.QueryRowContext(ctx, pluginCredentialInUseSQL, versionID, account, 0).Scan(&exists)
	return exists, err
}

func (r *newAPIPluginRepository) ReleasePluginCredential(ctx context.Context, state *service.PluginCredentialState) error {
	_, err := r.db.ExecContext(ctx, `UPDATE sub2api_plugin_credential_state SET lease_token=NULL,lease_until=NULL,next_run_at=GREATEST(next_run_at,$3),updated_at=NOW() WHERE account_id=$1 AND lease_token=$2::uuid`, state.AccountID, state.LeaseToken, time.Now().Add(time.Minute))
	return err
}

func (r *newAPIPluginRepository) DuePluginCredentials(ctx context.Context) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT a.id FROM accounts a
JOIN sub2api_js_plugin_versions v ON v.id=CASE WHEN a.credentials->>'newapi_plugin_version_id' ~ '^[0-9]{1,18}$' THEN (a.credentials->>'newapi_plugin_version_id')::bigint END
JOIN sub2api_js_plugins p ON p.plugin_key=v.plugin_key AND p.enabled=true
LEFT JOIN sub2api_plugin_credential_state s ON s.account_id=a.id AND s.connection=`+pluginCredentialConnectionSQL+`
WHERE a.deleted_at IS NULL AND (a.status='active' AND a.schedulable=true OR (`+pluginCredentialNativeMaintenanceSQL+`)) AND a.type='apikey' AND a.credentials ? 'newapi_plugin_version_id'
 AND (a.platform='openai' AND v.capabilities->>'provider'='true' OR v.capabilities->>'task'='true') AND (v.capabilities->'hooks' ?| ARRAY['login','scheduled'])
 AND (s.account_id IS NULL OR s.next_run_at<=NOW() AND (s.lease_until IS NULL OR s.lease_until<NOW()))
ORDER BY COALESCE(s.next_run_at,'epoch'::timestamptz),a.id LIMIT 32`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}
