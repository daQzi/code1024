-- Keep legacy ingress instances unchanged. Egress is explicitly restricted
-- to Task requests until the ordinary relay's attempt stages are integrated.
ALTER TABLE sub2api_request_plugin_instances
 ADD COLUMN formats JSONB NOT NULL DEFAULT '[]'::jsonb
 CHECK (jsonb_typeof(formats)='array');
ALTER TABLE sub2api_request_plugin_instances
 DROP CONSTRAINT sub2api_request_plugin_instances_stages_check;
ALTER TABLE sub2api_request_plugin_instances
 ADD CONSTRAINT sub2api_request_plugin_instances_stages_check
 CHECK (stages IN ('["ingress"]'::jsonb,'["egress"]'::jsonb,'["ingress","egress"]'::jsonb)
 AND (NOT stages ? 'egress' OR formats='["task"]'::jsonb));

-- Private, encrypted immutable policy/config references; no source or body.
-- Old queued tasks retain their original policy (no newly applied egress).
ALTER TABLE sub2api_generation_tasks
 ADD COLUMN request_plugins_encrypted TEXT NOT NULL DEFAULT ''
 CHECK (octet_length(request_plugins_encrypted)<=524288);
