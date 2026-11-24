-- Index plugin_version_id foreign keys so custom version deletion checks and
-- PostgreSQL FK verification avoid sequential table scans under the plugin lock.
CREATE INDEX IF NOT EXISTS idx_request_plugin_instances_plugin_version
    ON sub2api_request_plugin_instances(plugin_version_id);

CREATE INDEX IF NOT EXISTS idx_generation_tasks_plugin_version
    ON sub2api_generation_tasks(plugin_version_id);

CREATE INDEX IF NOT EXISTS idx_plugin_credential_state_plugin_version
    ON sub2api_plugin_credential_state(plugin_version_id);

CREATE INDEX IF NOT EXISTS idx_plugin_credential_revocations_plugin_version
    ON sub2api_plugin_credential_revocations(plugin_version_id);
