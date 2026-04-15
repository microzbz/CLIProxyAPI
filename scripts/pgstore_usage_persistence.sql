-- CLIProxyAPI PGStore usage persistence migration
--
-- Purpose:
-- 1. Create the settings_store table when it does not exist.
-- 2. Create the usage_store table for persisted usage statistics.
-- 3. Seed the default retention setting to 15 days.
--
-- Notes:
-- - This script is idempotent and can be executed multiple times.
-- - Default schema is public. If you use another schema, adjust the schema-qualified names below.

BEGIN;

CREATE TABLE IF NOT EXISTS public.settings_store (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS public.usage_store (
    id BIGSERIAL PRIMARY KEY,
    dedup_key TEXT NOT NULL UNIQUE,
    api_name TEXT NOT NULL,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    auth_id TEXT NOT NULL DEFAULT '',
    auth_index TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT '',
    requested_at TIMESTAMPTZ NOT NULL,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    failed BOOLEAN NOT NULL DEFAULT FALSE,
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    reasoning_tokens BIGINT NOT NULL DEFAULT 0,
    cached_tokens BIGINT NOT NULL DEFAULT 0,
    total_tokens BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS usage_store_requested_at_idx
    ON public.usage_store (requested_at DESC);

CREATE INDEX IF NOT EXISTS usage_store_api_model_idx
    ON public.usage_store (api_name, model);

INSERT INTO public.settings_store (key, value, created_at, updated_at)
VALUES ('usage_retention_days', '15', NOW(), NOW())
ON CONFLICT (key) DO NOTHING;

COMMIT;
