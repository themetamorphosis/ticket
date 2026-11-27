-- State of the local fake payment gateway (PAYMENT_PROVIDER=fake), used in
-- development and tests. Stored in PostgreSQL so the API and the worker see
-- the same payments. Unused, and empty, when a real provider is configured.

-- +goose Up
CREATE TABLE fake_gateway_payment (
    ref            text PRIMARY KEY,
    merchant_ref   text        NOT NULL,
    amount_minor   bigint      NOT NULL,
    currency       char(3)     NOT NULL,
    return_url     text        NOT NULL,
    state          text        NOT NULL DEFAULT 'created'
        CHECK (state IN ('created', 'approved', 'declined', 'cancelled', 'captured', 'failed')),
    trx_id         text UNIQUE,
    refunded_minor bigint      NOT NULL DEFAULT 0,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE fake_gateway_refund (
    refund_ref   text PRIMARY KEY,
    payment_ref  text   NOT NULL REFERENCES fake_gateway_payment (ref),
    amount_minor bigint NOT NULL,
    refund_id    text   NOT NULL UNIQUE
);

-- +goose Down
DROP TABLE fake_gateway_refund;
DROP TABLE fake_gateway_payment;
