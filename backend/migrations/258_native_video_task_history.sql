-- Native endpoints retain their execution and existing usage ledger. This is
-- lifecycle history only: no worker lease, reservation or settlement commands.
CREATE TABLE IF NOT EXISTS sub2api_native_video_tasks (
 id VARCHAR(77) PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id),
 api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
 group_id BIGINT NOT NULL REFERENCES groups(id),
 account_id BIGINT NOT NULL REFERENCES accounts(id),
 upstream_id VARCHAR(512) NOT NULL,
 ledger_request_id TEXT NOT NULL,
 model VARCHAR(100) NOT NULL,
 platform VARCHAR(32) NOT NULL CHECK(platform IN('grok','openai')),
 protocol VARCHAR(32) NOT NULL CHECK(protocol IN('xai_video','ark_video')),
 action VARCHAR(40) NOT NULL,
 public_fields JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(octet_length(public_fields::text)<=16384),
 status VARCHAR(16) NOT NULL CHECK(status IN('queued','running','succeeded','failed')),
 delivery_status VARCHAR(16) NOT NULL DEFAULT 'pending' CHECK(delivery_status IN('pending','ready','failed')),
 created_at TIMESTAMPTZ NOT NULL,
 updated_at TIMESTAMPTZ NOT NULL,
 running_at TIMESTAMPTZ,
 finished_at TIMESTAMPTZ,
 UNIQUE(api_key_id,group_id,account_id,protocol,upstream_id)
);
CREATE INDEX IF NOT EXISTS sub2api_native_video_tasks_owner_created
 ON sub2api_native_video_tasks(user_id,created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS sub2api_native_video_tasks_created
 ON sub2api_native_video_tasks(created_at DESC,id DESC);

-- A read-only projection combines histories without moving native tasks into
-- the plugin worker or touching their financial effects. NULL cost means no
-- committed billing record has been observed. Usage rows alone can also be
-- written after a billing failure or in simple mode, so they are not proof of
-- settlement. A confirmed zero-cost entry is still settled.
CREATE OR REPLACE VIEW sub2api_task_log_records AS
 SELECT t.id,t.user_id,t.api_key_id,t.group_id,t.account_id,t.upstream_id,
 t.model,t.platform,t.protocol,t.action,v.plugin_key,v.version AS plugin_version,
 t.status,t.delivery_status,t.billing_status,t.progress,t.hold_amount,t.actual_amount,
 t.error_code,t.error_message,t.created_at,t.updated_at,t.finished_at,t.public_fields,'plugin'::text AS source
 FROM sub2api_generation_tasks t JOIN sub2api_js_plugin_versions v ON v.id=t.plugin_version_id
 UNION ALL
 SELECT t.id,t.user_id,t.api_key_id,t.group_id,t.account_id,t.upstream_id,
 t.model,t.platform,t.protocol,t.action,('native-' || t.protocol)::text,''::text,
 t.status,t.delivery_status,CASE WHEN billing.applied THEN 'settled' ELSE 'unconfirmed' END,
 CASE WHEN t.status='succeeded' THEN 100 ELSE 0 END,0::numeric,CASE WHEN billing.applied THEN u.actual_cost ELSE NULL::numeric END,
 CASE WHEN t.status='failed' THEN 'native_video_failed' ELSE '' END,
 CASE WHEN t.status='failed' THEN 'The upstream reported that this video task did not complete.' ELSE '' END,
 t.created_at,t.updated_at,t.finished_at,t.public_fields,'native'::text
 FROM sub2api_native_video_tasks t
 LEFT JOIN usage_logs u ON u.request_id=t.ledger_request_id AND u.api_key_id=t.api_key_id
 AND u.user_id=t.user_id AND u.account_id=t.account_id AND u.group_id=t.group_id
 LEFT JOIN LATERAL (
 SELECT EXISTS(SELECT 1 FROM usage_billing_dedup d WHERE d.request_id=t.ledger_request_id AND d.api_key_id=t.api_key_id)
 OR EXISTS(SELECT 1 FROM usage_billing_dedup_archive d WHERE d.request_id=t.ledger_request_id AND d.api_key_id=t.api_key_id) AS applied
 ) billing ON u.id IS NOT NULL;
