package payments

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/orders"
)

var (
	// ErrHoldEnded means the order is not held, or too little hold time is
	// left to finish paying.
	ErrHoldEnded = errors.New("the hold has ended or is about to end")
	// ErrGatewayUnavailable means the provider could not open a session.
	ErrGatewayUnavailable = errors.New("payment provider unavailable")
	// ErrUnknownPayment means a return redirect names no attempt of ours.
	ErrUnknownPayment = errors.New("unknown payment")
	// ErrNotifyUnsupported means the configured provider sends no notifications.
	ErrNotifyUnsupported = errors.New("payment notifications not supported by this provider")
)

const (
	// MinCheckoutTime is the hold time a buyer needs left to start paying.
	MinCheckoutTime = time.Minute
	// CheckoutExtension is added to a hold once, when checkout first starts.
	CheckoutExtension = 5 * time.Minute
)

// Service runs checkout, settlement, reconciliation and refunds for one gateway.
type Service struct {
	DB      dbx.DB
	Gateway Gateway
	// ReturnURL is where the provider sends the buyer back, for example
	// "https://tickets.example.org/payments/return/bkash".
	ReturnURL string
}

func newRef(prefix string) string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

type attempt struct {
	ID          string  `db:"id"`
	MerchantRef string  `db:"merchant_ref"`
	OrderID     string  `db:"order_id"`
	OrderRef    string  `db:"order_ref"`
	ProviderRef *string `db:"provider_ref"`
	AmountMinor int64   `db:"amount_minor"`
	Currency    string  `db:"currency"`
	Status      string  `db:"status"`
	Old         bool    `db:"old"`
}

// Old is true once the provider reference can no longer settle
// (24 hours, the bKash paymentId validity).
const attemptColumns = `a.id::text, a.merchant_ref, a.order_id::text, o.public_ref AS order_ref, a.provider_ref,
	a.amount_minor, a.currency, a.status, a.created_at < now() - interval '24 hours' AS old`

func (s *Service) attemptWhere(ctx context.Context, db dbx.DB, where string, args ...any) (attempt, error) {
	return dbx.One[attempt](ctx, db, `SELECT `+attemptColumns+`
		FROM payment_attempt a JOIN ticket_order o ON o.id = a.order_id WHERE `+where, args...)
}

func open(status string) bool { return status == "starting" || status == "pending" }

func event(ctx context.Context, db dbx.DB, attemptID, kind string, detail map[string]any) error {
	b, _ := json.Marshal(detail)
	if detail == nil {
		b = []byte("{}")
	}
	_, err := db.Exec(ctx, `INSERT INTO payment_event (attempt_id, kind, detail) VALUES ($1, $2, $3::jsonb)`, attemptID, kind, string(b))
	return err
}

// Checkout starts (or resumes) payment for the buyer's held order and returns
// the provider page to send the buyer to. The hold is extended once by
// CheckoutExtension; no database lock is held while the provider is called.
func (s *Service) Checkout(ctx context.Context, buyerID, orderRef string) (string, error) {
	o, err := orders.Get(ctx, s.DB, buyerID, orderRef)
	if err != nil {
		return "", err
	}
	var (
		a        attempt
		resume   *string
		deadline time.Time
	)
	err = dbx.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		cur, err := dbx.One[struct {
			Status   string    `db:"status"`
			Left     float64   `db:"left"`
			Extended bool      `db:"checkout_extended"`
			Expires  time.Time `db:"expires_at"`
		}](ctx, tx, `SELECT status, extract(epoch FROM expires_at - now())::float8 AS left, checkout_extended, expires_at
			FROM ticket_order WHERE id = $1 FOR UPDATE`, o.ID)
		if err != nil {
			return err
		}
		if cur.Status != "held" || cur.Left < MinCheckoutTime.Seconds() {
			return ErrHoldEnded
		}
		var url string
		err = tx.QueryRow(ctx, `SELECT redirect_url FROM payment_attempt
			WHERE order_id = $1 AND provider = $2 AND status = 'pending' AND redirect_url IS NOT NULL
			ORDER BY created_at DESC LIMIT 1`, o.ID, s.Gateway.Provider()).Scan(&url)
		if err == nil {
			resume = &url
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		deadline = cur.Expires
		if !cur.Extended {
			if err := tx.QueryRow(ctx, `UPDATE ticket_order
				SET expires_at = expires_at + make_interval(secs => $2), checkout_extended = true
				WHERE id = $1 RETURNING expires_at`, o.ID, CheckoutExtension.Seconds()).Scan(&deadline); err != nil {
				return err
			}
		}
		a = attempt{MerchantRef: newRef("KC"), OrderID: o.ID, AmountMinor: o.TotalMinor, Currency: o.Currency}
		return tx.QueryRow(ctx, `INSERT INTO payment_attempt (merchant_ref, order_id, provider, amount_minor, currency)
			VALUES ($1, $2, $3, $4, $5) RETURNING id::text`,
			a.MerchantRef, o.ID, s.Gateway.Provider(), a.AmountMinor, a.Currency).Scan(&a.ID)
	})
	if err != nil {
		return "", err
	}
	if resume != nil {
		return *resume, nil
	}

	started, err := s.Gateway.Start(ctx, StartRequest{
		MerchantRef: a.MerchantRef, AmountMinor: a.AmountMinor, Currency: a.Currency,
		ReturnURL: s.ReturnURL, Name: o.Name, Email: o.Email, Phone: o.Phone, Deadline: deadline,
	})
	if err != nil {
		// Nothing can be captured without a session we know about, so the
		// attempt is closed and the buyer can simply try again.
		if dbErr := s.close(ctx, a.ID, "failed", "start_failed", map[string]any{"error": err.Error()}); dbErr != nil {
			return "", dbErr
		}
		return "", fmt.Errorf("%w: %v", ErrGatewayUnavailable, err)
	}
	err = dbx.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE payment_attempt
			SET provider_ref = $2, redirect_url = $3, status = 'pending', updated_at = now()
			WHERE id = $1 AND status = 'starting'`, a.ID, started.ProviderRef, started.RedirectURL); err != nil {
			return err
		}
		return event(ctx, tx, a.ID, "started", map[string]any{"provider_ref": started.ProviderRef})
	})
	if err != nil {
		return "", err
	}
	return started.RedirectURL, nil
}

// close moves an open attempt to a final status that involves no money.
func (s *Service) close(ctx context.Context, attemptID, status, kind string, detail map[string]any) error {
	return dbx.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE payment_attempt SET status = $2, updated_at = now()
			WHERE id = $1 AND status IN ('starting', 'pending')`, attemptID, status)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		return event(ctx, tx, attemptID, kind, detail)
	})
}

