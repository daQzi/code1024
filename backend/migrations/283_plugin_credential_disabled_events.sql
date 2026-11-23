-- A native automatic disable belongs to the exact persisted account mutation.
-- Manual account changes invalidate this timestamp; a hook cannot undo them.
ALTER TABLE sub2api_plugin_credential_state
 ADD COLUMN IF NOT EXISTS native_disabled_at TIMESTAMPTZ,
 ADD COLUMN IF NOT EXISTS native_disable_pending BOOLEAN NOT NULL DEFAULT FALSE,
 ADD COLUMN IF NOT EXISTS native_disabled_reason TEXT NOT NULL DEFAULT '',
 ADD COLUMN IF NOT EXISTS last_disabled_run_at TIMESTAMPTZ;
