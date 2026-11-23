-- Private bounded input media. Generated movies remain on the upstream.
CREATE TABLE IF NOT EXISTS sub2api_generation_files (
 id VARCHAR(46) PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id),
 api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
 group_id BIGINT NOT NULL REFERENCES groups(id),
 filename VARCHAR(255) NOT NULL,
 mime_type VARCHAR(128) NOT NULL,
 size BIGINT NOT NULL CHECK(size BETWEEN 1 AND 33554432),
 sha256 VARCHAR(64) NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW()+INTERVAL '1 hour'
);
CREATE INDEX IF NOT EXISTS sub2api_generation_files_expiry ON sub2api_generation_files(expires_at,id);
CREATE INDEX IF NOT EXISTS sub2api_generation_files_owner ON sub2api_generation_files(user_id,id);
CREATE TABLE IF NOT EXISTS sub2api_generation_file_chunks (
 file_id VARCHAR(46) NOT NULL REFERENCES sub2api_generation_files(id) ON DELETE CASCADE,
 position INTEGER NOT NULL CHECK(position BETWEEN 0 AND 511),
 content BYTEA NOT NULL CHECK(octet_length(content) BETWEEN 1 AND 65536),
 PRIMARY KEY(file_id,position)
);
CREATE TABLE IF NOT EXISTS sub2api_generation_task_files (
 task_id VARCHAR(46) NOT NULL REFERENCES sub2api_generation_tasks(id),
 file_id VARCHAR(46) NOT NULL REFERENCES sub2api_generation_files(id) ON DELETE CASCADE,
 PRIMARY KEY(task_id,file_id)
);
CREATE INDEX IF NOT EXISTS sub2api_generation_task_files_file ON sub2api_generation_task_files(file_id,task_id);
ALTER TABLE sub2api_generation_tasks ADD COLUMN IF NOT EXISTS files JSONB NOT NULL DEFAULT '[]'::jsonb
 CHECK(jsonb_typeof(files)='array' AND jsonb_array_length(files)<=4 AND octet_length(files::text)<=16384);