// Return handles the buyer coming back from the provider and returns the
// order reference to show. The redirect's claim is only a hint: the provider
// is asked before anything changes.
func (s *Service) Return(ctx context.Context, q map[string][]string) (string, error) {
	ref, outcome, err := s.Gateway.ParseReturn(q)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnknownPayment, err)
	}
	a, err := s.attemptWhere(ctx, s.DB, `a.provider = $1 AND a.provider_ref = $2`, s.Gateway.Provider(), ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnknownPayment
	}
	if err != nil {
		return "", err
	}
	if err := event(ctx, s.DB, a.ID, "return", map[string]any{"outcome": string(outcome)}); err != nil {
		return "", err
	}
	if !open(a.Status) {
		return a.OrderRef, nil
	}
	return a.OrderRef, s.settle(ctx, a, outcome == OutcomeSuccess)
}

// settle asks the provider for the truth and records it. With capture, a
// gateway that captures on Complete is only completed while the hold lasts,
// so a buyer returning after expiry is never charged.
func (s *Service) settle(ctx context.Context, a attempt, capture bool) error {
	var (
		res Result
		err error
	)
	if capture && s.Gateway.CapturesOnComplete() {
		var live bool
		if err := s.DB.QueryRow(ctx, `SELECT status = 'held' AND expires_at > now() FROM ticket_order WHERE id = $1`,
			a.OrderID).Scan(&live); err != nil {
			return err
		}
		if !live {
			return s.close(ctx, a.ID, "abandoned", "not_captured_hold_ended", nil)
		}
		res, err = s.Gateway.Complete(ctx, *a.ProviderRef)
	} else {
		res, err = s.Gateway.Query(ctx, *a.ProviderRef)
	}
	if err != nil {
		// Unknown: leave the attempt open for reconciliation.
		return event(ctx, s.DB, a.ID, "unknown", map[string]any{"error": err.Error()})
	}
	if !capture && res.Status != Succeeded && (res.Status == Failed || s.Gateway.CapturesOnComplete()) {
		// The buyer cancelled or failed and nothing was captured; with a
		// capture-on-complete gateway nothing ever will be without us.
		res.Status = Failed
	}
	return s.finalize(ctx, a.ID, res)
}

