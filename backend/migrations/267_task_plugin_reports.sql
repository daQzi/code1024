ALTER TABLE sub2api_request_plugin_instances
 DROP CONSTRAINT sub2api_request_plugin_instances_stages_check;
ALTER TABLE sub2api_request_plugin_instances
 ADD CONSTRAINT sub2api_request_plugin_instances_stages_check CHECK (
 jsonb_typeof(stages)='array' AND jsonb_array_length(stages) BETWEEN 1 AND 3
 AND stages <@ '["ingress","egress","report"]'::jsonb
 AND (NOT (stages ? 'egress' OR stages ? 'report') OR formats='["task"]'::jsonb));

ALTER TABLE sub2api_generation_tasks
 ADD COLUMN request_report_encrypted TEXT NOT NULL DEFAULT ''
 CHECK (octet_length(request_report_encrypted)<=262144);
