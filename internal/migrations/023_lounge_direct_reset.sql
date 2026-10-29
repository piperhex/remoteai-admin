ALTER TABLE lounge_direct_permissions
    ADD COLUMN IF NOT EXISTS generation bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS fresh boolean NOT NULL DEFAULT false;
