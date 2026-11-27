// Package ticketops holds the staff actions that change a ticket after it was
// issued: void, reissue and refund. Each locks the affected ticket rows, the
// same rows a gate scan locks, so it is serialized against admission, and
// each writes an audit entry naming the staff member.
package ticketops

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/jackc/pgx/v5"

	"kacheem/backend/internal/audit"
	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/outbox"
	"kacheem/backend/internal/tickets"
)

var (
	// ErrAdmitted means a ticket was already used at the gate; a supervisor
	// must review it instead.
	ErrAdmitted = errors.New("ticketops: ticket already admitted")
	// ErrNotValid means the ticket is void or blocked by a pending refund.
	ErrNotValid = errors.New("ticketops: ticket is not valid")
	// ErrNotFound means no such ticket or order.
	ErrNotFound = errors.New("ticketops: not found")
	// ErrNoPayment means the order has no captured payment to refund.
	ErrNoPayment = errors.New("ticketops: no captured payment to refund")
	// ErrReason means the action needs a written reason.
	ErrReason = errors.New("ticketops: a reason is required")
)

func lockTicket(ctx context.Context, tx pgx.Tx, ticketID string) (orderID, status string, err error) {
	err = tx.QueryRow(ctx, `SELECT order_id::text, status FROM ticket WHERE id::text = $1 FOR UPDATE`, ticketID).Scan(&orderID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return orderID, status, err
}

func check(status string) error {
	switch status {
	case "valid":
		return nil
	case "used":
		return ErrAdmitted
	}
	return ErrNotValid
}

// Void makes an unused ticket unusable.
func Void(ctx context.Context, db dbx.DB, staffID, ticketID, reason string) error {
	if reason == "" {
		return ErrReason
	}
	return dbx.InTx(ctx, db, func(tx pgx.Tx) error {
		_, status, err := lockTicket(ctx, tx, ticketID)
		if err != nil {
			return err
		}
		if err := check(status); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE ticket SET status = 'void' WHERE id = $1`, ticketID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Staff(staffID), "ticket.void", "ticket", ticketID, reason, nil)
	})
}

// Reissue gives an unused ticket a new QR credential and fallback code, so the
// old ones stop working; revokes the order's pickup links; and queues a new
// PDF and delivery to the recorded contact.
func Reissue(ctx context.Context, db dbx.DB, issuer *tickets.Issuer, staffID, ticketID, reason string) error {
	if reason == "" {
		return ErrReason
	}
	return dbx.InTx(ctx, db, func(tx pgx.Tx) error {
		orderID, status, err := lockTicket(ctx, tx, ticketID)
		if err != nil {
			return err
		}
		if err := check(status); err != nil {
			return err
		}
		version, err := issuer.Reseal(ctx, tx, ticketID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE download_grant SET revoked_at = now()
			WHERE order_id = $1 AND revoked_at IS NULL`, orderID); err != nil {
			return err
		}
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		if err := outbox.Enqueue(ctx, tx, "render_tickets", orderID, orderID+":reissue:"+hex.EncodeToString(b)); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Staff(staffID), "ticket.reissue", "ticket", ticketID, reason,
			map[string]any{"credential_version": version})
	})
}

// RequestRefund blocks every ticket of a paid order at once and queues a full
// refund of its captured payment. The tickets become void when the refund
// settles. If any ticket was already admitted nothing changes: a supervisor
// reviews that order instead.
func RequestRefund(ctx context.Context, db dbx.DB, staffID, orderID, reason string) error {
	if reason == "" {
		return ErrReason
	}
	return dbx.InTx(ctx, db, func(tx pgx.Tx) error {
		statuses, err := dbx.All[struct {
			Status string `db:"status"`
		}](ctx, tx, `SELECT status FROM ticket WHERE order_id::text = $1 ORDER BY id FOR UPDATE`, orderID)
		if err != nil {
			return err
		}
		if len(statuses) == 0 {
			return ErrNotFound
		}
		for _, s := range statuses {
			if err := check(s.Status); err != nil {
				return err
			}
		}
		b := make([]byte, 10)
		_, _ = rand.Read(b)
		tag, err := tx.Exec(ctx, `INSERT INTO refund (refund_ref, attempt_id, amount_minor, currency, reason)
			SELECT $2, a.id, a.captured_minor, a.currency, 'organizer' FROM payment_attempt a
			WHERE a.order_id::text = $1 AND a.status = 'succeeded'
				AND NOT EXISTS (SELECT 1 FROM refund r WHERE r.attempt_id = a.id)
			ORDER BY a.created_at LIMIT 1`, orderID, "RF-"+hex.EncodeToString(b))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNoPayment
		}
		if _, err := tx.Exec(ctx, `UPDATE ticket SET status = 'refund_pending' WHERE order_id::text = $1`, orderID); err != nil {
			return err
		}
		// From approval on, the money is accounted to the refund, not to sales.
		if _, err := tx.Exec(ctx, `UPDATE ticket_order SET status = 'refunded' WHERE id::text = $1`, orderID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Staff(staffID), "order.refund_requested", "order", orderID, reason, nil)
	})
}
