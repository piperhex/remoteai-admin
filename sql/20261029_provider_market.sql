-- Apply before enabling the Provider market. Existing personal Providers are untouched.
BEGIN;
ALTER TABLE synced_providers ADD COLUMN IF NOT EXISTS "balanceScript" jsonb;
CREATE TABLE IF NOT EXISTS provider_market_items (
    id uuid PRIMARY KEY,
    owner_id uuid REFERENCES users(id) ON DELETE CASCADE,
    official boolean NOT NULL DEFAULT false,
    published boolean NOT NULL DEFAULT true,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    sort_order bigint CHECK (sort_order BETWEEN 0 AND 1000000),
    draft jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((official AND owner_id IS NULL) OR (NOT official AND owner_id IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS provider_market_owner ON provider_market_items(owner_id);
CREATE INDEX IF NOT EXISTS provider_market_order ON provider_market_items(published, sort_order, created_at);
CREATE TABLE IF NOT EXISTS provider_market_usage (
    item_id uuid NOT NULL REFERENCES provider_market_items(id) ON DELETE CASCADE,
    owner_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (item_id, owner_id)
);
COMMIT;
