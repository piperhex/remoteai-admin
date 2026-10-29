BEGIN;
-- Public nicknames and owner-private contact labels; no conversation content or edit history.
CREATE TABLE IF NOT EXISTS lounge_nicknames (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    nickname text NOT NULL DEFAULT '',
    CONSTRAINT lounge_nickname_length CHECK (char_length(nickname) <= 32)
);
CREATE TABLE IF NOT EXISTS lounge_contact_remarks (
    owner_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    remark text NOT NULL,
    PRIMARY KEY (owner_id, user_id),
    CONSTRAINT lounge_remark_length CHECK (char_length(remark) BETWEEN 1 AND 64),
    CONSTRAINT lounge_remark_other_user CHECK (owner_id <> user_id)
);

COMMIT;
