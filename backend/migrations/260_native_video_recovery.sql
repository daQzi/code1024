-- Private create-time billing inputs for legacy native video requests. NULL
-- means no reliable snapshot was captured; never reconstruct it from public
-- fields or today's model settings. Execution and billing stay on the native
-- path, without plugin reservations or worker claims.
ALTER TABLE sub2api_native_video_tasks
 ADD COLUMN IF NOT EXISTS pending_billing JSONB
 CHECK (pending_billing IS NULL OR
  (jsonb_typeof(pending_billing)='object' AND octet_length(pending_billing::text)<=8192));

-- A lookup must consider every matching account, not pick the latest one if an
-- upstream reused its task ID. Two matches are enough to reject ambiguity.
CREATE INDEX IF NOT EXISTS sub2api_native_video_tasks_recovery
 ON sub2api_native_video_tasks(user_id,api_key_id,group_id,protocol,upstream_id);
