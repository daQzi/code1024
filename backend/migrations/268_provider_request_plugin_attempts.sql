-- Account placement is the host equivalent of a channel-bound instance.
-- Ordinary attempt stages are initially restricted to explicit Provider
-- accounts; existing global/group Task policies retain their old contract.
ALTER TABLE sub2api_request_plugin_instances
 ADD COLUMN account_ids JSONB NOT NULL DEFAULT '[]'::jsonb
 CHECK (jsonb_typeof(account_ids)='array' AND jsonb_array_length(account_ids)<=100);
ALTER TABLE sub2api_request_plugin_instances
 DROP CONSTRAINT sub2api_request_plugin_instances_scope_check;
ALTER TABLE sub2api_request_plugin_instances
 ADD CONSTRAINT sub2api_request_plugin_instances_scope_check CHECK (scope IN ('global','group','account'));
ALTER TABLE sub2api_request_plugin_instances
 DROP CONSTRAINT sub2api_request_plugin_instances_stages_check;
ALTER TABLE sub2api_request_plugin_instances
 ADD CONSTRAINT sub2api_request_plugin_instances_stages_check CHECK (
 (scope='account' AND group_ids='[]'::jsonb AND jsonb_array_length(account_ids)>0
  AND jsonb_array_length(stages) BETWEEN 1 AND 3 AND stages <@ '["request","outbound","egress"]'::jsonb
  AND jsonb_array_length(formats) BETWEEN 1 AND 2 AND formats <@ '["openai","openai_responses"]'::jsonb)
 OR
 (scope IN ('global','group') AND account_ids='[]'::jsonb
  AND jsonb_array_length(stages) BETWEEN 1 AND 3 AND stages <@ '["ingress","egress","report"]'::jsonb
  AND (NOT (stages ? 'egress' OR stages ? 'report') OR formats='["task"]'::jsonb)));
