-- Realtime/Live turns are observed from actual response terminal events,
-- independently from connection duration, partial bills and session closure.
ALTER TABLE sub2api_relay_metric_epoch
 ADD COLUMN IF NOT EXISTS media_ws_started_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
