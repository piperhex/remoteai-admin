CREATE TABLE IF NOT EXISTS "synced_ssh_profiles" (
    "ownerId" uuid NOT NULL REFERENCES "users"("id") ON DELETE CASCADE,
    "profileId" varchar(128) NOT NULL,
    "revision" bigint NOT NULL CHECK ("revision" > 0),
    "config" jsonb,
    PRIMARY KEY ("ownerId", "profileId")
);
