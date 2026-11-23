-- An ingress/egress network grant must never automatically authorize sending
-- generated model replies. Reply access has its own explicit opt-in.
ALTER TABLE sub2api_request_plugin_instances
 ADD COLUMN reply_network_enabled BOOLEAN NOT NULL DEFAULT FALSE,
 ADD CONSTRAINT sub2api_request_plugin_instances_reply_network_check
 CHECK (NOT reply_network_enabled OR stages ? 'reply');
