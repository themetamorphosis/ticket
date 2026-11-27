package payments

import (
	"context"
	"time"

	"kacheem/backend/internal/dbx"
)

// maxRefundBackoff caps the wait between refund tries.
const maxRefundBackoff = time.Hour

// ProcessRefunds sends due refunds to the provider and returns how many it
// tried. Each refund is claimed (status pending, next try pushed out by the
// backoff, which acts as a lease) and committed before the provider is called,
// so no lock is held across the network and two workers never send the same
// refund at once. The provider call itself is idempotent per refund reference.
func (s *Service) ProcessRefunds(ctx context.Context, limit int) (int, error) {
	due, err := dbx.All[struct {
		ID          string `db:"id"`
		RefundRef   string `db:"refund_ref"`
		AmountMinor int64  `db:"amount_minor"`
		Currency    string `db:"currency"`
		Reason      string `db:"reason"`
		ProviderRef string `db:"provider_ref"`
		TrxID       string `db:"provider_trx_id"`
	}](ctx, s.DB, `
		WITH claimed AS (
			SELECT r.id FROM refund r JOIN payment_attempt a ON a.id = r.attempt_id
			WHERE r.status IN ('requested', 'pending') AND r.next_try_at <= now() AND a.provider = $1
			ORDER BY r.next_try_at LIMIT $2 FOR UPDATE OF r SKIP LOCKED
		)
		UPDATE refund r SET status = 'pending', tries = r.tries + 1, updated_at = now(),
			next_try_at = now() + least(make_interval(mins => power(2, r.tries)::int), make_interval(secs => $3))
		FROM claimed, payment_attempt a
		WHERE r.id = claimed.id AND a.id = r.attempt_id
		RETURNING r.id::text, r.refund_ref, r.amount_minor, r.currency, r.reason, a.provider_ref, a.provider_trx_id`,
		s.Gateway.Provider(), limit, maxRefundBackoff.Seconds())
	if err != nil {
		return 0, err
	}
	for i, r := range due {
		res, err := s.Gateway.Refund(ctx, RefundRequest{
			RefundRef: r.RefundRef, ProviderRef: r.ProviderRef, TrxID: r.TrxID,
			AmountMinor: r.AmountMinor, Currency: r.Currency, Reason: r.Reason,
		})
		if err != nil {
			res = RefundResult{Status: RefundPending, Detail: err.Error()}
		}
		// A settled organizer refund voids the tickets it blocked.
		if _, err := s.DB.Exec(ctx, `
			WITH r AS (
				UPDATE refund SET status = $2, provider_refund_id = nullif($3, ''),
					last_error = nullif($4, ''), updated_at = now() WHERE id = $1
				RETURNING attempt_id, status, reason
			)
			UPDATE ticket t SET status = 'void'
			FROM r JOIN payment_attempt a ON a.id = r.attempt_id
			WHERE r.status = 'completed' AND r.reason = 'organizer' AND t.order_id = a.order_id AND t.status = 'refund_pending'`,
			r.ID, string(res.Status), res.ProviderRefundID, res.Detail); err != nil {
			return i, err
		}
	}
	return len(due), nil
}

// Ledger is the internal money position finance reconciles against the
// provider's settlement report.
type Ledger struct {
	CapturedMinor     int64 `db:"captured" json:"captured_minor"`
	PaidOrdersMinor   int64 `db:"paid_orders" json:"paid_orders_minor"`
	ReviewMinor       int64 `db:"review" json:"review_minor"`
	RefundedMinor     int64 `db:"refunded" json:"refunded_minor"`
	RefundOpenMinor   int64 `db:"refund_open" json:"refund_open_minor"`
	RefundFailedMinor int64 `db:"refund_failed" json:"refund_failed_minor"`
}

// Balanced reports whether every captured taka is accounted for: sold
// tickets, money under review, or a refund (done, in flight or failed).
func (l Ledger) Balanced() bool {
	return l.CapturedMinor == l.PaidOrdersMinor+l.ReviewMinor+l.RefundedMinor+l.RefundOpenMinor+l.RefundFailedMinor
}

// GetLedger computes the ledger from the database.
func GetLedger(ctx context.Context, db dbx.DB) (Ledger, error) {
	return dbx.One[Ledger](ctx, db, `SELECT
		(SELECT coalesce(sum(captured_minor), 0) FROM payment_attempt WHERE status IN ('succeeded', 'review'))::bigint AS captured,
		(SELECT coalesce(sum(total_minor), 0) FROM ticket_order WHERE status = 'paid')::bigint AS paid_orders,
		(SELECT coalesce(sum(captured_minor), 0) FROM payment_attempt WHERE status = 'review')::bigint AS review,
		(SELECT coalesce(sum(amount_minor), 0) FROM refund WHERE status = 'completed')::bigint AS refunded,
		(SELECT coalesce(sum(amount_minor), 0) FROM refund WHERE status IN ('requested', 'pending'))::bigint AS refund_open,
		(SELECT coalesce(sum(amount_minor), 0) FROM refund WHERE status = 'failed')::bigint AS refund_failed`)
}

// Entry is one buyer-visible step in an order's history.
type Entry struct {
	At   time.Time `db:"at"`
	Kind string    `db:"kind"`
}

// Timeline lists an order's history oldest first: creation, each payment
// attempt and its outcome, refunds, and how the order closed.
func Timeline(ctx context.Context, db dbx.DB, orderID string) ([]Entry, error) {
	return dbx.All[Entry](ctx, db, `
		SELECT created_at AS at, 'order_created' AS kind FROM ticket_order WHERE id = $1
		UNION ALL
		SELECT created_at, 'payment_started' FROM payment_attempt WHERE order_id = $1
		UNION ALL
		SELECT updated_at, 'payment_' || status FROM payment_attempt
			WHERE order_id = $1 AND status IN ('succeeded', 'failed', 'abandoned', 'review')
		UNION ALL
		SELECT r.created_at, 'refund_requested' FROM refund r JOIN payment_attempt a ON a.id = r.attempt_id WHERE a.order_id = $1
		UNION ALL
		SELECT r.updated_at, 'refund_' || r.status FROM refund r JOIN payment_attempt a ON a.id = r.attempt_id
			WHERE a.order_id = $1 AND r.status IN ('completed', 'failed')
		UNION ALL
		SELECT closed_at, 'order_' || status FROM ticket_order WHERE id = $1 AND closed_at IS NOT NULL
		ORDER BY at, kind`, orderID)
}

// LatestStatus is the status of the order's most recent payment attempt, or
// "" if payment never started.
func LatestStatus(ctx context.Context, db dbx.DB, orderID string) (string, error) {
	rows, err := dbx.All[struct {
		Status string `db:"status"`
	}](ctx, db, `SELECT status FROM payment_attempt WHERE order_id = $1 ORDER BY created_at DESC LIMIT 1`, orderID)
	if err != nil || len(rows) == 0 {
		return "", err
	}
	return rows[0].Status, nil
}
