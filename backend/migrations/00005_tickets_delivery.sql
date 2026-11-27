-- Tickets, their PDFs, the fulfilment outbox, pickup grants and delivery
-- outcomes (plan sections 4 and 5). One ticket row per paid place.

-- +goose Up
CREATE TABLE ticket (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id             uuid        NOT NULL REFERENCES ticket_order (id),
    ticket_type_id       uuid        NOT NULL REFERENCES ticket_type (id),
    ordinal              integer     NOT NULL CHECK (ordinal > 0),
    -- SHA-256 of the QR credential and of the normalized fallback code; the
    -- gate looks tickets up by these and never needs the plaintext.
    credential_digest    bytea       NOT NULL UNIQUE CHECK (length(credential_digest) = 32),
    fallback_digest      bytea       NOT NULL UNIQUE CHECK (length(fallback_digest) = 32),
    -- The credential and fallback code, encrypted, so the PDF can be rendered
    -- again. Reissue increments credential_version with new values.
    credential_sealed    bytea       NOT NULL,
    credential_version   integer     NOT NULL DEFAULT 1 CHECK (credential_version > 0),
    status               text        NOT NULL DEFAULT 'valid' CHECK (status IN ('valid', 'used', 'void')),
    created_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (order_id, ordinal)
);
CREATE INDEX ticket_by_order ON ticket (order_id, ordinal);

-- Immutable rendered PDF per ticket and credential version.
CREATE TABLE ticket_document (
    ticket_id   uuid        NOT NULL REFERENCES ticket (id),
    version     integer     NOT NULL,
    pdf         bytea       NOT NULL,
    size_bytes  integer     NOT NULL CHECK (size_bytes = length(pdf)),
    sha256      bytea       NOT NULL CHECK (length(sha256) = 32),
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (ticket_id, version)
);

CREATE TRIGGER ticket_document_immutable BEFORE UPDATE ON ticket_document
    FOR EACH ROW EXECUTE FUNCTION reject_update();

-- Durable job queue. A job is enqueued in the same transaction as the change
-- that needs it, claimed with FOR UPDATE SKIP LOCKED under a lease, and
-- reclaimed if a worker dies mid-job.
CREATE TABLE outbox_job (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind        text        NOT NULL CHECK (kind ~ '^[a-z_]+$'),
    order_id    uuid        NOT NULL REFERENCES ticket_order (id),
    dedupe_key  text        NOT NULL,
    status      text        NOT NULL DEFAULT 'ready' CHECK (status IN ('ready', 'running', 'done', 'failed')),
    attempts    integer     NOT NULL DEFAULT 0,
    run_at      timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz,
    last_error  text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (kind, dedupe_key)
);
CREATE INDEX outbox_job_ready ON outbox_job (run_at) WHERE status IN ('ready', 'running');

-- Private pickup links sent by SMS. Only the SHA-256 of the secret is kept.
CREATE TABLE download_grant (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id      uuid        NOT NULL REFERENCES ticket_order (id),
    secret_digest bytea       NOT NULL UNIQUE CHECK (length(secret_digest) = 32),
    expires_at    timestamptz NOT NULL,
    revoked_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX download_grant_order ON download_grant (order_id);

-- One row per send attempt on each channel.
CREATE TABLE delivery_attempt (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    order_id    uuid        NOT NULL REFERENCES ticket_order (id),
    channel     text        NOT NULL CHECK (channel IN ('email', 'sms')),
    status      text        NOT NULL CHECK (status IN ('sent', 'failed')),
    provider_id text,
    error       text,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX delivery_attempt_order ON delivery_attempt (order_id, created_at);

-- Messages "sent" by the development channels (EMAIL_PROVIDER/SMS_PROVIDER
-- = fake), shown at /dev/outbox. Empty with real providers.
CREATE TABLE dev_message (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    channel     text        NOT NULL CHECK (channel IN ('email', 'sms')),
    recipient   text        NOT NULL,
    subject     text        NOT NULL DEFAULT '',
    body        text        NOT NULL,
    attachments jsonb       NOT NULL DEFAULT '[]'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE dev_message;
DROP TABLE delivery_attempt;
DROP TABLE download_grant;
DROP TABLE outbox_job;
DROP TABLE ticket_document;
DROP TABLE ticket;
