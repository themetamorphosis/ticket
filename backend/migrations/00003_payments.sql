-- Payment attempts, their event log, refunds and the shared gateway token
-- cache (plan section 4, "Settle payments even when the browser closes").
-- Provider-neutral: each row names its provider (bkash today, paystation later).

-- +goose Up
ALTER TABLE ticket_order ADD COLUMN checkout_extended boolean NOT NULL DEFAULT false;

CREATE TABLE payment_attempt (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Our unique reference, sent to the provider as the invoice number.
    merchant_ref    text        NOT NULL UNIQUE CHECK (merchant_ref ~ '^[A-Za-z0-9-]{8,64}$'),
    order_id        uuid        NOT NULL REFERENCES ticket_order (id),
    provider        text        NOT NULL CHECK (provider ~ '^[a-z0-9]+$'),
    provider_ref    text,
    provider_trx_id text,
    amount_minor    bigint      NOT NULL CHECK (amount_minor >= 0),
    currency        char(3)     NOT NULL,
    -- starting: row written, provider not yet called (or the call is in flight)
    -- pending: buyer sent to the provider; result not known yet
    -- succeeded: money verified as captured; review: captured but mismatched
    -- failed: no money taken; abandoned: not captured because the hold had ended
    status          text        NOT NULL DEFAULT 'starting'
        CHECK (status IN ('starting', 'pending', 'succeeded', 'failed', 'abandoned', 'review')),
    redirect_url    text,
    captured_minor  bigint CHECK (captured_minor >= 0),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_ref),
    UNIQUE (provider, provider_trx_id),
    CHECK (status NOT IN ('succeeded', 'review') OR (provider_trx_id IS NOT NULL AND captured_minor IS NOT NULL))
);
CREATE INDEX payment_attempt_open ON payment_attempt (updated_at) WHERE status IN ('starting', 'pending');
CREATE INDEX payment_attempt_order ON payment_attempt (order_id, created_at);

-- Append-only record of every provider interaction, for audit and support.
-- Holds only the fields needed for verification; never credentials.
CREATE TABLE payment_event (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    attempt_id  uuid        NOT NULL REFERENCES payment_attempt (id),
    kind        text        NOT NULL CHECK (kind <> ''),
    detail      jsonb       NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(detail) = 'object'),
    occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX payment_event_attempt ON payment_event (attempt_id, occurred_at);

CREATE TRIGGER payment_event_immutable BEFORE UPDATE OR DELETE ON payment_event
    FOR EACH ROW EXECUTE FUNCTION audit_event_append_only();

-- A refund intent is persisted before the provider is called and tracked to
-- settlement. One full refund per captured attempt.
CREATE TABLE refund (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    refund_ref         text        NOT NULL UNIQUE,
    attempt_id         uuid        NOT NULL UNIQUE REFERENCES payment_attempt (id),
    amount_minor       bigint      NOT NULL CHECK (amount_minor > 0),
    currency           char(3)     NOT NULL,
    reason             text        NOT NULL CHECK (reason IN ('late_payment', 'duplicate_charge')),
    status             text        NOT NULL DEFAULT 'requested'
        CHECK (status IN ('requested', 'pending', 'completed', 'failed')),
    provider_refund_id text,
    tries              integer     NOT NULL DEFAULT 0,
    last_error         text,
    next_try_at        timestamptz NOT NULL DEFAULT now(),
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CHECK (status <> 'completed' OR provider_refund_id IS NOT NULL)
);
CREATE INDEX refund_due ON refund (next_try_at) WHERE status IN ('requested', 'pending');

-- Provider access tokens shared by every API node and worker, because bKash
-- allows only two token grants and two refreshes per hour.
CREATE TABLE gateway_token (
    provider           text PRIMARY KEY,
    access_token       text        NOT NULL,
    refresh_token      text        NOT NULL,
    access_expires_at  timestamptz NOT NULL,
    refresh_expires_at timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE gateway_token;
DROP TABLE refund;
DROP TABLE payment_event;
DROP TABLE payment_attempt;
ALTER TABLE ticket_order DROP COLUMN checkout_extended;
