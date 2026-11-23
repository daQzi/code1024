package repository

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func (r *newAPIPluginRepository) ReadPluginCredentialScheduling(ctx context.Context, ids []int64) ([]service.PluginCredentialScheduling, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 256 {
		return nil, errors.New("plugin scheduling batch exceeds limit")
	}
	rows, err := r.db.QueryContext(ctx, `SELECT a.id,v.id,`+pluginCredentialConnectionSQL+`,p.enabled,
COALESCE(v.capabilities->'hooks' ? 'login',false),COALESCE(v.capabilities->'hooks' ? 'refresh',false),jsonb_build_object('status',s.public->'status','credential',s.public->'credential')
FROM accounts a
JOIN sub2api_js_plugin_versions v ON v.id=CASE WHEN a.credentials->>'newapi_plugin_version_id' ~ '^[0-9]{1,18}$' THEN (a.credentials->>'newapi_plugin_version_id')::bigint END
JOIN sub2api_js_plugins p ON p.plugin_key=v.plugin_key
LEFT JOIN sub2api_plugin_credential_state s ON s.account_id=a.id AND s.plugin_version_id=v.id AND s.connection=`+pluginCredentialConnectionSQL+`
WHERE a.id=ANY($1) AND a.deleted_at IS NULL AND a.type='apikey'
AND (a.platform='openai' AND v.capabilities->>'provider'='true' OR v.capabilities->>'task'='true')`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]service.PluginCredentialScheduling, 0, len(ids))
	for rows.Next() {
		var state service.PluginCredentialScheduling
		if err = rows.Scan(&state.AccountID, &state.PluginVersionID, &state.Connection, &state.Enabled, &state.RequiresLogin, &state.CanRefresh, &state.Public); err != nil {
			return nil, err
		}
		result = append(result, state)
	}
	return result, rows.Err()
}
