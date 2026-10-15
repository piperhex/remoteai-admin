CREATE TABLE IF NOT EXISTS "device_port_mappings" (
    "ownerId" uuid NOT NULL,
    "deviceId" uuid NOT NULL,
    "revision" bigint NOT NULL DEFAULT 0,
    "mappings" jsonb NOT NULL DEFAULT '[]'::jsonb,
    PRIMARY KEY ("ownerId", "deviceId"),
    FOREIGN KEY ("ownerId", "deviceId") REFERENCES "remote_devices"("ownerId", "deviceId") ON DELETE CASCADE
);
