-- Community source is inert until reviewed, published and explicitly installed.
CREATE TABLE IF NOT EXISTS sub2api_plugin_submissions (
 id BIGSERIAL PRIMARY KEY,
 author_id BIGINT NOT NULL REFERENCES users(id),
 plugin_key VARCHAR(30) NOT NULL,
 version VARCHAR(128) NOT NULL,
 sha256 VARCHAR(64) NOT NULL,
 source TEXT NOT NULL CHECK(octet_length(source) BETWEEN 1 AND 1048576),
 readme TEXT NOT NULL DEFAULT '' CHECK(octet_length(readme)<=65536),
 meta JSONB NOT NULL,
 capabilities JSONB NOT NULL,
 status VARCHAR(16) NOT NULL DEFAULT 'draft' CHECK(status IN('draft','submitted','rejected','published','withdrawn')),
 revision BIGINT NOT NULL DEFAULT 1,
 review_reason TEXT NOT NULL DEFAULT '',
 reviewed_by BIGINT REFERENCES users(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 published_at TIMESTAMPTZ,
 UNIQUE(plugin_key,version)
);
CREATE INDEX IF NOT EXISTS sub2api_plugin_submissions_owner ON sub2api_plugin_submissions(author_id,updated_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS sub2api_plugin_submissions_market ON sub2api_plugin_submissions(status,updated_at DESC,id DESC);
CREATE TABLE IF NOT EXISTS sub2api_plugin_submission_events (
 id BIGSERIAL PRIMARY KEY,
 submission_id BIGINT NOT NULL REFERENCES sub2api_plugin_submissions(id),
 actor_id BIGINT NOT NULL REFERENCES users(id),
 action VARCHAR(32) NOT NULL,
 revision BIGINT NOT NULL,
 sha256 VARCHAR(64) NOT NULL,
 reason TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
