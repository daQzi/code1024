-- Keep request-time public tool prices with the existing usage row. NULL
-- preserves unknown historical details; no historical prices are inferred.
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS provider_tool_billing JSONB;

COMMENT ON COLUMN usage_logs.provider_tool_billing IS
    'Immutable Provider tool selling-price breakdown included in usage costs, not an additional debit';
