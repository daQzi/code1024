package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.GenerationVideoRoutingRepository = (*generationTaskRepository)(nil)

const generationOriginColumns = `id,user_id,api_key_id,group_id,account_id,plugin_version_id,model,upstream_model,platform,connection,config_encrypted,upstream_id,action,status,data,credential_binding`

func scanGenerationOrigin(row interface{ Scan(...any) error }) (*service.GenerationTask, error) {
	parent := &service.GenerationTask{}
	var connection, credentialBinding []byte
	err := row.Scan(&parent.ID, &parent.UserID, &parent.APIKeyID, &parent.GroupID, &parent.AccountID, &parent.PluginVersionID, &parent.Model, &parent.UpstreamModel, &parent.Platform, &connection, &parent.ConfigEncrypted, &parent.UpstreamID, &parent.Action, &parent.Status, &parent.Data, &credentialBinding)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrGenerationTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(connection, &parent.Connection); err != nil {
		return nil, err
	}
	if json.Unmarshal(credentialBinding, &parent.CredentialBinding) != nil || parent.CredentialBinding.Validate() != nil {
		return nil, service.ErrGenerationCredential
	}
	return parent, nil
}

func (r *generationTaskRepository) GetVideoOrigin(ctx context.Context, id string, user, key, group int64) (*service.GenerationTask, error) {
	return scanGenerationOrigin(r.db.QueryRowContext(ctx, `SELECT `+generationOriginColumns+` FROM sub2api_generation_tasks WHERE id=$1 AND user_id=$2 AND api_key_id=$3 AND group_id=$4 AND public_deleted_at IS NULL`, id, user, key, group))
}

func (r *generationTaskRepository) ListVideoDecodeCandidates(ctx context.Context, user, key, group int64, platform, model string) ([]service.GenerationVideoDecodeCandidate, error) {
	// Disabled accounts and non-active historical versions still describe the
	// protocol of an owned parent. No account secret or plugin config is read.
	rows, err := r.db.QueryContext(ctx, `
 WITH candidates AS (
 SELECT CASE WHEN a.credentials->>'newapi_plugin_version_id' ~ '^[1-9][0-9]{0,17}$'
   THEN (a.credentials->>'newapi_plugin_version_id')::bigint END AS version_id,
   COALESCE(a.credentials->'model_mapping','{}'::jsonb) AS mapping,''::text AS upstream_model
 FROM accounts a JOIN account_groups ag ON ag.account_id=a.id
 WHERE ag.group_id=$3 AND a.platform=$4 AND a.deleted_at IS NULL AND a.type='apikey'
   AND a.credentials ? 'newapi_plugin_version_id'
 UNION
 SELECT t.plugin_version_id,'{}'::jsonb,CASE WHEN t.model=$5 THEN t.upstream_model ELSE $5 END
 FROM sub2api_generation_tasks t
 WHERE t.user_id=$1 AND t.api_key_id=$2 AND t.group_id=$3 AND t.platform=$4
 ) SELECT DISTINCT version_id,mapping,upstream_model FROM candidates c
 WHERE version_id IS NOT NULL AND ($6='' OR EXISTS (
   SELECT 1 FROM sub2api_js_plugin_versions v WHERE v.id=c.version_id AND v.plugin_key=$6
 )) ORDER BY version_id,upstream_model,mapping LIMIT 129`, user, key, group, platform, model, service.GenerationTaskSubmitPlugin(ctx))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []service.GenerationVideoDecodeCandidate
	totalMappingBytes := 0
	for rows.Next() {
		var candidate service.GenerationVideoDecodeCandidate
		var raw []byte
		if err := rows.Scan(&candidate.VersionID, &raw, &candidate.UpstreamModel); err != nil {
			return nil, err
		}
		totalMappingBytes += len(raw)
		if len(result) >= 128 || len(raw) > 64<<10 || totalMappingBytes > 512<<10 {
			return nil, service.ErrGenerationCapacity
		}
		if err := json.Unmarshal(raw, &candidate.Mapping); err != nil || candidate.Mapping == nil {
			return nil, service.ErrGenerationDecode
		}
		result = append(result, candidate)
	}
	return result, rows.Err()
}

func generationOriginJSON(origins []service.GenerationOriginTask) ([]byte, error) {
	if err := service.ValidateGenerationOrigins(origins); err != nil {
		return nil, err
	}
	if len(origins) == 0 {
		return []byte(`[]`), nil
	}
	return json.Marshal(origins)
}

