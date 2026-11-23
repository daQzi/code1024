CREATE TABLE IF NOT EXISTS sub2api_generation_tasks (
 id VARCHAR(46) PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id),
 api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
 group_id BIGINT NOT NULL REFERENCES groups(id),
 account_id BIGINT NOT NULL REFERENCES accounts(id),
 plugin_version_id BIGINT NOT NULL REFERENCES sub2api_js_plugin_versions(id),
 idempotency_key VARCHAR(200) NOT NULL,
 fingerprint VARCHAR(64) NOT NULL,
 model VARCHAR(256) NOT NULL,
 upstream_model VARCHAR(256) NOT NULL,
 platform VARCHAR(32) NOT NULL,
 protocol VARCHAR(64) NOT NULL,
 action VARCHAR(64) NOT NULL,
 request JSONB NOT NULL CHECK(octet_length(request::text)<=1048576),
 public_fields JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(octet_length(public_fields::text)<=16384),
 config_encrypted TEXT NOT NULL DEFAULT '',
 connection JSONB NOT NULL,
 upstream_id TEXT NOT NULL DEFAULT '',
 state JSONB NOT NULL DEFAULT '{}'::jsonb,
 data JSONB NOT NULL DEFAULT '{}'::jsonb,
 usage JSONB NOT NULL DEFAULT '{}'::jsonb,
 price JSONB NOT NULL,
 funding JSONB NOT NULL,
 hold_amount NUMERIC(20,8) NOT NULL CHECK(hold_amount>=0),
 actual_amount NUMERIC(20,8),
 reconciled_amount NUMERIC(20,8) CHECK(reconciled_amount>=0 AND reconciled_amount<=hold_amount),
 reviewed_by BIGINT REFERENCES users(id),
 review_reason TEXT NOT NULL DEFAULT '',
 source_cost JSONB,
 status VARCHAR(32) NOT NULL DEFAULT 'queued' CHECK(status IN('queued','submitting','submission_uncertain','running','succeeded','failed')),
 delivery_status VARCHAR(16) NOT NULL DEFAULT 'pending' CHECK(delivery_status IN('pending','ready','failed')),
 billing_status VARCHAR(16) NOT NULL DEFAULT 'held' CHECK(billing_status IN('held','pending','settled','review')),
 progress INTEGER NOT NULL DEFAULT 0 CHECK(progress BETWEEN 0 AND 100),
 error_code VARCHAR(128) NOT NULL DEFAULT '',
 error_message TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 finished_at TIMESTAMPTZ,
 next_run_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 lease_token VARCHAR(64) NOT NULL DEFAULT '',
 lease_until TIMESTAMPTZ,
 attempts INTEGER NOT NULL DEFAULT 0,
 UNIQUE(api_key_id,idempotency_key)
);
CREATE INDEX IF NOT EXISTS sub2api_generation_tasks_due ON sub2api_generation_tasks(next_run_at,id)
 WHERE billing_status IN('held','pending') AND status<>'submission_uncertain';
CREATE INDEX IF NOT EXISTS sub2api_generation_tasks_owner ON sub2api_generation_tasks(user_id,api_key_id,id DESC);
CREATE INDEX IF NOT EXISTS sub2api_generation_tasks_model_finished ON sub2api_generation_tasks(group_id,platform,model,finished_at)
 WHERE finished_at IS NOT NULL;
CREATE TABLE IF NOT EXISTS sub2api_generation_events (
 id BIGSERIAL PRIMARY KEY,
 task_id VARCHAR(46) NOT NULL REFERENCES sub2api_generation_tasks(id),
 event_key VARCHAR(128) NOT NULL,
 event_type VARCHAR(64) NOT NULL,
 payload JSONB NOT NULL DEFAULT '{}'::jsonb,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE(task_id,event_key)
);
