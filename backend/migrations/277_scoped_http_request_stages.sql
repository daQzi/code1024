-- Use the same request-stage host for global/group/account HTTP placements.
-- Task egress remains separately scoped and cannot opt into HTTP mutations.
ALTER TABLE sub2api_request_plugin_instances
 DROP CONSTRAINT sub2api_request_plugin_instances_stages_check;
ALTER TABLE sub2api_request_plugin_instances
 ADD CONSTRAINT sub2api_request_plugin_instances_stages_check CHECK (
 (scope='account' AND group_ids='[]'::jsonb AND jsonb_array_length(account_ids)>0
  AND jsonb_array_length(stages) BETWEEN 1 AND 6 AND stages <@ '["ingress","request","outbound","egress","reply","report"]'::jsonb
  AND jsonb_array_length(formats) BETWEEN 1 AND 2 AND formats <@ '["openai","openai_responses"]'::jsonb)
 OR
 (scope IN ('global','group') AND account_ids='[]'::jsonb
  AND jsonb_array_length(stages) BETWEEN 1 AND 6 AND stages <@ '["ingress","request","outbound","egress","reply","report"]'::jsonb
  AND (NOT (stages ?| ARRAY['request','outbound','reply'])
       OR (jsonb_array_length(formats) BETWEEN 1 AND 2 AND formats <@ '["openai","openai_responses"]'::jsonb))
  AND (NOT (stages ?| ARRAY['egress','report']) OR formats='["task"]'::jsonb
       OR (jsonb_array_length(formats) BETWEEN 1 AND 2 AND formats <@ '["openai","openai_responses"]'::jsonb))));
