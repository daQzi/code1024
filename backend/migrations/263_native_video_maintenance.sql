CREATE TABLE IF NOT EXISTS sub2api_native_video_actions (
 id BIGSERIAL PRIMARY KEY,
 task_id VARCHAR(77) NOT NULL REFERENCES sub2api_native_video_tasks(id),
 actor_id BIGINT NOT NULL REFERENCES users(id),
 action VARCHAR(32) NOT NULL CHECK(action IN('resume_poll','retry_settlement')),
 reason VARCHAR(2000) NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS sub2api_native_video_actions_task
 ON sub2api_native_video_actions(task_id,created_at,id);
