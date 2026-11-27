-- Events, ticket tiers, sharded inventory, orders and holds (plan section 4).
-- PostgreSQL is the only source of truth for inventory. Each tier's capacity is
-- split across inventory_shard rows so concurrent holds rarely contend on one
-- hot row; the shard capacities always add up exactly to the tier capacity,
-- and tier capacities never exceed the event capacity.
--
-- Lock order for every writer: inventory_shard rows ascending by
-- (ticket_type_id, shard), then the ticket_order row.

-- +goose Up
CREATE TABLE event (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug               text        NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    title              text        NOT NULL CHECK (title <> ''),
    organizer          text        NOT NULL CHECK (organizer <> ''),
    venue_name         text        NOT NULL CHECK (venue_name <> ''),
    venue_address      text        NOT NULL DEFAULT '',
    description        text        NOT NULL DEFAULT '',
    lineup             text[]      NOT NULL DEFAULT '{}',
    starts_at          timestamptz NOT NULL,
    doors_at           timestamptz,
    time_zone          text        NOT NULL CHECK (time_zone <> ''),
    currency           char(3)     NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    capacity           integer     NOT NULL CHECK (capacity >= 0),
    max_per_order      integer     NOT NULL CHECK (max_per_order BETWEEN 1 AND 20),
    min_age            integer     NOT NULL DEFAULT 0 CHECK (min_age BETWEEN 0 AND 120),
    hold_minutes       integer     NOT NULL DEFAULT 10 CHECK (hold_minutes BETWEEN 1 AND 60),
    sale_opens_at      timestamptz NOT NULL,
    sale_closes_at     timestamptz NOT NULL,
    status             text        NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published')),
    published_revision integer     NOT NULL DEFAULT 0 CHECK (published_revision >= 0),
    published_at       timestamptz,
    CHECK (sale_opens_at < sale_closes_at),
    CHECK (doors_at IS NULL OR doors_at <= starts_at)
);

CREATE TABLE ticket_type (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id    uuid    NOT NULL REFERENCES event (id),
    name        text    NOT NULL CHECK (name <> ''),
    price_minor bigint  NOT NULL CHECK (price_minor >= 0),
    fee_minor   bigint  NOT NULL CHECK (fee_minor >= 0),
    capacity    integer NOT NULL CHECK (capacity >= 0),
    sort_order  integer NOT NULL DEFAULT 0,
    UNIQUE (event_id, name)
);

CREATE TABLE inventory_shard (
    ticket_type_id uuid     NOT NULL REFERENCES ticket_type (id),
    shard          smallint NOT NULL CHECK (shard BETWEEN 0 AND 63),
    capacity       integer  NOT NULL CHECK (capacity >= 0),
    held           integer  NOT NULL DEFAULT 0 CHECK (held >= 0),
    sold           integer  NOT NULL DEFAULT 0 CHECK (sold >= 0),
    PRIMARY KEY (ticket_type_id, shard),
    CHECK (held + sold <= capacity)
);

-- +goose StatementBegin
CREATE FUNCTION check_capacity_sums() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    tt uuid;
    ev uuid;
BEGIN
    IF TG_TABLE_NAME = 'inventory_shard' THEN
        tt := NEW.ticket_type_id;
        IF (SELECT capacity FROM ticket_type WHERE id = tt)
           IS DISTINCT FROM (SELECT coalesce(sum(capacity), 0) FROM inventory_shard WHERE ticket_type_id = tt) THEN
            RAISE EXCEPTION 'shard capacities must add up to ticket_type % capacity', tt
                USING ERRCODE = 'check_violation';
        END IF;
        SELECT event_id INTO ev FROM ticket_type WHERE id = tt;
    ELSIF TG_TABLE_NAME = 'ticket_type' THEN
        ev := NEW.event_id;
        IF NEW.capacity <> (SELECT coalesce(sum(capacity), 0) FROM inventory_shard WHERE ticket_type_id = NEW.id) THEN
            RAISE EXCEPTION 'shard capacities must add up to ticket_type % capacity', NEW.id
                USING ERRCODE = 'check_violation';
        END IF;
    ELSE
        ev := NEW.id;
    END IF;
    IF (SELECT coalesce(sum(capacity), 0) FROM ticket_type WHERE event_id = ev)
       > (SELECT capacity FROM event WHERE id = ev) THEN
        RAISE EXCEPTION 'ticket_type capacities exceed event % capacity', ev
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

