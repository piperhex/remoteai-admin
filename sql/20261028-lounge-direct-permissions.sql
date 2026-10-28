BEGIN;
CREATE TABLE IF NOT EXISTS lounge_direct_permissions (
    first_user uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    second_user uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    initiator uuid,
    fingerprint varchar(64),
    PRIMARY KEY (first_user, second_user),
    CHECK (first_user < second_user),
    CHECK ((initiator IS NULL AND fingerprint IS NULL) OR
        (initiator IS NOT NULL AND initiator IN (first_user, second_user)
            AND fingerprint IS NOT NULL AND length(fingerprint) = 64))
);
COMMIT;
