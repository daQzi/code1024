-- Credentials and login-flow secrets use the host's existing secret cipher.
-- They belong to an existing account and fixed plugin revision. No endpoint,
-- key, proxy, routing or price is duplicated here.
CREATE TABLE IF NOT EXISTS sub2api_plugin_credential_state (
 account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 plugin_version_id BIGINT NOT NULL REFERENCES sub2api_js_plugin_versions(id),
 connection JSONB NOT NULL,
 revision BIGINT NOT NULL DEFAULT 1,
 encrypted TEXT NOT NULL DEFAULT '' CHECK (octet_length(encrypted) <= 262144),
 public JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (octet_length(public::text) <= 32768),
 next_run_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 lease_token UUID,
 lease_until TIMESTAMPTZ,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS sub2api_plugin_credential_due
 ON sub2api_plugin_credential_state(next_run_at,account_id);
CREATE INDEX IF NOT EXISTS accounts_plugin_credential_candidates
 ON accounts ((credentials->>'newapi_plugin_version_id'))
 WHERE deleted_at IS NULL AND credentials ? 'newapi_plugin_version_id';
