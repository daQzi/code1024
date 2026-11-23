-- Only newly enrolled native tasks have an immutable financial snapshot and
-- original connection identity. Do not invent prices or units for old jobs.
CREATE TABLE IF NOT EXISTS sub2api_native_video_settlements (
 task_id VARCHAR(77) PRIMARY KEY REFERENCES sub2api_native_video_tasks(id),
 status VARCHAR(24) NOT NULL DEFAULT 'awaiting_usage'
  CHECK(status IN('awaiting_usage','ready','settled','logged','ignored','review')),
 final_usage JSONB CHECK(final_usage IS NULL OR
  (jsonb_typeof(final_usage)='object' AND octet_length(final_usage::text)<=1024)),
 next_run_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 lease_token VARCHAR(64) NOT NULL DEFAULT '',
 lease_until TIMESTAMPTZ,
 failures INTEGER NOT NULL DEFAULT 0,
 last_error_code VARCHAR(64) NOT NULL DEFAULT '',
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS sub2api_native_video_settlements_due
 ON sub2api_native_video_settlements(next_run_at,task_id) WHERE status='ready';

CREATE OR REPLACE VIEW sub2api_task_log_records AS
 SELECT t.id,t.user_id,t.api_key_id,t.group_id,t.account_id,t.upstream_id,
 t.model,t.platform,t.protocol,t.action,v.plugin_key,v.version AS plugin_version,
 t.status,t.delivery_status,t.billing_status,t.progress,t.hold_amount,t.actual_amount,
 t.error_code,t.error_message,t.created_at,t.updated_at,t.finished_at,t.public_fields,'plugin'::text AS source
 FROM sub2api_generation_tasks t JOIN sub2api_js_plugin_versions v ON v.id=t.plugin_version_id
 UNION ALL
 SELECT t.id,t.user_id,t.api_key_id,t.group_id,t.account_id,t.upstream_id,
 t.model,t.platform,t.protocol,t.action,('native-' || t.protocol)::text,''::text,
 t.status,t.delivery_status,
 CASE WHEN s.status='ignored' OR t.pending_billing->'financial'->>'mode' IN('log_only','rate_only') THEN 'not_charged'
 WHEN billing.applied THEN 'settled' WHEN s.status='review' THEN 'review'
 WHEN s.status='ready' THEN 'pending' ELSE 'unconfirmed' END,
 CASE WHEN t.status='succeeded' THEN 100 ELSE 0 END,0::numeric,
 CASE WHEN billing.applied AND COALESCE(t.pending_billing->'financial'->>'mode','standard')='standard' THEN u.actual_cost ELSE NULL::numeric END,
 CASE WHEN t.status='failed' THEN 'native_video_failed' WHEN s.status='review' THEN s.last_error_code ELSE '' END,
 CASE WHEN t.status='failed' THEN 'The upstream reported that this video task did not complete.'
 WHEN s.status='review' THEN 'The native video settlement requires administrator review; no new generation will be submitted.' ELSE '' END,
 t.created_at,t.updated_at,t.finished_at,t.public_fields,'native'::text
 FROM sub2api_native_video_tasks t
 LEFT JOIN sub2api_native_video_settlements s ON s.task_id=t.id
 LEFT JOIN usage_logs u ON u.request_id=t.ledger_request_id AND u.api_key_id=t.api_key_id
 AND u.user_id=t.user_id AND u.account_id=t.account_id AND u.group_id=t.group_id
 LEFT JOIN LATERAL (
 SELECT EXISTS(SELECT 1 FROM usage_billing_dedup d WHERE d.request_id=t.ledger_request_id AND d.api_key_id=t.api_key_id)
 OR EXISTS(SELECT 1 FROM usage_billing_dedup_archive d WHERE d.request_id=t.ledger_request_id AND d.api_key_id=t.api_key_id) AS applied
 ) billing ON u.id IS NOT NULL;
