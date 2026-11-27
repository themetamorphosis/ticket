package pgtest

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"kacheem/backend/internal/dbx"
)

// ErrInjected is the error a Faulty DB returns on its chosen call.
var ErrInjected = errors.New("pgtest: injected failure")

// Faulty wraps a DB and fails exactly its FailAt-th call (Exec, Query,
// QueryRow, Begin or Commit, counted across nested transactions). Running an
// operation with FailAt = 1, 2, 3, ... until it succeeds drives every error
// branch and lets the test assert invariants after each partial failure.
type Faulty struct {
	db     dbx.DB
	failAt int
	calls  *int
}

// NewFaulty returns db wrapped to fail its failAt-th call.
func NewFaulty(db dbx.DB, failAt int) *Faulty {
	return &Faulty{db: db, failAt: failAt, calls: new(int)}
}

// Calls reports how many calls have been made.
func (f *Faulty) Calls() int { return *f.calls }

// Injected reports whether the failure has been injected.
func (f *Faulty) Injected() bool { return *f.calls >= f.failAt }

func (f *Faulty) hit() bool {
	*f.calls++
	return *f.calls == f.failAt
}

func (f *Faulty) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return faultyExec(f, f.db, ctx, sql, args)
}

func (f *Faulty) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return faultyQuery(f, f.db, ctx, sql, args)
}

func (f *Faulty) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return faultyQueryRow(f, f.db, ctx, sql, args)
}

func (f *Faulty) Begin(ctx context.Context) (pgx.Tx, error) {
	return faultyBegin(f, f.db, ctx)
}

type faultyTx struct {
	pgx.Tx
	f *Faulty
}

func (t *faultyTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return faultyExec(t.f, t.Tx, ctx, sql, args)
}

func (t *faultyTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return faultyQuery(t.f, t.Tx, ctx, sql, args)
}

func (t *faultyTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return faultyQueryRow(t.f, t.Tx, ctx, sql, args)
}

func (t *faultyTx) Begin(ctx context.Context) (pgx.Tx, error) {
	return faultyBegin(t.f, t.Tx, ctx)
}

func (t *faultyTx) Commit(ctx context.Context) error {
	if t.f.hit() {
		return ErrInjected
	}
	return t.Tx.Commit(ctx)
}

func faultyExec(f *Faulty, db dbx.DB, ctx context.Context, sql string, args []any) (pgconn.CommandTag, error) {
	if f.hit() {
		return pgconn.CommandTag{}, ErrInjected
	}
	return db.Exec(ctx, sql, args...)
}

func faultyQuery(f *Faulty, db dbx.DB, ctx context.Context, sql string, args []any) (pgx.Rows, error) {
	if f.hit() {
		return nil, ErrInjected
	}
	return db.Query(ctx, sql, args...)
}

func faultyQueryRow(f *Faulty, db dbx.DB, ctx context.Context, sql string, args []any) pgx.Row {
	if f.hit() {
		return errRow{}
	}
	return db.QueryRow(ctx, sql, args...)
}

func faultyBegin(f *Faulty, db dbx.DB, ctx context.Context) (pgx.Tx, error) {
	if f.hit() {
		return nil, ErrInjected
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &faultyTx{Tx: tx, f: f}, nil
}

type errRow struct{}

func (errRow) Scan(...any) error { return ErrInjected }

// EachFault runs op with a DB failing at call 1, 2, 3, ... until op succeeds,
// calling afterFailure after every injected failure (to assert invariants).
// It returns the number of injected failures. op must return an error
// whenever the injected failure reached it.
func EachFault(t TB, db dbx.DB, op func(dbx.DB) error, afterFailure func(failAt int, err error)) int {
	t.Helper()
	for failAt := 1; ; failAt++ {
		f := NewFaulty(db, failAt)
		err := op(f)
		if !f.Injected() {
			if err != nil {
				t.Fatalf("pgtest: op failed without injection: %v", err)
			}
			return failAt - 1
		}
		if err == nil {
			t.Fatalf("pgtest: injected failure at call %d was swallowed", failAt)
			return failAt
		}
		afterFailure(failAt, err)
	}
}
