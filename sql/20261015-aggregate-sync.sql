CREATE TABLE IF NOT EXISTS "synced_aggregates" (
    "ownerId" uuid NOT NULL REFERENCES "users"("id") ON DELETE CASCADE,
    "aggregateId" varchar(64) NOT NULL,
    "modifiedAt" timestamptz NOT NULL,
    "config" jsonb,
    PRIMARY KEY ("ownerId", "aggregateId")
);
