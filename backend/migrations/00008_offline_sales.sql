-- Offline sales: staff record a sale paid in cash, by bank transfer or to the
-- organizer's bKash number, and the order is issued and delivered exactly like
-- an online one. The money is a payment_attempt with provider 'offline', so the
-- ledger, order detail and refunds see one model of payment.

-- +goose Up
ALTER TABLE ticket_order ADD COLUMN channel text NOT NULL DEFAULT 'online'
    CHECK (channel IN ('online', 'offline'));

ALTER TABLE payment_attempt ADD COLUMN offline_method text
    CHECK (offline_method IN ('cash', 'bank', 'bkash'));
-- What the staff member typed as proof (bKash TrxID, bank reference, receipt
-- number); optional, so not unique.
ALTER TABLE payment_attempt ADD COLUMN offline_reference text
    CHECK (offline_reference <> '' AND length(offline_reference) <= 64);
ALTER TABLE payment_attempt ADD COLUMN received_by uuid REFERENCES staff (id);
ALTER TABLE payment_attempt ADD CONSTRAINT payment_attempt_offline_fields CHECK (
    CASE WHEN provider = 'offline'
        THEN offline_method IS NOT NULL AND received_by IS NOT NULL
        ELSE offline_method IS NULL AND offline_reference IS NULL AND received_by IS NULL
    END);

-- +goose Down
ALTER TABLE payment_attempt DROP CONSTRAINT payment_attempt_offline_fields;
ALTER TABLE payment_attempt DROP COLUMN received_by;
ALTER TABLE payment_attempt DROP COLUMN offline_reference;
ALTER TABLE payment_attempt DROP COLUMN offline_method;
ALTER TABLE ticket_order DROP COLUMN channel;
