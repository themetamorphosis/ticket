package migrations_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"kacheem/backend/internal/pgtest"
	"kacheem/backend/migrations"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	cfg, err := pgx.ParseConfig(pgtest.NewDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestUpThenDownRoundTrips(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	p, err := migrations.NewProvider(db, migrations.Files)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("DownTo(0): %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, "select count(*) from pg_tables where tablename = 'audit_event'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("audit_event left after down: n=%d err=%v", n, err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}

func TestAuditEventIsAppendOnly(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	p, _ := migrations.NewProvider(db, migrations.Files)
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if _, err := db.ExecContext(ctx, `insert into audit_event (actor_kind, actor_id, action, object_kind, object_id, reason)
		values ('staff', 'staff-1', 'capacity.update', 'event', 'evt-1', 'venue confirmed')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	for _, stmt := range []string{
		"update audit_event set reason = 'edited'",
		"delete from audit_event",
		"truncate audit_event",
	} {
		_, err := db.ExecContext(ctx, stmt)
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: err = %v, want append-only rejection", stmt, err)
		}
	}
}

func TestAuditEventConstraints(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	p, _ := migrations.NewProvider(db, migrations.Files)
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	insert := `insert into audit_event (actor_kind, actor_id, action, object_kind, object_id, detail) values ($1, $2, $3, 'order', 'ord-1', $4)`
	if _, err := db.ExecContext(ctx, insert, "system", nil, "hold.expire", `{}`); err != nil {
		t.Fatalf("system event without actor_id rejected: %v", err)
	}
	bad := []struct {
		name                        string
		kind, actor, action, detail any
	}{
		{"unknown actor kind", "robot", "r-1", "x", `{}`},
		{"staff without actor", "staff", nil, "x", `{}`},
		{"empty action", "staff", "s-1", "", `{}`},
		{"non-object detail", "staff", "s-1", "x", `[1]`},
	}
	for _, b := range bad {
		if _, err := db.ExecContext(ctx, insert, b.kind, b.actor, b.action, b.detail); err == nil {
			t.Errorf("%s: insert accepted", b.name)
		}
	}
}

func TestNewProviderRejectsBrokenMigrationSet(t *testing.T) {
	broken := fstest.MapFS{
		"00001_a.sql": {Data: []byte("-- +goose Up\nselect 1;\n")},
		"00001_b.sql": {Data: []byte("-- +goose Up\nselect 1;\n")},
	}
	if _, err := migrations.NewProvider(openDB(t), broken); err == nil {
		t.Fatal("expected duplicate-version error")
	}
}

func TestInventoryCapacityInvariants(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	ids := pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "caps", Shards: []int32{5, 5}})

	mustFail := func(name, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	mustFail("held+sold over shard capacity",
		`UPDATE inventory_shard SET held = 4, sold = 2 WHERE ticket_type_id = $1 AND shard = 0`, ids.TicketTypeID)
	mustFail("negative held",
		`UPDATE inventory_shard SET held = -1 WHERE ticket_type_id = $1 AND shard = 0`, ids.TicketTypeID)
	mustFail("shard capacity no longer adds up to tier",
		`UPDATE inventory_shard SET capacity = 6 WHERE ticket_type_id = $1 AND shard = 0`, ids.TicketTypeID)
	mustFail("tier capacity no longer matches shards",
		`UPDATE ticket_type SET capacity = 11 WHERE id = $1`, ids.TicketTypeID)
	mustFail("event capacity below tier capacity",
		`UPDATE event SET capacity = 9 WHERE id = $1`, ids.EventID)

	// A coordinated change of tier and shards in one transaction is allowed.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		`UPDATE event SET capacity = 12 WHERE id = $1`,
		`UPDATE ticket_type SET capacity = 12 WHERE event_id = $1`,
		`UPDATE inventory_shard SET capacity = 6 WHERE ticket_type_id = (SELECT id FROM ticket_type WHERE event_id = $1)`,
	} {
		if _, err := tx.Exec(ctx, s, ids.EventID); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("coordinated capacity change rejected: %v", err)
	}
}

func TestOrderLinesAndHoldsAreImmutable(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	ids := pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "imm", Shards: []int32{5}})
	var orderID string
	err := pool.QueryRow(ctx, `
		INSERT INTO ticket_order (public_ref, event_id, buyer_id, idempotency_key, request_digest,
			purchaser_name, email, phone, age, currency, total_minor, expires_at)
		VALUES ('ABCDE-FGHJK', $1, 'buyer', 'key-12345', decode(repeat('00', 32), 'hex'),
			'A Buyer', 'a@example.com', '+8801711000000', 30, 'BDT', 155000, now() + interval '10 minutes')
		RETURNING id`, ids.EventID).Scan(&orderID)
	if err != nil {
		t.Fatalf("insert order: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO order_line VALUES ($1, $2, 1, 150000, 5000)`, orderID, ids.TicketTypeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO hold VALUES ($1, $2, 0, 1)`, orderID, ids.TicketTypeID); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		`UPDATE order_line SET quantity = 2`,
		`UPDATE hold SET quantity = 2`,
	} {
		if _, err := pool.Exec(ctx, s); err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Errorf("%s: err = %v", s, err)
		}
	}
	// A second active hold for the same buyer and event is rejected.
	_, err = pool.Exec(ctx, `
		INSERT INTO ticket_order (public_ref, event_id, buyer_id, idempotency_key, request_digest,
			purchaser_name, email, phone, age, currency, total_minor, expires_at)
		VALUES ('ABCDE-FGHJM', $1, 'buyer', 'key-67890', decode(repeat('00', 32), 'hex'),
			'A Buyer', 'a@example.com', '+8801711000000', 30, 'BDT', 155000, now() + interval '10 minutes')`, ids.EventID)
	if err == nil || !strings.Contains(err.Error(), "ticket_order_one_active_hold") {
		t.Fatalf("second active hold: err = %v", err)
	}
}
