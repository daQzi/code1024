-- Preparation is safe to resume: no paid generation has crossed the submit
-- boundary. Existing tasks retain the conservative lost-submit-lease behavior.
ALTER TABLE sub2api_generation_tasks
    ADD COLUMN IF NOT EXISTS preparation_status VARCHAR(16) NOT NULL DEFAULT ''
        CHECK (preparation_status IN ('','pending','ready','cleanup','cleaned')),
    ADD COLUMN IF NOT EXISTS preparation_encrypted TEXT NOT NULL DEFAULT ''
        CHECK (octet_length(preparation_encrypted) <= 98304);

CREATE INDEX IF NOT EXISTS generation_preparation_cleanup
    ON sub2api_generation_tasks(next_run_at,id)
    WHERE preparation_status='cleanup' AND billing_status='settled';