-- Deferred, so a capacity change can update a tier and all its shards in one transaction.
CREATE CONSTRAINT TRIGGER inventory_shard_capacity_sum
    AFTER INSERT OR UPDATE OF capacity ON inventory_shard
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_capacity_sums();
CREATE CONSTRAINT TRIGGER ticket_type_capacity_sum
    AFTER INSERT OR UPDATE OF capacity ON ticket_type
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_capacity_sums();
CREATE CONSTRAINT TRIGGER event_capacity_sum
    AFTER UPDATE OF capacity ON event
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_capacity_sums();

-- "order" is reserved in SQL, hence ticket_order.
CREATE TABLE ticket_order (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    public_ref      text        NOT NULL UNIQUE CHECK (public_ref ~ '^[0-9A-HJKMNP-TV-Z]{5}-[0-9A-HJKMNP-TV-Z]{5}$'),
    event_id        uuid        NOT NULL REFERENCES event (id),
    buyer_id        text        NOT NULL CHECK (buyer_id <> ''),
    idempotency_key text        NOT NULL CHECK (length(idempotency_key) BETWEEN 8 AND 100),
    request_digest  bytea       NOT NULL CHECK (length(request_digest) = 32),
    purchaser_name  text        NOT NULL CHECK (purchaser_name <> ''),
    email           text        NOT NULL CHECK (email <> ''),
    phone           text        NOT NULL CHECK (phone ~ '^\+[1-9][0-9]{7,14}$'),
    age             integer     NOT NULL CHECK (age BETWEEN 0 AND 120),
    currency        char(3)     NOT NULL,
    total_minor     bigint      NOT NULL CHECK (total_minor >= 0),
    status          text        NOT NULL DEFAULT 'held' CHECK (status IN ('held', 'expired', 'cancelled', 'paid')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    closed_at       timestamptz,
    UNIQUE (buyer_id, idempotency_key),
    CHECK ((status = 'held') = (closed_at IS NULL))
);

-- One active hold per buyer session per event.
CREATE UNIQUE INDEX ticket_order_one_active_hold ON ticket_order (event_id, buyer_id) WHERE status = 'held';
CREATE INDEX ticket_order_held_expiry ON ticket_order (expires_at) WHERE status = 'held';
CREATE INDEX ticket_order_event_created ON ticket_order (event_id, created_at);
CREATE INDEX ticket_order_email ON ticket_order (lower(email));
CREATE INDEX ticket_order_phone ON ticket_order (phone);

-- Immutable price and quantity snapshot.
CREATE TABLE order_line (
    order_id         uuid    NOT NULL REFERENCES ticket_order (id),
    ticket_type_id   uuid    NOT NULL REFERENCES ticket_type (id),
    quantity         integer NOT NULL CHECK (quantity > 0),
    unit_price_minor bigint  NOT NULL CHECK (unit_price_minor >= 0),
    unit_fee_minor   bigint  NOT NULL CHECK (unit_fee_minor >= 0),
    PRIMARY KEY (order_id, ticket_type_id)
);

-- +goose StatementBegin
CREATE FUNCTION reject_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% rows are immutable', TG_TABLE_NAME USING ERRCODE = 'insufficient_privilege';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER order_line_immutable BEFORE UPDATE ON order_line
    FOR EACH ROW EXECUTE FUNCTION reject_update();

-- Quantity an order holds on each shard. The hold is active while its order is
-- 'held'; the transition out of 'held' releases it exactly once.
CREATE TABLE hold (
    order_id       uuid     NOT NULL REFERENCES ticket_order (id),
    ticket_type_id uuid     NOT NULL,
    shard          smallint NOT NULL,
    quantity       integer  NOT NULL CHECK (quantity > 0),
    PRIMARY KEY (order_id, ticket_type_id, shard),
    FOREIGN KEY (ticket_type_id, shard) REFERENCES inventory_shard (ticket_type_id, shard)
);

CREATE TRIGGER hold_immutable BEFORE UPDATE ON hold
    FOR EACH ROW EXECUTE FUNCTION reject_update();

-- Buyer sessions (SCS pgxstore schema). Opaque tokens, host-only cookies.
CREATE TABLE buyer_session (
    token  text PRIMARY KEY,
    data   bytea       NOT NULL,
    expiry timestamptz NOT NULL
);
CREATE INDEX buyer_session_expiry ON buyer_session (expiry);

-- +goose Down
DROP TABLE buyer_session;
DROP TABLE hold;
DROP TABLE order_line;
DROP FUNCTION reject_update();
DROP TABLE ticket_order;
DROP TABLE inventory_shard;
DROP TABLE ticket_type;
DROP TABLE event;
DROP FUNCTION check_capacity_sums();
