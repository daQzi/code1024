-- Task quota projection and its Redis delivery commit with financial settlement.
-- The cursor says which Task deltas are already included in an absolute usage
-- snapshot; ordinary Redis snapshot flushing must retain all later deltas.
ALTER TABLE user_platform_quotas
    ADD COLUMN IF NOT EXISTS generation_cursor BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS generation_daily_reset_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS generation_weekly_reset_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS generation_monthly_reset_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS sub2api_generation_quota_outbox (
    id BIGSERIAL PRIMARY KEY,
    task_id VARCHAR(64) NOT NULL UNIQUE,
    quota_id BIGINT NOT NULL REFERENCES user_platform_quotas(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform VARCHAR(32) NOT NULL,
    amount NUMERIC(20,8) NOT NULL CHECK (amount > 0),
    daily_start TIMESTAMPTZ NOT NULL,
    weekly_start TIMESTAMPTZ NOT NULL,
    monthly_start TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    delivered_at TIMESTAMPTZ,
    next_run_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    lease_token VARCHAR(64) NOT NULL DEFAULT '',
    lease_until TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS generation_quota_outbox_due
    ON sub2api_generation_quota_outbox(next_run_at,id) WHERE delivered_at IS NULL;
CREATE INDEX IF NOT EXISTS generation_quota_outbox_projection
    ON sub2api_generation_quota_outbox(quota_id,id);
CREATE INDEX IF NOT EXISTS generation_quota_outbox_order
    ON sub2api_generation_quota_outbox(quota_id,id) WHERE delivered_at IS NULL;
