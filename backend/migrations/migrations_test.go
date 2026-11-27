package migrations

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"kacheem/backend/internal/pgtest"
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
	p, err := NewProvider(db, Files)
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
	p, _ := NewProvider(db, Files)
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
	p, _ := NewProvider(db, Files)
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
	if _, err := NewProvider(openDB(t), broken); err == nil {
		t.Fatal("expected duplicate-version error")
	}
}
