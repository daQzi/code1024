-- Explicit HTTP reports for global/group placements, including native relay
-- accounts. Existing task-only scopes and account stage boundaries stay fixed.
ALTER TABLE sub2api_request_plugin_instances
 DROP CONSTRAINT sub2api_request_plugin_instances_stages_check;
ALTER TABLE sub2api_request_plugin_instances
 ADD CONSTRAINT sub2api_request_plugin_instances_stages_check CHECK (
 (scope='account' AND group_ids='[]'::jsonb AND jsonb_array_length(account_ids)>0
  AND jsonb_array_length(stages) BETWEEN 1 AND 6 AND stages <@ '["ingress","request","outbound","egress","reply","report"]'::jsonb
  AND jsonb_array_length(formats) BETWEEN 1 AND 2 AND formats <@ '["openai","openai_responses"]'::jsonb)
 OR
 (scope IN ('global','group') AND account_ids='[]'::jsonb
  AND jsonb_array_length(stages) BETWEEN 1 AND 3 AND stages <@ '["ingress","egress","report"]'::jsonb
  AND (NOT (stages ? 'egress') OR formats='["task"]'::jsonb)
  AND (NOT (stages ? 'report') OR formats='["task"]'::jsonb
       OR (jsonb_array_length(formats) BETWEEN 1 AND 2 AND formats <@ '["openai","openai_responses"]'::jsonb))));
