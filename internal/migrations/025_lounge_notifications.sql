-- Only muted conversations are stored. The empty conversation ID identifies the public group.
CREATE TABLE IF NOT EXISTS lounge_notification_settings (
    owner_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    conversation_id text NOT NULL,
    peer_id uuid GENERATED ALWAYS AS (NULLIF(conversation_id, '')::uuid) STORED
        REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (owner_id, conversation_id),
    CONSTRAINT lounge_notification_target CHECK (
        conversation_id = '' OR (conversation_id = peer_id::text AND owner_id <> peer_id)
    )
);
