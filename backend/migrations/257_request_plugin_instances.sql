-- Request processing has its own switch and pins an immutable source version.
-- This first host integration explicitly runs JSON ingress only.
CREATE TABLE IF NOT EXISTS sub2api_request_plugin_instances (
 id BIGSERIAL PRIMARY KEY,
 name VARCHAR(128) NOT NULL,
 plugin_version_id BIGINT NOT NULL REFERENCES sub2api_js_plugin_versions(id) ON DELETE RESTRICT,
 enabled BOOLEAN NOT NULL DEFAULT FALSE,
 scope VARCHAR(16) NOT NULL CHECK (scope IN ('global','group')),
 group_ids JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(group_ids)='array'),
 models JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(models)='array'),
 position INTEGER NOT NULL DEFAULT 0,
 stages JSONB NOT NULL DEFAULT '["ingress"]'::jsonb CHECK (stages='["ingress"]'::jsonb),
 config_encrypted TEXT NOT NULL CHECK (octet_length(config_encrypted) BETWEEN 1 AND 131072),
 revision BIGINT NOT NULL DEFAULT 1,
 updated_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS sub2api_request_plugin_runs (
 id BIGSERIAL PRIMARY KEY,
 request_id VARCHAR(128) NOT NULL,
 user_id BIGINT,
 group_id BIGINT,
 model VARCHAR(256) NOT NULL DEFAULT '',
 format VARCHAR(32) NOT NULL,
 dry_run BOOLEAN NOT NULL DEFAULT FALSE,
 records JSONB NOT NULL CHECK (jsonb_typeof(records)='array'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_request_plugin_runs_created ON sub2api_request_plugin_runs(created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS idx_request_plugin_runs_request ON sub2api_request_plugin_runs(request_id);
