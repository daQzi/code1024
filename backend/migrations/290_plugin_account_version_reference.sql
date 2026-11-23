CREATE OR REPLACE FUNCTION sub2api_check_account_plugin_version() RETURNS trigger AS $$
DECLARE
  version_text text;
BEGIN
  IF NEW.deleted_at IS NOT NULL THEN
    RETURN NEW;
  END IF;
  version_text := NEW.credentials->>'newapi_plugin_version_id';
  IF version_text IS NULL OR version_text !~ '^[1-9][0-9]{0,17}$' THEN
    RETURN NEW;
  END IF;
  IF TG_OP = 'UPDATE' THEN
    IF OLD.deleted_at IS NULL AND OLD.credentials->>'newapi_plugin_version_id' IS NOT DISTINCT FROM version_text THEN
      RETURN NEW;
    END IF;
  END IF;
  PERFORM id FROM sub2api_js_plugin_versions WHERE id=version_text::bigint FOR KEY SHARE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'Plugin version % no longer exists', version_text USING ERRCODE='23503';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS sub2api_account_plugin_version_reference ON accounts;
CREATE TRIGGER sub2api_account_plugin_version_reference
BEFORE INSERT OR UPDATE OF credentials, deleted_at ON accounts
FOR EACH ROW EXECUTE FUNCTION sub2api_check_account_plugin_version();
