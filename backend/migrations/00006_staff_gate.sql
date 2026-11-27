-- Staff accounts, WebAuthn credentials, gates, admissions and scan attempts
-- (plan sections 6 and 7).

-- +goose Up
CREATE TABLE staff (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text        NOT NULL UNIQUE CHECK (email = lower(email) AND email <> ''),
    name          text        NOT NULL CHECK (name <> ''),
    role          text        NOT NULL CHECK (role IN ('organizer', 'finance', 'support', 'gate')),
    -- Argon2id PHC string; NULL until the invitation is accepted.
    password_hash text,
    status        text        NOT NULL DEFAULT 'invited' CHECK (status IN ('invited', 'active', 'disabled')),
    -- Bumped on revocation or role change: sessions carrying an older value are refused.
    session_epoch integer     NOT NULL DEFAULT 1,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CHECK (status = 'invited' OR password_hash IS NOT NULL)
);

-- One-time invitation links. Only the SHA-256 of the token is stored.
CREATE TABLE staff_invite (
    token_digest bytea       PRIMARY KEY CHECK (length(token_digest) = 32),
    staff_id     uuid        NOT NULL REFERENCES staff (id),
    expires_at   timestamptz NOT NULL,
    used_at      timestamptz
);

-- WebAuthn (security key / passkey) credentials, the second factor.
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

-- Staff sessions (SCS pgxstore schema), separate from buyer sessions.
CREATE TABLE staff_session (
    token  text PRIMARY KEY,
    data   bytea       NOT NULL,
    expiry timestamptz NOT NULL
);
CREATE INDEX staff_session_expiry ON staff_session (expiry);

CREATE TABLE gate (
    id       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id uuid NOT NULL REFERENCES event (id),
    name     text NOT NULL CHECK (name <> ''),
    UNIQUE (event_id, name)
);

-- Gate staff may scan only at gates they are assigned to.
CREATE TABLE gate_assignment (
    staff_id uuid NOT NULL REFERENCES staff (id),
    gate_id  uuid NOT NULL REFERENCES gate (id),
    PRIMARY KEY (staff_id, gate_id)
);

-- At most one admission per ticket (single-entry event).
CREATE TABLE admission (
    ticket_id   uuid PRIMARY KEY REFERENCES ticket (id),
    gate_id     uuid        NOT NULL REFERENCES gate (id),
    staff_id    uuid        NOT NULL REFERENCES staff (id),
    device_id   text        NOT NULL,
    request_id  text        NOT NULL,
    admitted_at timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER admission_immutable BEFORE UPDATE OR DELETE ON admission
    FOR EACH ROW EXECUTE FUNCTION reject_update();

-- Every scan and its outcome. A device's retry of the same request returns
-- the recorded outcome instead of deciding again.
CREATE TABLE scan_attempt (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    device_id  text        NOT NULL CHECK (device_id ~ '^[A-Za-z0-9_-]{8,64}$'),
    request_id text        NOT NULL CHECK (request_id ~ '^[A-Za-z0-9_-]{8,64}$'),
    gate_id    uuid        NOT NULL REFERENCES gate (id),
    staff_id   uuid        NOT NULL REFERENCES staff (id),
    ticket_id  uuid REFERENCES ticket (id),
    method     text        NOT NULL CHECK (method IN ('qr', 'code')),
    outcome    text        NOT NULL CHECK (outcome IN ('admitted', 'already_used', 'unknown', 'wrong_event', 'void',
                                                       'not_open', 'closed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (device_id, request_id)
);
CREATE INDEX scan_attempt_gate_time ON scan_attempt (gate_id, created_at);

CREATE TRIGGER scan_attempt_immutable BEFORE UPDATE OR DELETE ON scan_attempt
    FOR EACH ROW EXECUTE FUNCTION reject_update();

-- Doors: when admission opens and closes, relative to the event. NULL opens
-- three hours before the start and closes twelve hours after.
ALTER TABLE event ADD COLUMN admission_opens_at timestamptz;
ALTER TABLE event ADD COLUMN admission_closes_at timestamptz;

-- Tickets can be blocked by a pending refund before it settles.
ALTER TABLE ticket DROP CONSTRAINT ticket_status_check;
ALTER TABLE ticket ADD CONSTRAINT ticket_status_check CHECK (status IN ('valid', 'used', 'void', 'refund_pending'));

-- An order whose refund was approved: its money is accounted to the refund,
-- not to sales, from the moment of approval.
ALTER TABLE ticket_order DROP CONSTRAINT ticket_order_status_check;
ALTER TABLE ticket_order ADD CONSTRAINT ticket_order_status_check
    CHECK (status IN ('held', 'expired', 'cancelled', 'paid', 'refunded'));

-- Organizer-approved refunds (finance), alongside the automatic ones.
ALTER TABLE refund DROP CONSTRAINT refund_reason_check;
ALTER TABLE refund ADD CONSTRAINT refund_reason_check CHECK (reason IN ('late_payment', 'duplicate_charge', 'organizer'));

-- +goose Down
ALTER TABLE ticket_order DROP CONSTRAINT ticket_order_status_check;
ALTER TABLE ticket_order ADD CONSTRAINT ticket_order_status_check
    CHECK (status IN ('held', 'expired', 'cancelled', 'paid'));
ALTER TABLE refund DROP CONSTRAINT refund_reason_check;
ALTER TABLE refund ADD CONSTRAINT refund_reason_check CHECK (reason IN ('late_payment', 'duplicate_charge'));
ALTER TABLE ticket DROP CONSTRAINT ticket_status_check;
ALTER TABLE ticket ADD CONSTRAINT ticket_status_check CHECK (status IN ('valid', 'used', 'void'));
ALTER TABLE event DROP COLUMN admission_closes_at;
ALTER TABLE event DROP COLUMN admission_opens_at;
DROP TABLE scan_attempt;
DROP TABLE admission;
DROP TABLE gate_assignment;
DROP TABLE gate;
DROP TABLE staff_session;
DROP TABLE webauthn_credential;
DROP TABLE staff_invite;
DROP TABLE staff;
