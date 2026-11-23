-- Switch WS turns (including HTTP requests using an upstream WS transport) to
-- final independent telemetry. Keep the original HTTP cutover and historical
-- WS logs; repeated migrations must never reset either epoch.
ALTER TABLE sub2api_relay_metric_epoch
 ADD COLUMN IF NOT EXISTS websocket_started_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
