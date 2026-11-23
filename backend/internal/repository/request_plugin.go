package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const requestPluginSelect = `SELECT i.id,i.name,i.plugin_version_id,i.enabled,i.scope,i.group_ids,i.models,i.position,i.stages,i.revision,i.updated_at,i.formats,i.account_ids,i.network_enabled,i.reply_network_enabled,
 i.config_encrypted,v.plugin_key,v.version,v.source,v.meta,v.capabilities
 FROM sub2api_request_plugin_instances i JOIN sub2api_js_plugin_versions v ON v.id=i.plugin_version_id`

func scanRequestPlugin(row interface{ Scan(...any) error }) (*service.RequestPluginInstance, error) {
	i := &service.RequestPluginInstance{Version: &service.NewAPIPluginVersion{}}
	var groups, accounts, models, stages, formats, meta, capabilities []byte
	err := row.Scan(&i.ID, &i.Name, &i.PluginVersionID, &i.Enabled, &i.Scope, &groups, &models, &i.Position, &stages, &i.Revision, &i.UpdatedAt, &formats, &accounts, &i.NetworkEnabled, &i.ReplyNetworkEnabled, &i.ConfigEncrypted, &i.PluginKey, &i.PluginVersion, &i.Version.Source, &meta, &capabilities)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrRequestPluginMissing
	}
	if err != nil {
		return nil, err
	}
	for _, value := range []struct {
		raw    []byte
		target any
	}{{groups, &i.GroupIDs}, {accounts, &i.AccountIDs}, {models, &i.Models}, {stages, &i.Stages}, {formats, &i.Formats}, {meta, &i.Version.Meta}, {capabilities, &i.Version.Capabilities}} {
		if err = json.Unmarshal(value.raw, value.target); err != nil {
			return nil, err
		}
	}
	i.Version.ID = i.PluginVersionID
	i.Version.Key = i.PluginKey
	i.Version.Version = i.PluginVersion
	return i, nil
}

func (r *newAPIPluginRepository) ListRequestPluginInstances(ctx context.Context, execution bool) ([]service.RequestPluginInstance, error) {
	query := requestPluginSelect
	if !execution {
		// The administrative list does not need source code or encrypted secrets.
		query = strings.Replace(query, "i.config_encrypted,v.plugin_key,v.version,v.source", "''::text,v.plugin_key,v.version,''::text", 1)
	}
	if execution {
		query += ` WHERE i.enabled=TRUE`
	}
	query += ` ORDER BY i.position,i.id LIMIT 65`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.RequestPluginInstance, 0)
	for rows.Next() {
		item, err := scanRequestPlugin(rows)
		if err != nil {
			return nil, err
		}
		if !execution {
			item.ConfigEncrypted = ""
			item.Version = nil
		}
		items = append(items, *item)
	}
	if len(items) > 64 {
		return nil, errors.New("request processing exceeds instance limit")
	}
	return items, rows.Err()
}

func (r *newAPIPluginRepository) GetRequestPluginInstance(ctx context.Context, id int64) (*service.RequestPluginInstance, error) {
	return scanRequestPlugin(r.db.QueryRowContext(ctx, requestPluginSelect+` WHERE i.id=$1`, id))
}

