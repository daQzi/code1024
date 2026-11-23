-- Dedicated final HTTP outcomes for synchronous Images, Embeddings and
-- native Voice TTS/STT. Retain historical logs before their own cutover;
-- repeated migration must not reset any protocol's epoch.
ALTER TABLE sub2api_relay_metric_epoch
 ADD COLUMN IF NOT EXISTS media_http_started_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
