ALTER TABLE sub2api_generation_tasks
 ADD COLUMN IF NOT EXISTS origins JSONB NOT NULL DEFAULT '[]'::jsonb
 CHECK (jsonb_typeof(origins)='array' AND jsonb_array_length(origins)<=16 AND octet_length(origins::text)<=1048576);

-- Parent snapshots are private and survive restarts. No historical parent is
-- guessed from an upstream ID or from the body of an older request.
CREATE INDEX IF NOT EXISTS sub2api_generation_decoder_history
 ON sub2api_generation_tasks(user_id,api_key_id,group_id,platform,plugin_version_id,model,upstream_model);