// Admission holds share locks on parents until the child's hold commits. A
// mutable parent or account cannot change ownership/connection underneath it.
// Locks are ordered so multi-parent submissions cannot invert lock order.
func bindGenerationOrigins(ctx context.Context, tx *sql.Tx, child *service.GenerationTask) error {
	if len(child.Origins) == 0 {
		return nil
	}
	parents := slices.Clone(child.Origins)
	slices.SortFunc(parents, func(a, b service.GenerationOriginTask) int {
		if a.TaskID < b.TaskID {
			return -1
		}
		if a.TaskID > b.TaskID {
			return 1
		}
		return 0
	})
	for _, origin := range parents {
		parent, err := scanGenerationOrigin(tx.QueryRowContext(ctx, `SELECT `+generationOriginColumns+` FROM sub2api_generation_tasks WHERE id=$1 AND data=$2::jsonb AND public_deleted_at IS NULL FOR SHARE`, origin.TaskID, []byte(origin.Data)))
		if err != nil {
			if errors.Is(err, service.ErrGenerationTaskNotFound) {
				return service.ErrGenerationOrigin
			}
			return err
		}
		if parent.ID == child.ID || parent.UserID != child.UserID || parent.APIKeyID != child.APIKeyID || parent.GroupID != child.GroupID || parent.AccountID != child.AccountID || parent.PluginVersionID != child.PluginVersionID || parent.Platform != child.Platform || parent.Connection != child.Connection || parent.ConfigEncrypted != child.ConfigEncrypted || parent.UpstreamID != origin.UpstreamTaskID || parent.Action != origin.Action || !service.SameGenerationCredential(parent.CredentialBinding, child.CredentialBinding) {
			return service.ErrGenerationOrigin
		}
		status := map[string]string{service.GenerationQueued: "QUEUED", service.GenerationSubmitting: "SUBMITTED", service.GenerationRunning: "IN_PROGRESS", service.GenerationUncertain: "UNKNOWN", service.GenerationSucceeded: "SUCCESS", service.GenerationFailed: "FAILURE"}[parent.Status]
		if status != origin.Status {
			return service.ErrGenerationOrigin
		}
	}
	return nil
}

func validateGenerationTaskAccount(ctx context.Context, tx *sql.Tx, child *service.GenerationTask) error {
	plugin := service.GenerationTaskSubmitPlugin(ctx)
	if len(child.Origins) == 0 && plugin == "" {
		return nil
	}
	var matches bool
	var mapping []byte
	platform := ""
	if plugin != "" {
		platform = child.Platform
	}
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(status='active' AND schedulable
 AND credentials->>'newapi_plugin_version_id'=($2::bigint)::text
 AND credentials->>'base_url'=$3
 AND COALESCE(NULLIF(credentials->>'newapi_upstream_kind',''),'vendor')=$4
 AND COALESCE(credentials->>'newapi_plugin_config_encrypted','')=$5
 AND ($6='' OR (type='apikey' AND platform=$6))
 ,false),COALESCE(credentials->'model_mapping','{}'::jsonb)
 FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, child.AccountID, child.PluginVersionID, child.Connection.BaseURL, child.Connection.Kind, child.ConfigEncrypted, platform).Scan(&matches, &mapping)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !matches {
		return service.ErrGenerationOrigin
	}
	if err != nil {
		return err
	}
	var fields map[string]any
	if len(mapping) > 64<<10 || json.Unmarshal(mapping, &fields) != nil {
		return service.ErrGenerationOrigin
	}
	account := &service.Account{Credentials: map[string]any{service.NewAPIPluginRevisionKey: child.PluginVersionID, "model_mapping": fields}}
	if account.GetMappedModel(child.Model) != child.UpstreamModel {
		return service.ErrGenerationOrigin
	}
	var group int64
	err = tx.QueryRowContext(ctx, `SELECT group_id FROM account_groups WHERE account_id=$1 AND group_id=$2 FOR SHARE`, child.AccountID, child.GroupID).Scan(&group)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrGenerationOrigin
	}
	if err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `SELECT p.enabled AND ($2='' OR p.plugin_key=$2) FROM sub2api_js_plugins p JOIN sub2api_js_plugin_versions v USING(plugin_key) WHERE v.id=$1 FOR SHARE OF p`, child.PluginVersionID, plugin).Scan(&matches)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !matches {
		return service.ErrGenerationOrigin
	}
	return err
}
