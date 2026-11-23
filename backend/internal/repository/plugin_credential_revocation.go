package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

func (r *newAPIPluginRepository) DuePluginCredentialRevocations(ctx context.Context) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM sub2api_plugin_credential_revocations WHERE status='pending' AND next_run_at<=NOW() AND (lease_until IS NULL OR lease_until<NOW()) ORDER BY next_run_at,id LIMIT 16`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *newAPIPluginRepository) ClaimPluginCredentialRevocation(ctx context.Context, id int64) (*service.PluginCredentialRevocation, error) {
	job := &service.PluginCredentialRevocation{}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var identity string
	err = tx.QueryRowContext(ctx, `SELECT identity_fingerprint FROM sub2api_plugin_credential_revocations WHERE id=$1 AND status='pending' AND next_run_at<=NOW() AND (lease_until IS NULL OR lease_until<NOW()) FOR UPDATE`, id).Scan(&identity)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrPluginCredentialConflict
	}
	if err != nil {
		return nil, err
	}
	if identity != "" {
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, identity); err != nil {
			return nil, err
		}
	}
	err = tx.QueryRowContext(ctx, `UPDATE sub2api_plugin_credential_revocations SET lease_token=$2,lease_until=NOW()+INTERVAL '60 seconds' WHERE id=$1 AND status='pending' AND next_run_at<=NOW() AND (lease_until IS NULL OR lease_until<NOW())
RETURNING id,account_id,plugin_version_id,status,attempts,last_error,created_at,finished_at,base_url,config_encrypted,state_encrypted,proxy_id,proxy_fingerprint,lease_token::text`, id, uuid.NewString()).Scan(&job.ID, &job.AccountID, &job.PluginVersionID, &job.Status, &job.Attempts, &job.LastError, &job.CreatedAt, &job.FinishedAt, &job.BaseURL, &job.ConfigEncrypted, &job.StateEncrypted, &job.ProxyID, &job.ProxyFingerprint, &job.LeaseToken)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrPluginCredentialConflict
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if job.ProxyID != nil {
		// Load an immutable in-memory transport only if it still matches the
		// retired connection. Never fall back to direct egress or a new proxy.
		var raw []byte
		err = r.db.QueryRowContext(ctx, `SELECT jsonb_build_object('ID',p.id,'Protocol',p.protocol,'Host',p.host,'Port',p.port,'Username',COALESCE(p.username,''),'Password',COALESCE(p.password,''),'Status',p.status)
FROM proxies p WHERE id=$1 AND deleted_at IS NULL AND status='active' AND (expires_at IS NULL OR expires_at>NOW())
AND encode(sha256(convert_to(jsonb_build_array(p.protocol,p.host,p.port,COALESCE(p.username,''),COALESCE(p.password,''))::text,'UTF8')),'hex')=$2`, *job.ProxyID, job.ProxyFingerprint).Scan(&raw)
		if err == nil {
			var proxy service.Proxy
			if json.Unmarshal(raw, &proxy) == nil {
				job.Proxy = &proxy
			}
		}
	}
	return job, nil
}

func (r *newAPIPluginRepository) FinishPluginCredentialRevocation(ctx context.Context, job *service.PluginCredentialRevocation, status, diagnostic string) error {
	if !slices.Contains([]string{"pending", "completed", "protected"}, status) || !slices.Contains([]string{"", "cleanup_failed", "original_proxy_changed", "plugin_unavailable", "no_revoke_hook", "credential_unreadable", "identity_in_use", "original_connection_invalid", "account_busy", "configuration_unreadable", "upstream_revoke_failed"}, diagnostic) {
		return errors.New("invalid credential cleanup outcome")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE sub2api_plugin_credential_revocations SET
status=CASE WHEN $3='pending' AND attempts>=9 THEN 'attention' ELSE $3 END,attempts=attempts+1,last_error=$4,next_run_at=NOW()+INTERVAL '15 minutes',lease_token=NULL,lease_until=NULL,
finished_at=CASE WHEN $3<>'pending' THEN NOW() ELSE NULL END,
state_encrypted=CASE WHEN $3<>'pending' THEN '' ELSE state_encrypted END,
config_encrypted=CASE WHEN $3<>'pending' THEN '' ELSE config_encrypted END
WHERE id=$1 AND lease_token=$2::uuid AND lease_until>NOW()`, job.ID, job.LeaseToken, status, diagnostic)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return service.ErrPluginCredentialConflict
	}
	return nil
}

func (r *newAPIPluginRepository) ListPluginCredentialRevocations(ctx context.Context) ([]service.PluginCredentialRevocation, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,account_id,plugin_version_id,status,attempts,last_error,created_at,finished_at FROM sub2api_plugin_credential_revocations ORDER BY id DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]service.PluginCredentialRevocation, 0)
	for rows.Next() {
		var job service.PluginCredentialRevocation
		if err := rows.Scan(&job.ID, &job.AccountID, &job.PluginVersionID, &job.Status, &job.Attempts, &job.LastError, &job.CreatedAt, &job.FinishedAt); err != nil {
			return nil, err
		}
		result = append(result, job)
	}
	return result, rows.Err()
}

func (r *newAPIPluginRepository) RetryPluginCredentialRevocation(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `UPDATE sub2api_plugin_credential_revocations SET status='pending',attempts=0,last_error='',next_run_at=NOW() WHERE id=$1 AND status IN ('pending','attention') AND state_encrypted<>'' AND (lease_until IS NULL OR lease_until<NOW())`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return service.ErrPluginCredentialConflict
	}
	return nil
}
