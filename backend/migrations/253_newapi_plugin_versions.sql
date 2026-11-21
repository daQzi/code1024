-- Immutable JS source versions. A task pins a version ID; activating another
-- version only changes the default for new account bindings/requests.
CREATE TABLE IF NOT EXISTS sub2api_js_plugin_versions (
 id BIGSERIAL PRIMARY KEY,
 plugin_key VARCHAR(30) NOT NULL,
 version VARCHAR(128) NOT NULL,
 source TEXT NOT NULL CHECK (octet_length(source) BETWEEN 1 AND 1048576),
 sha256 VARCHAR(64) NOT NULL,
 origin TEXT NOT NULL DEFAULT '',
 meta JSONB NOT NULL,
 capabilities JSONB NOT NULL,
 installed_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE(plugin_key,version),
 UNIQUE(plugin_key,sha256),
 UNIQUE(plugin_key,id)
);
CREATE TABLE IF NOT EXISTS sub2api_js_plugins (
 plugin_key VARCHAR(30) PRIMARY KEY,
 active_version_id BIGINT NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT FALSE,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 FOREIGN KEY(plugin_key,active_version_id) REFERENCES sub2api_js_plugin_versions(plugin_key,id)
);
