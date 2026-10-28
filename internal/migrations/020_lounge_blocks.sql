CREATE TABLE IF NOT EXISTS lounge_user_blocks (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    blocked_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, blocked_user_id),
    CHECK (user_id <> blocked_user_id)
);
CREATE INDEX IF NOT EXISTS lounge_user_blocks_target ON lounge_user_blocks(blocked_user_id);
COMMENT ON TABLE lounge_user_blocks IS 'Current user blocking preferences only; no messages, presence or history';
