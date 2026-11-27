package pgtest

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kacheem/backend/internal/dbx"
)

func TestPoolIsMigratedAndCreateEventSeedsShards(t *testing.T) {
	pool := Pool(t)
	ids := CreateEvent(t, pool, Event{Slug: "fixture", Shards: []int32{3, 4}})
	var capacity, shards int
	err := pool.QueryRow(context.Background(),
		`SELECT sum(capacity), count(*) FROM inventory_shard WHERE ticket_type_id = $1`, ids.TicketTypeID).Scan(&capacity, &shards)
	if err != nil || capacity != 7 || shards != 2 {
		t.Fatalf("capacity %d shards %d err %v", capacity, shards, err)
	}
	draft := CreateEvent(t, pool, Event{Slug: "draft", Status: "draft"})
	var status string
	_ = pool.QueryRow(context.Background(), `SELECT status FROM event WHERE id = $1`, draft.EventID).Scan(&status)
	if status != "draft" {
		t.Fatalf("status %q", status)
	}
}

func TestCreateEventReportsFailure(t *testing.T) {
	pool := Pool(t)
	f := &fakeTB{}
	CreateEvent(f, pool, Event{Slug: "Not A Slug"})
	if len(f.failed) != 1 || !strings.Contains(f.failed[0], "create event") {
		t.Fatalf("failed = %v", f.failed)
	}
}

func TestPoolReportsMigrationFailure(t *testing.T) {
	orig := migrateUp
	defer func() { migrateUp = orig }()
	migrateUp = func(context.Context, *sql.DB) error { return errors.New("boom") }
	f := &fakeTB{}
	if Pool(f) != nil || len(f.failed) != 1 || !strings.Contains(f.failed[0], "migrate") {
		t.Fatalf("failed = %v", f.failed)
	}
	f.runCleanups()
}

func TestPoolReportsOpenFailure(t *testing.T) {
	orig := openPool
	defer func() { openPool = orig }()
	openPool = func(context.Context, string) (*pgxpool.Pool, error) { return nil, errors.New("boom") }
	f := &fakeTB{}
	if Pool(f) != nil || len(f.failed) != 1 || !strings.Contains(f.failed[0], "open pool") {
		t.Fatalf("failed = %v", f.failed)
	}
	f.runCleanups()
}

func TestFaultyFailsEachCallKindOnce(t *testing.T) {
	ctx := context.Background()
	pool := Pool(t)
	calls := []func(dbx.DB) error{
		func(db dbx.DB) error { _, err := db.Exec(ctx, "select 1"); return err },
		func(db dbx.DB) error {
			rows, err := db.Query(ctx, "select 1")
			if err == nil {
				rows.Close()
			}
			return err
		},
		func(db dbx.DB) error { var n int; return db.QueryRow(ctx, "select 1").Scan(&n) },
		func(db dbx.DB) error {
			tx, err := db.Begin(ctx)
			if err == nil {
				tx.Rollback(ctx)
			}
			return err
		},
	}
	for i, call := range calls {
		f := NewFaulty(pool, 2)
		if err := call(f); err != nil {
			t.Fatalf("call %d: first call failed: %v", i, err)
		}
		if err := call(f); !errors.Is(err, ErrInjected) {
			t.Fatalf("call %d: second call err = %v", i, err)
		}
		if err := call(f); err != nil || f.Calls() != 3 || !f.Injected() {
			t.Fatalf("call %d: third call err = %v calls %d", i, err, f.Calls())
		}
	}
}

func TestFaultyTransactionCallsAreCounted(t *testing.T) {
	ctx := context.Background()
	pool := Pool(t)
	run := func(db dbx.DB) error {
		return dbx.InTx(ctx, db, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "select 1"); err != nil {
				return err
			}
			rows, err := tx.Query(ctx, "select 1")
			if err != nil {
				return err
			}
			rows.Close()
			var n int
			if err := tx.QueryRow(ctx, "select 1").Scan(&n); err != nil {
				return err
			}
			nested, err := tx.Begin(ctx)
			if err != nil {
				return err
			}
			return nested.Commit(ctx)
		})
	}
	// Begin, Exec, Query, QueryRow, nested Begin, nested Commit, Commit.
	n := EachFault(t, pool, run, func(failAt int, err error) {
		if !errors.Is(err, ErrInjected) {
			t.Errorf("failAt %d: err = %v", failAt, err)
		}
	})
	if n != 7 {
		t.Fatalf("injected %d failures, want 7", n)
	}
}

func TestFaultyBeginReportsUnderlyingError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pool := Pool(t)
	if _, err := NewFaulty(pool, 99).Begin(ctx); err == nil {
		t.Fatal("expected begin error on cancelled context")
	}
}

func TestEachFaultFailsTestWhenOpErrorsWithoutInjection(t *testing.T) {
	f := &fakeTB{}
	EachFault(f, nil, func(dbx.DB) error { return errors.New("always") }, func(int, error) {})
	if len(f.failed) != 1 || !strings.Contains(f.failed[0], "without injection") {
		t.Fatalf("failed = %v", f.failed)
	}
}

func TestEachFaultFailsTestWhenInjectionIsSwallowed(t *testing.T) {
	ctx := context.Background()
	pool := Pool(t)
	f := &fakeTB{}
	EachFault(f, pool, func(db dbx.DB) error { _, _ = db.Exec(ctx, "select 1"); return nil }, func(int, error) {})
	if len(f.failed) != 1 || !strings.Contains(f.failed[0], "swallowed") {
		t.Fatalf("failed = %v", f.failed)
	}
}
