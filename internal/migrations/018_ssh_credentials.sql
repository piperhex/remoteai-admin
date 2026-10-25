ALTER TABLE "synced_ssh_profiles"
    ADD COLUMN IF NOT EXISTS "encryptedCredentials" text;
