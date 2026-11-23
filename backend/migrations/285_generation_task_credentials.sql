-- Tokens remain in encrypted account storage. Tasks retain a non-secret login
-- identity and hashed connection only, so refresh never transfers ownership.
ALTER TABLE sub2api_generation_tasks
 ADD COLUMN IF NOT EXISTS credential_binding JSONB NOT NULL DEFAULT '{}'::jsonb
 CHECK (jsonb_typeof(credential_binding)='object' AND octet_length(credential_binding::text)<=4096);

-- No historical managed identity is guessed; pre-upgrade tasks used static keys.
