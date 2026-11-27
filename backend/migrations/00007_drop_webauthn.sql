-- Staff sign in with a password only; security keys and passkeys were removed.
-- The Down migration restores the empty table, not the deleted credentials.

-- +goose Up
DROP TABLE webauthn_credential;

-- +goose Down
CREATE TABLE webauthn_credential (
    id            bytea PRIMARY KEY,
    staff_id      uuid        NOT NULL REFERENCES staff (id),
    public_key    bytea       NOT NULL,
    attestation   text        NOT NULL DEFAULT '',
    sign_count    bigint      NOT NULL DEFAULT 0,
    aaguid        bytea,
    transports    text[]      NOT NULL DEFAULT '{}',
    backup_eligible boolean   NOT NULL DEFAULT false,
    backup_state  boolean     NOT NULL DEFAULT false,
    name          text        NOT NULL DEFAULT 'Security key',
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_used_at  timestamptz
);
CREATE INDEX webauthn_credential_staff ON webauthn_credential (staff_id);
