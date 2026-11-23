-- Never grant new outbound access to an existing instance. Task egress pins
-- this opt-in with its immutable policy snapshot; old snapshots remain offline.
ALTER TABLE sub2api_request_plugin_instances
 ADD COLUMN network_enabled BOOLEAN NOT NULL DEFAULT FALSE,
 ADD CONSTRAINT sub2api_request_plugin_instances_network_check
 CHECK (NOT network_enabled OR stages ? 'ingress' OR stages ? 'egress');
