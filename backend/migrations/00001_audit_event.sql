-- Append-only audit trail. Every sensitive staff, buyer, provider and system
-- action names its actor here (plan sections 1 and 4). Rows are never updated
-- or deleted by the application; a redacted copy is also shipped off-host.

-- +goose Up
CREATE TABLE audit_event (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    actor_kind  text        NOT NULL CHECK (actor_kind IN ('staff', 'buyer', 'system', 'provider')),
    actor_id    text        CHECK (actor_id <> ''),
    action      text        NOT NULL CHECK (action <> ''),
    object_kind text        NOT NULL CHECK (object_kind <> ''),
    object_id   text        NOT NULL CHECK (object_id <> ''),
    reason      text,
    detail      jsonb       NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(detail) = 'object'),
    CHECK (actor_kind = 'system' OR actor_id IS NOT NULL)
);

CREATE INDEX audit_event_object_idx ON audit_event (object_kind, object_id, occurred_at);
CREATE INDEX audit_event_occurred_idx ON audit_event (occurred_at);

-- +goose StatementBegin
CREATE FUNCTION audit_event_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_event is append-only (% rejected)', TG_OP
        USING ERRCODE = 'insufficient_privilege';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER audit_event_no_update_delete
    BEFORE UPDATE OR DELETE ON audit_event
    FOR EACH ROW EXECUTE FUNCTION audit_event_append_only();

CREATE TRIGGER audit_event_no_truncate
    BEFORE TRUNCATE ON audit_event
    FOR EACH STATEMENT EXECUTE FUNCTION audit_event_append_only();

-- +goose Down
DROP TABLE audit_event;
DROP FUNCTION audit_event_append_only();
