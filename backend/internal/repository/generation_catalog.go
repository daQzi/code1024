package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/newapi/jsplugin"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *newAPIPluginRepository) ListGenerationCatalog(ctx context.Context) ([]service.GenerationCatalogAccount, error) {
	// Read only catalog fields, never source or account API keys. The bound
	// revision remains visible after switching the plugin's active revision.
	rows, err := r.db.QueryContext(ctx, `SELECT ag.group_id,a.platform,
 jsonb_build_object('models',v.meta->'models','protocols',v.meta->'protocols'),
 COALESCE(a.credentials->'model_mapping','{}'::jsonb),COALESCE(a.credentials->'newapi_video_pricing','{}'::jsonb)
 FROM accounts a JOIN account_groups ag ON ag.account_id=a.id
 JOIN sub2api_js_plugin_versions v ON a.credentials->>'newapi_plugin_version_id'=v.id::text
 JOIN sub2api_js_plugins p ON p.plugin_key=v.plugin_key
 WHERE a.deleted_at IS NULL AND a.status='active' AND a.schedulable AND p.enabled
 ORDER BY a.priority,a.id,ag.group_id LIMIT 1025`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]service.GenerationCatalogAccount, 0)
	for rows.Next() {
		if len(result) >= 1024 {
			return nil, errors.New("plugin model catalog exceeds 1024 account/group bindings")
		}
		var item service.GenerationCatalogAccount
		var metaRaw, mappingRaw, pricingRaw []byte
		if err = rows.Scan(&item.GroupID, &item.Platform, &metaRaw, &mappingRaw, &pricingRaw); err != nil {
			return nil, err
		}
		var meta jsplugin.Meta
		if err = json.Unmarshal(metaRaw, &meta); err != nil {
			return nil, err
		}
		found := false
		for _, protocol := range meta.Protocols {
			if protocol.Name == "openai_video" {
				found = true
				item.Models = protocol.Models
				if len(item.Models) == 0 {
					item.Models = meta.Models
				}
			}
		}
		if !found {
			continue
		}
		if err = json.Unmarshal(mappingRaw, &item.Mapping); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(pricingRaw, &item.Rules); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
