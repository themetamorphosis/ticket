package dbx_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/pgtest"
)

type row struct {
	N int `db:"n"`
}

func TestInTxCommitsOnSuccessAndRollsBackOnError(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	if _, err := pool.Exec(ctx, "create table t (n int)"); err != nil {
		t.Fatal(err)
	}
	if err := dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "insert into t values (1)")
		return err
	}); err != nil {
		t.Fatalf("InTx: %v", err)
	}
	boom := errors.New("boom")
	if err := dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		_, _ = tx.Exec(ctx, "insert into t values (2)")
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	got, err := dbx.All[row](ctx, pool, "select n from t order by n")
	if err != nil || len(got) != 1 || got[0].N != 1 {
		t.Fatalf("rows %v err %v", got, err)
	}
}

func TestInTxErrorPaths(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	n := pgtest.EachFault(t, pool, func(db dbx.DB) error {
		return dbx.InTx(ctx, db, func(pgx.Tx) error { return nil })
	}, func(int, error) {})
	if n != 2 { // Begin, Commit
		t.Fatalf("faults %d", n)
	}
}

func TestOneAndAll(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	one, err := dbx.One[row](ctx, pool, "select 7 as n")
	if err != nil || one.N != 7 {
		t.Fatalf("One: %v %v", one, err)
	}
	if _, err := dbx.One[row](ctx, pool, "select 1 as n where false"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("One no rows: %v", err)
	}
	if _, err := dbx.One[row](ctx, pool, "select 'x' as n"); err == nil {
		t.Fatal("One: expected scan error")
	}
	if _, err := dbx.All[row](ctx, pool, "select 'x' as n"); err == nil {
		t.Fatal("All: expected scan error")
	}
	for _, q := range []func(dbx.DB) error{
		func(db dbx.DB) error { _, err := dbx.One[row](ctx, db, "select 1 as n"); return err },
		func(db dbx.DB) error { _, err := dbx.All[row](ctx, db, "select 1 as n"); return err },
	} {
		if n := pgtest.EachFault(t, pool, q, func(int, error) {}); n != 1 {
			t.Fatalf("faults %d", n)
		}
	}
}

func TestIsUniqueViolation(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	_, _ = pool.Exec(ctx, "create table u (n int constraint u_n unique)")
	_, _ = pool.Exec(ctx, "insert into u values (1)")
	_, err := pool.Exec(ctx, "insert into u values (1)")
	if !dbx.IsUniqueViolation(err, "u_n") || dbx.IsUniqueViolation(err, "other") || dbx.IsUniqueViolation(errors.New("x"), "u_n") {
		t.Fatalf("IsUniqueViolation wrong for %v", err)
	}
}
