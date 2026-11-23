-- Retire grants in the same transaction as account deletion or connection
-- changes. Jobs contain the existing encrypted state, never a new plaintext
-- copy of credentials. The account itself is still the only live connection.
CREATE TABLE IF NOT EXISTS sub2api_plugin_credential_revocations (
 id BIGSERIAL PRIMARY KEY,
 account_id BIGINT NOT NULL,
 source_revision BIGINT NOT NULL,
 source_fingerprint TEXT NOT NULL,
 plugin_version_id BIGINT NOT NULL REFERENCES sub2api_js_plugin_versions(id),
 base_url TEXT NOT NULL,
 config_encrypted TEXT NOT NULL,
 state_encrypted TEXT NOT NULL,
 proxy_id BIGINT,
 proxy_fingerprint TEXT NOT NULL DEFAULT '',
 identity_fingerprint TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','completed','protected','attention')),
 attempts INTEGER NOT NULL DEFAULT 0,
 last_error TEXT NOT NULL DEFAULT '',
 next_run_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 lease_token UUID,
 lease_until TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 finished_at TIMESTAMPTZ,
 UNIQUE(account_id,source_fingerprint)
);
CREATE INDEX IF NOT EXISTS sub2api_plugin_credential_revocations_due
 ON sub2api_plugin_credential_revocations(next_run_at,id) WHERE status='pending';

CREATE OR REPLACE FUNCTION sub2api_retire_plugin_credential() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
 previous_connection JSONB;
 changed BOOLEAN;
BEGIN
 changed := TG_OP='DELETE';
 IF TG_OP='UPDATE' THEN
  changed := OLD.platform IS DISTINCT FROM NEW.platform OR OLD.type IS DISTINCT FROM NEW.type
   OR OLD.proxy_id IS DISTINCT FROM NEW.proxy_id
   OR (OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL)
   OR jsonb_build_array(OLD.credentials->'newapi_plugin_version_id',OLD.credentials->'base_url',OLD.credentials->'api_key',OLD.credentials->'newapi_plugin_config_encrypted')
      IS DISTINCT FROM jsonb_build_array(NEW.credentials->'newapi_plugin_version_id',NEW.credentials->'base_url',NEW.credentials->'api_key',NEW.credentials->'newapi_plugin_config_encrypted');
 END IF;
 IF changed THEN
  previous_connection := jsonb_build_array(OLD.platform,OLD.type,OLD.proxy_id,OLD.credentials->'newapi_plugin_version_id',
   CASE WHEN jsonb_typeof(OLD.credentials->'base_url')='string' THEN encode(sha256(convert_to(OLD.credentials->>'base_url','UTF8')),'hex') END,
   CASE WHEN jsonb_typeof(OLD.credentials->'api_key')='string' THEN encode(sha256(convert_to(OLD.credentials->>'api_key','UTF8')),'hex') END,
   CASE WHEN jsonb_typeof(OLD.credentials->'newapi_plugin_config_encrypted')='string' THEN encode(sha256(convert_to(OLD.credentials->>'newapi_plugin_config_encrypted','UTF8')),'hex') END);
  INSERT INTO sub2api_plugin_credential_revocations(account_id,source_revision,source_fingerprint,plugin_version_id,base_url,config_encrypted,state_encrypted,proxy_id,proxy_fingerprint,identity_fingerprint)
  SELECT s.account_id,s.revision,encode(sha256(convert_to(s.encrypted,'UTF8')),'hex'),s.plugin_version_id,COALESCE(OLD.credentials->>'base_url',''),COALESCE(OLD.credentials->>'newapi_plugin_config_encrypted',''),s.encrypted,OLD.proxy_id,
   COALESCE((SELECT encode(sha256(convert_to(jsonb_build_array(p.protocol,p.host,p.port,COALESCE(p.username,''),COALESCE(p.password,''))::text,'UTF8')),'hex') FROM proxies p WHERE p.id=OLD.proxy_id AND p.deleted_at IS NULL),''),
   CASE WHEN COALESCE(s.public->'credential'->>'account_id','')<>'' THEN
    (SELECT encode(sha256(convert_to(jsonb_build_array(v.plugin_key,s.public->'credential'->>'account_id')::text,'UTF8')),'hex') FROM sub2api_js_plugin_versions v WHERE v.id=s.plugin_version_id)
   ELSE '' END
  FROM sub2api_plugin_credential_state s WHERE s.account_id=OLD.id AND s.connection=previous_connection
   AND s.encrypted<>'' AND jsonb_typeof(s.public->'credential')='object'
  ON CONFLICT(account_id,source_fingerprint) DO NOTHING;
  DELETE FROM sub2api_plugin_credential_state WHERE account_id=OLD.id;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS sub2api_retire_plugin_credential ON accounts;
CREATE TRIGGER sub2api_retire_plugin_credential BEFORE DELETE OR UPDATE OF platform,type,proxy_id,credentials,deleted_at ON accounts
 FOR EACH ROW EXECUTE FUNCTION sub2api_retire_plugin_credential();