func (r *newAPIPluginRepository) SaveRequestPluginInstance(ctx context.Context, item *service.RequestPluginInstance, actor int64) (*service.RequestPluginInstance, error) {
	groups, _ := json.Marshal(item.GroupIDs)
	models, _ := json.Marshal(item.Models)
	stages, _ := json.Marshal(item.Stages)
	formatValues := item.Formats
	if formatValues == nil {
		formatValues = []string{}
	}
	formats, _ := json.Marshal(formatValues)
	accountValues := item.AccountIDs
	if accountValues == nil {
		accountValues = []int64{}
	}
	accounts, _ := json.Marshal(accountValues)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('request-plugin-instances',0))`); err != nil {
		return nil, err
	}
	for _, id := range item.GroupIDs {
		var found int64
		if err = tx.QueryRowContext(ctx, `SELECT id FROM groups WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, id).Scan(&found); err != nil {
			return nil, errors.New("请求处理实例引用了不存在的分组")
		}
	}
	for _, id := range item.AccountIDs {
		var found int64
		if err = tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, id).Scan(&found); err != nil {
			return nil, errors.New("请求处理实例引用了不存在的账号")
		}
	}
	var id int64
	if item.ID == 0 {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sub2api_request_plugin_instances`).Scan(&count); err != nil {
			return nil, err
		}
		if count >= 64 {
			return nil, errors.New("最多配置 64 个请求处理实例")
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO sub2api_request_plugin_instances(name,plugin_version_id,enabled,scope,group_ids,models,position,stages,config_encrypted,updated_by,formats,account_ids,network_enabled,reply_network_enabled) VALUES($1,$2,$3,$4,$5::jsonb,$6::jsonb,$7,$8::jsonb,$9,$10,$11::jsonb,$12::jsonb,$13,$14) RETURNING id`, item.Name, item.PluginVersionID, item.Enabled, item.Scope, groups, models, item.Position, stages, item.ConfigEncrypted, actor, formats, accounts, item.NetworkEnabled, item.ReplyNetworkEnabled).Scan(&id)
	} else {
		err = tx.QueryRowContext(ctx, `UPDATE sub2api_request_plugin_instances SET name=$3,plugin_version_id=$4,enabled=$5,scope=$6,group_ids=$7::jsonb,models=$8::jsonb,position=$9,stages=$10::jsonb,config_encrypted=$11,updated_by=$12,formats=$13::jsonb,account_ids=$14::jsonb,network_enabled=$15,reply_network_enabled=$16,revision=revision+1,updated_at=NOW() WHERE id=$1 AND revision=$2 RETURNING id`, item.ID, item.Revision, item.Name, item.PluginVersionID, item.Enabled, item.Scope, groups, models, item.Position, stages, item.ConfigEncrypted, actor, formats, accounts, item.NetworkEnabled, item.ReplyNetworkEnabled).Scan(&id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrRequestPluginConflict
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	saved, err := r.GetRequestPluginInstance(ctx, id)
	if err == nil {
		saved.ConfigEncrypted = ""
		saved.Version = nil
	}
	return saved, err
}

func (r *newAPIPluginRepository) RecordRequestPluginRun(ctx context.Context, run service.RequestPluginRun) error {
	records, err := json.Marshal(run.Records)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO sub2api_request_plugin_runs(request_id,user_id,group_id,model,format,dry_run,records) VALUES($1,NULLIF($2::bigint,0),NULLIF($3::bigint,0),$4,$5,$6,$7::jsonb)`, run.RequestID, run.UserID, run.GroupID, run.Model, run.Format, run.DryRun, records)
	return err
}

func (r *newAPIPluginRepository) ListRequestPluginRuns(ctx context.Context, requestID string, page, size int) ([]service.RequestPluginRun, int64, error) {
	if page < 1 || page > 100000 || size < 1 || size > 100 {
		return nil, 0, errors.New("invalid request plugin pagination")
	}
	where := ` FROM sub2api_request_plugin_runs WHERE ($1::text='' OR request_id=$1)`
	var count int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*)`+where, requestID).Scan(&count); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,request_id,COALESCE(user_id,0),COALESCE(group_id,0),model,format,dry_run,records,created_at`+where+` ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, requestID, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.RequestPluginRun, 0)
	for rows.Next() {
		var item service.RequestPluginRun
		var raw []byte
		if err = rows.Scan(&item.ID, &item.RequestID, &item.UserID, &item.GroupID, &item.Model, &item.Format, &item.DryRun, &raw, &item.CreatedAt); err != nil {
			return nil, 0, err
		}
		if err = json.Unmarshal(raw, &item.Records); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, count, rows.Err()
}
