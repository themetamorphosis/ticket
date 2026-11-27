// Package outbox is the durable fulfilment queue. A job is enqueued in the
// same transaction as the change that needs it, claimed under a lease with
// FOR UPDATE SKIP LOCKED, and committed before the slow work starts, so a
// crashed worker's job is reclaimed when its lease expires.
package outbox

import (
	"context"
	"time"

	"kacheem/backend/internal/dbx"
)

// MaxAttempts is how many tries a job gets before it is marked failed for
// support to see.
const MaxAttempts = 10

// Job is a claimed unit of work.
type Job struct {
	ID       int64  `db:"id"`
	Kind     string `db:"kind"`
	OrderID  string `db:"order_id"`
	Attempts int    `db:"attempts"`
}

// Enqueue adds a job unless one with the same kind and dedupe key exists.
func Enqueue(ctx context.Context, db dbx.DB, kind, orderID, dedupe string) error {
	_, err := db.Exec(ctx, `INSERT INTO outbox_job (kind, order_id, dedupe_key) VALUES ($1, $2, $3)
		ON CONFLICT (kind, dedupe_key) DO NOTHING`, kind, orderID, dedupe)
	return err
}

// Claim leases up to limit due jobs: ready ones, and running ones whose lease
// expired (their worker died).
func Claim(ctx context.Context, db dbx.DB, limit int, lease time.Duration) ([]Job, error) {
	return dbx.All[Job](ctx, db, `
		WITH due AS (
			SELECT id FROM outbox_job
			WHERE (status = 'ready' AND run_at <= now()) OR (status = 'running' AND lease_until < now())
			ORDER BY run_at LIMIT $1 FOR UPDATE SKIP LOCKED
		)
		UPDATE outbox_job j SET status = 'running', attempts = j.attempts + 1,
			lease_until = now() + make_interval(secs => $2), updated_at = now()
		FROM due WHERE j.id = due.id
		RETURNING j.id, j.kind, j.order_id::text, j.attempts`, limit, lease.Seconds())
}

// Done marks a job finished.
func Done(ctx context.Context, db dbx.DB, id int64) error {
	_, err := db.Exec(ctx, `UPDATE outbox_job SET status = 'done', lease_until = NULL, last_error = NULL,
		updated_at = now() WHERE id = $1`, id)
	return err
}

// Retry schedules another try with exponential backoff (30 s doubling, at
// most an hour), or marks the job failed after MaxAttempts.
func Retry(ctx context.Context, db dbx.DB, j Job, cause error) error {
	_, err := db.Exec(ctx, `UPDATE outbox_job SET
			status = CASE WHEN attempts >= $2 THEN 'failed' ELSE 'ready' END,
			run_at = now() + least(make_interval(secs => 30 * power(2, attempts - 1)), interval '1 hour'),
			lease_until = NULL, last_error = $3, updated_at = now()
		WHERE id = $1`, j.ID, MaxAttempts, cause.Error())
	return err
}
