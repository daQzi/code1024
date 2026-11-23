-- Legacy native jobs remain on their original ledger. This queue may only GET
-- status; it contains no credentials, submit command or financial operation.
CREATE TABLE IF NOT EXISTS sub2api_native_video_polls (
 task_id VARCHAR(77) PRIMARY KEY REFERENCES sub2api_native_video_tasks(id),
 connection_hash VARCHAR(64) NOT NULL CHECK(connection_hash ~ '^[0-9a-f]{64}$'),
 monitor_status VARCHAR(16) NOT NULL DEFAULT 'active' CHECK(monitor_status IN('active','complete','review')),
 next_run_at TIMESTAMPTZ NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 lease_token VARCHAR(64) NOT NULL DEFAULT '',
 lease_until TIMESTAMPTZ,
 attempts INTEGER NOT NULL DEFAULT 0,
 failures INTEGER NOT NULL DEFAULT 0,
 last_error_code VARCHAR(64) NOT NULL DEFAULT '',
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS sub2api_native_video_polls_due
 ON sub2api_native_video_polls(next_run_at,task_id) WHERE monitor_status='active';
-- Deliberately no backfill: old tasks have no captured connection identity.
