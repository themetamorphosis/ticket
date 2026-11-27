package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/orders"
	"kacheem/backend/internal/pgtest"
)

var ctx = context.Background()

func orderID(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live"})
	o, _, err := orders.Reserve(ctx, pool, orders.Request{EventSlug: "live", BuyerID: "b", IdempotencyKey: "key-00000001",
		Input: orders.Input{Quantity: 1, Name: "A Buyer", Email: "a@example.com", Phone: "+8801711000000", Age: 30}})
	if err != nil {
		t.Fatal(err)
	}
	return o.ID
}

func status(t *testing.T, pool *pgxpool.Pool, id int64) (string, int, bool) {
	t.Helper()
	var s string
	var attempts int
	var due bool
	_ = pool.QueryRow(ctx, `SELECT status, attempts, run_at <= now() FROM outbox_job WHERE id = $1`, id).Scan(&s, &attempts, &due)
	return s, attempts, due
}

func TestEnqueueClaimDone(t *testing.T) {
	pool := pgtest.Pool(t)
	oid := orderID(t, pool)
	for range 2 {
		if err := Enqueue(ctx, pool, "render_tickets", oid, oid); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := Claim(ctx, pool, 10, time.Minute)
	if err != nil || len(jobs) != 1 || jobs[0].Kind != "render_tickets" || jobs[0].OrderID != oid || jobs[0].Attempts != 1 {
		t.Fatalf("claim %+v %v", jobs, err)
	}
	if again, _ := Claim(ctx, pool, 10, time.Minute); len(again) != 0 {
		t.Fatal("leased job claimed twice")
	}
	if err := Done(ctx, pool, jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := status(t, pool, jobs[0].ID); s != "done" {
		t.Fatalf("status %s", s)
	}
}

func TestExpiredLeaseIsReclaimed(t *testing.T) {
	pool := pgtest.Pool(t)
	oid := orderID(t, pool)
	_ = Enqueue(ctx, pool, "deliver_email", oid, oid)
	first, _ := Claim(ctx, pool, 10, time.Minute)
	_, _ = pool.Exec(ctx, `UPDATE outbox_job SET lease_until = now() - interval '1 second'`)
	second, _ := Claim(ctx, pool, 10, time.Minute)
	if len(second) != 1 || second[0].ID != first[0].ID || second[0].Attempts != 2 {
		t.Fatalf("reclaim %+v", second)
	}
}

func TestRetryBacksOffThenFails(t *testing.T) {
	pool := pgtest.Pool(t)
	oid := orderID(t, pool)
	_ = Enqueue(ctx, pool, "deliver_sms", oid, oid)
	jobs, _ := Claim(ctx, pool, 10, time.Minute)
	if err := Retry(ctx, pool, jobs[0], errors.New("carrier down")); err != nil {
		t.Fatal(err)
	}
	s, _, due := status(t, pool, jobs[0].ID)
	var lastErr string
	_ = pool.QueryRow(ctx, `SELECT last_error FROM outbox_job`).Scan(&lastErr)
	if s != "ready" || due || lastErr != "carrier down" {
		t.Fatalf("after retry: %s due=%v %q", s, due, lastErr)
	}
	_, _ = pool.Exec(ctx, `UPDATE outbox_job SET attempts = $1, run_at = now()`, MaxAttempts-1)
	jobs, _ = Claim(ctx, pool, 10, time.Minute)
	_ = Retry(ctx, pool, jobs[0], errors.New("still down"))
	if s, attempts, _ := status(t, pool, jobs[0].ID); s != "failed" || attempts != MaxAttempts {
		t.Fatalf("after max attempts: %s %d", s, attempts)
	}
}

func TestFaults(t *testing.T) {
	pool := pgtest.Pool(t)
	oid := orderID(t, pool)
	for _, op := range []func(dbx.DB) error{
		func(db dbx.DB) error { return Enqueue(ctx, db, "x", oid, "f") },
		func(db dbx.DB) error { _, err := Claim(ctx, db, 1, time.Minute); return err },
		func(db dbx.DB) error { return Done(ctx, db, 1) },
		func(db dbx.DB) error { return Retry(ctx, db, Job{ID: 1}, errors.New("x")) },
	} {
		pgtest.EachFault(t, pool, op, func(int, error) {})
	}
}
