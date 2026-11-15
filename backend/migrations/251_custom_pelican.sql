-- Custom UI benchmark module; additive, independent of gateway billing tables.
-- Preserve this filename and checksum after applying it to a database.

CREATE TABLE IF NOT EXISTS pelican_results (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    reasoning_effort TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'ok',
    html TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_pelican_results_group_created
    ON pelican_results (group_id, created_at DESC);

CREATE TABLE IF NOT EXISTS pelican_config (
    id INTEGER PRIMARY KEY,
    data JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
