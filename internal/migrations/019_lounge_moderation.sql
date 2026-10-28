CREATE TABLE IF NOT EXISTS lounge_user_policies (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    moderator boolean NOT NULL DEFAULT false,
    muted_until timestamptz
);
COMMENT ON TABLE lounge_user_policies IS 'Current lounge permissions and mute expiry only; no messages or history';
