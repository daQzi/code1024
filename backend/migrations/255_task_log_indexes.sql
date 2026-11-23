-- Console task history uses submission time, independently of API-key cursors.
CREATE INDEX IF NOT EXISTS sub2api_generation_tasks_log_time
 ON sub2api_generation_tasks(created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS sub2api_generation_tasks_log_owner
 ON sub2api_generation_tasks(user_id,created_at DESC,id DESC);
