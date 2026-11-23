-- Dedicated final HTTP relay outcomes, independent of billing and ops logging.
-- The epoch prevents post-upgrade partial usage from becoming success samples
-- when telemetry is unavailable. Pre-upgrade log history remains readable.
CREATE TABLE IF NOT EXISTS sub2api_relay_metric_epoch (
 singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(singleton),
 started_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO sub2api_relay_metric_epoch(singleton) VALUES(TRUE) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS sub2api_relay_performance_samples (
 id UUID PRIMARY KEY,
 group_id BIGINT NOT NULL CHECK(group_id>0),
 platform VARCHAR(32) NOT NULL CHECK(platform<>''),
 model VARCHAR(100) NOT NULL CHECK(model<>''),
 success BOOLEAN NOT NULL,
 duration_ms BIGINT NOT NULL CHECK(duration_ms BETWEEN 0 AND 604800000),
 first_token_ms BIGINT CHECK(first_token_ms BETWEEN 0 AND 604800000),
 output_tokens BIGINT NOT NULL DEFAULT 0 CHECK(output_tokens>=0),
 created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS sub2api_relay_performance_samples_scope
 ON sub2api_relay_performance_samples(group_id,platform,model,created_at);
CREATE INDEX IF NOT EXISTS sub2api_relay_performance_samples_expiry
 ON sub2api_relay_performance_samples(created_at,id);
CREATE INDEX IF NOT EXISTS sub2api_native_video_tasks_metrics
 ON sub2api_native_video_tasks(group_id,platform,model,finished_at) WHERE finished_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS sub2api_native_video_tasks_metric_ledger
 ON sub2api_native_video_tasks(api_key_id,group_id,account_id,ledger_request_id);
