BEGIN;
-- Current account avatar only; no messages, original images, timestamps, or profile history.
CREATE TABLE IF NOT EXISTS lounge_profiles (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    avatar text NOT NULL DEFAULT '',
    CONSTRAINT lounge_profile_avatar_size CHECK (octet_length(avatar) <= 21870)
);
COMMIT;