// finalize records a provider result in one transaction under the order's
// locks: a timely capture sells the held places; a capture for an order that
// is no longer held (late or duplicate) is recorded and queued for refund; a
// capture that does not match the attempt is held for finance review.
func (s *Service) finalize(ctx context.Context, attemptID string, res Result) error {
	return dbx.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		var orderID string
		if err := tx.QueryRow(ctx, `SELECT order_id::text FROM payment_attempt WHERE id = $1`, attemptID).Scan(&orderID); err != nil {
			return err
		}
		ord, err := orders.LockOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		a, err := s.attemptWhere(ctx, tx, `a.id = $1 FOR UPDATE OF a`, attemptID)
		if err != nil {
			return err
		}
		// A closed attempt changes only if the provider reports a capture we
		// had given up on (a provider that captures in its own page, after we
		// marked the attempt failed): that money must still be accounted for.
		reopened := res.Status == Succeeded && (a.Status == "failed" || a.Status == "abandoned")
		if !open(a.Status) && !reopened {
			return nil
		}
		if err := event(ctx, tx, a.ID, "result", map[string]any{
			"status": string(res.Status), "trx_id": res.TrxID, "amount_minor": res.AmountMinor, "currency": res.Currency,
		}); err != nil {
			return err
		}
		switch res.Status {
		case Pending, Authorized:
			return nil
		case Failed:
			_, err := tx.Exec(ctx, `UPDATE payment_attempt SET status = 'failed', updated_at = now() WHERE id = $1`, a.ID)
			return err
		}

		status := "succeeded"
		if res.AmountMinor != a.AmountMinor || res.Currency != a.Currency || (res.MerchantRef != "" && res.MerchantRef != a.MerchantRef) {
			status = "review"
		}
		if _, err := tx.Exec(ctx, `UPDATE payment_attempt
			SET status = $2, provider_trx_id = $3, captured_minor = $4, updated_at = now() WHERE id = $1`,
			a.ID, status, res.TrxID, res.AmountMinor); err != nil {
			return err
		}
		if status == "review" {
			return event(ctx, tx, a.ID, "mismatch", map[string]any{"expected_minor": a.AmountMinor, "currency": a.Currency})
		}
		if ord.Status == "held" && !ord.Expired {
			return orders.SettleHold(ctx, tx, orderID, "paid", true)
		}
		reason := "late_payment"
		if ord.Status == "paid" {
			reason = "duplicate_charge"
		}
		if ord.Status == "held" {
			if err := orders.SettleHold(ctx, tx, orderID, "expired", false); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO refund (refund_ref, attempt_id, amount_minor, currency, reason)
			VALUES ($1, $2, $3, $4, $5)`, newRef("RF"), a.ID, res.AmountMinor, res.Currency, reason)
		return err
	})
}

// Notify handles a provider's server-to-server notification. The body only
// says which payment to look at; the provider is queried for the facts, and
// the notification is recorded before anything changes. Repeats are harmless.
func (s *Service) Notify(ctx context.Context, body []byte) error {
	n, ok := s.Gateway.(Notifier)
	if !ok {
		return ErrNotifyUnsupported
	}
	ref, err := n.ParseNotification(body)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnknownPayment, err)
	}
	a, err := s.attemptWhere(ctx, s.DB, `a.provider = $1 AND a.provider_ref = $2`, s.Gateway.Provider(), ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUnknownPayment
	}
	if err != nil {
		return err
	}
	if err := event(ctx, s.DB, a.ID, "notification", nil); err != nil {
		return err
	}
	if a.Status == "succeeded" || a.Status == "review" {
		return nil
	}
	res, err := s.Gateway.Query(ctx, ref)
	if err != nil {
		return err
	}
	return s.finalize(ctx, a.ID, res)
}

// Reconcile settles attempts left open (closed browser, lost redirect,
// timeouts) that have not changed for at least idle. It returns how many it
// looked at.
func (s *Service) Reconcile(ctx context.Context, idle time.Duration, limit int) (int, error) {
	ids, err := dbx.All[struct {
		ID string `db:"id"`
	}](ctx, s.DB, `SELECT id::text FROM payment_attempt
		WHERE status IN ('starting', 'pending') AND provider = $1 AND updated_at < now() - make_interval(secs => $2)
		ORDER BY updated_at LIMIT $3`, s.Gateway.Provider(), idle.Seconds(), limit)
	if err != nil {
		return 0, err
	}
	for i, row := range ids {
		if err := s.reconcileOne(ctx, row.ID); err != nil {
			return i, err
		}
	}
	return len(ids), nil
}

func (s *Service) reconcileOne(ctx context.Context, id string) error {
	a, err := s.attemptWhere(ctx, s.DB, `a.id = $1`, id)
	if err != nil {
		return err
	}
	if a.ProviderRef == nil {
		// Start never recorded a session: nothing can have been captured
		// through it, so close it.
		return s.close(ctx, a.ID, "failed", "start_lost", nil)
	}
	res, err := s.Gateway.Query(ctx, *a.ProviderRef)
	if err != nil {
		return event(ctx, s.DB, a.ID, "unknown", map[string]any{"error": err.Error()})
	}
	switch {
	case res.Status == Succeeded || res.Status == Failed:
		return s.finalize(ctx, a.ID, res)
	case res.Status == Authorized && s.Gateway.CapturesOnComplete():
		// The buyer approved and closed the browser: capture if the hold
		// lasts, otherwise settle abandons the attempt without capturing.
		return s.settle(ctx, a, true)
	case a.Old:
		// A provider reference is valid for 24 hours (bKash paymentId).
		return s.close(ctx, a.ID, "failed", "expired_at_provider", nil)
	case s.Gateway.CapturesOnComplete():
		var live bool
		if err := s.DB.QueryRow(ctx, `SELECT status = 'held' AND expires_at > now() FROM ticket_order WHERE id = $1`,
			a.OrderID).Scan(&live); err != nil {
			return err
		}
		if !live {
			return s.close(ctx, a.ID, "abandoned", "not_captured_hold_ended", nil)
		}
	}
	return nil
}
