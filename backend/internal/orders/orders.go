// Package orders reserves places with expiring holds. PostgreSQL is the only
// source of truth: a hold is committed in one short transaction that locks
// inventory_shard rows in ascending order, then the order row; every other
// writer (expiry, cancel, payment, capacity edits) uses the same order.
package orders

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"

	"kacheem/backend/internal/catalog"
	"kacheem/backend/internal/dbx"
)

var (
	// ErrOrderNotFound means no order with that reference for this buyer.
	ErrOrderNotFound = errors.New("order not found")
	// ErrIdempotencyConflict means the key was already used for different contents.
	ErrIdempotencyConflict = errors.New("idempotency key reused with different booking details")
	// ErrNotHeld means the order is no longer an active hold.
	ErrNotHeld = errors.New("order is not an active hold")
)

// NotOnSaleError means the sale window is not open.
type NotOnSaleError struct{ State string }

func (e *NotOnSaleError) Error() string { return "tickets are not on sale: " + e.State }

// SoldOutError means the requested quantity does not fit. Remaining is the
// number of free places; when it is zero, places may still be released by
// expiring holds unless the event is fully sold.
type SoldOutError struct{ Remaining int }

func (e *SoldOutError) Error() string { return fmt.Sprintf("only %d places remain", e.Remaining) }

// ActiveHoldError means this buyer already holds places for the event.
type ActiveHoldError struct{ Ref string }

func (e *ActiveHoldError) Error() string { return "an active hold already exists: " + e.Ref }

// Request is a validated-on-entry booking request from one buyer session.
type Request struct {
	EventSlug      string
	BuyerID        string
	IdempotencyKey string
	Input          Input
}

// Order is the buyer-visible view of an order. Status "expired" is reported as
// soon as a hold passes its deadline, before the worker releases it.
type Order struct {
	ID             string    `db:"id"`
	Ref            string    `db:"public_ref"`
	Status         string    `db:"status"`
	EventSlug      string    `db:"event_slug"`
	EventTitle     string    `db:"event_title"`
	EventStartsAt  time.Time `db:"event_starts_at"`
	EventTimeZone  string    `db:"event_time_zone"`
	Quantity       int       `db:"quantity"`
	UnitPriceMinor int64     `db:"unit_price_minor"`
	UnitFeeMinor   int64     `db:"unit_fee_minor"`
	TotalMinor     int64     `db:"total_minor"`
	Currency       string    `db:"currency"`
	Name           string    `db:"purchaser_name"`
	Email          string    `db:"email"`
	Phone          string    `db:"phone"`
	Age            int       `db:"age"`
	CreatedAt      time.Time `db:"created_at"`
	ExpiresAt      time.Time `db:"expires_at"`
	ServerTime     time.Time `db:"server_time"`
}

const orderView = `
	SELECT o.id::text, o.public_ref,
		CASE WHEN o.status = 'held' AND o.expires_at <= now() THEN 'expired' ELSE o.status END AS status,
		e.slug AS event_slug, e.title AS event_title, e.starts_at AS event_starts_at, e.time_zone AS event_time_zone,
		l.quantity, l.unit_price_minor, l.unit_fee_minor, o.total_minor, o.currency,
		o.purchaser_name, o.email, o.phone, o.age, o.created_at, o.expires_at, now() AS server_time
	FROM ticket_order o
	JOIN event e ON e.id = o.event_id
	JOIN order_line l ON l.order_id = o.id`

// Get returns the buyer's order by public reference.
func Get(ctx context.Context, db dbx.DB, buyerID, ref string) (Order, error) {
	o, err := dbx.One[Order](ctx, db, orderView+` WHERE o.public_ref = $1 AND o.buyer_id = $2`, ref, buyerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrOrderNotFound
	}
	return o, err
}

func getByID(ctx context.Context, db dbx.DB, id string) (Order, error) {
	return dbx.One[Order](ctx, db, orderView+` WHERE o.id = $1`, id)
}

// optimisticAttempts bounds retries that lock only the chosen shards before
// falling back to locking every shard of the tier, which always decides.
const optimisticAttempts = 3

var (
	// intn picks the rotation start; a seam for deterministic tests.
	intn = rand.IntN
	// afterChoose runs between choosing shards and locking them; a seam that
	// lets tests take the chosen stock first and force a retry.
	afterChoose = func() {}

	errRetry   = errors.New("chosen shards changed; retry")
	errLockAll = errors.New("not enough free places in the unlocked read")
)

// Reserve validates the request and holds the places. A retry with the same
// idempotency key and contents returns the original order (created=false).
func Reserve(ctx context.Context, db dbx.DB, req Request) (o Order, created bool, err error) {
	e, err := catalog.GetPublishedEvent(ctx, db, req.EventSlug)
	if err != nil {
		return Order{}, false, err
	}
	tier, err := e.Tier()
	if err != nil {
		return Order{}, false, err
	}
	in, err := Normalize(e, req.Input)
	if err != nil {
		return Order{}, false, err
	}
	sum := digest(e.Slug, in)

	if o, err := replay(ctx, db, req, sum); !errors.Is(err, pgx.ErrNoRows) {
		return o, false, err
	}

	avail, err := catalog.GetAvailability(ctx, db, e.Slug)
	if err != nil {
		return Order{}, false, err
	}
	if avail.State == catalog.StateNotOpen || avail.State == catalog.StateClosed {
		return Order{}, false, &NotOnSaleError{State: avail.State}
	}
	if err := clearOwnStaleHold(ctx, db, e.ID, req.BuyerID); err != nil {
		return Order{}, false, err
	}

	for attempt := 0; ; attempt++ {
		var id string
		id, err = reserveTx(ctx, db, e, tier, req, in, sum, attempt >= optimisticAttempts)
		switch {
		case errors.Is(err, errLockAll):
			attempt = optimisticAttempts - 1
			continue
		case errors.Is(err, errRetry):
			continue
		case dbx.IsUniqueViolation(err, "ticket_order_buyer_id_idempotency_key_key"):
			// A concurrent duplicate submit won; return its order.
			o, err := replay(ctx, db, req, sum)
			return o, false, err
		case dbx.IsUniqueViolation(err, "ticket_order_one_active_hold"):
			return Order{}, false, activeHold(ctx, db, e.ID, req.BuyerID)
		case err != nil:
			return Order{}, false, err
		}
		o, err = getByID(ctx, db, id)
		return o, err == nil, err
	}
}

// replay returns pgx.ErrNoRows when the key is unused.
func replay(ctx context.Context, db dbx.DB, req Request, sum []byte) (Order, error) {
	prev, err := dbx.One[struct {
		ID     string `db:"id"`
		Digest []byte `db:"request_digest"`
	}](ctx, db, `SELECT id::text, request_digest FROM ticket_order WHERE buyer_id = $1 AND idempotency_key = $2`,
		req.BuyerID, req.IdempotencyKey)
	if err != nil {
		return Order{}, err
	}
	if !bytes.Equal(prev.Digest, sum) {
		return Order{}, ErrIdempotencyConflict
	}
	return getByID(ctx, db, prev.ID)
}

type heldRow struct {
	ID      string `db:"id"`
	Ref     string `db:"public_ref"`
	Expired bool   `db:"expired"`
}

func findHeld(ctx context.Context, db dbx.DB, eventID, buyerID string) (heldRow, error) {
	return dbx.One[heldRow](ctx, db, `
		SELECT id::text, public_ref, expires_at <= now() AS expired
		FROM ticket_order WHERE event_id = $1 AND buyer_id = $2 AND status = 'held'`, eventID, buyerID)
}

// clearOwnStaleHold releases this buyer's expired hold so they can book
// again before the worker gets to it, and refuses while one is still active.
func clearOwnStaleHold(ctx context.Context, db dbx.DB, eventID, buyerID string) error {
	h, err := findHeld(ctx, db, eventID, buyerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !h.Expired {
		return &ActiveHoldError{Ref: h.Ref}
	}
	_, err = release(ctx, db, h.ID, "expired", true)
	return err
}

func activeHold(ctx context.Context, db dbx.DB, eventID, buyerID string) error {
	h, err := findHeld(ctx, db, eventID, buyerID)
	if err != nil {
		return err
	}
	return &ActiveHoldError{Ref: h.Ref}
}

func reserveTx(ctx context.Context, db dbx.DB, e catalog.Event, tier catalog.Tier, req Request,
	in Input, sum []byte, lockAll bool) (string, error) {
	var orderID string
	err := dbx.InTx(ctx, db, func(tx pgx.Tx) error {
		const freeSQL = `SELECT shard, capacity - held - sold AS free FROM inventory_shard WHERE ticket_type_id = $1`
		var locked []shardFree
		var err error
		if lockAll {
			locked, err = dbx.All[shardFree](ctx, tx, freeSQL+` ORDER BY shard FOR UPDATE`, tier.ID)
		} else {
			locked, err = lockChosen(ctx, tx, freeSQL, tier.ID, in.Quantity)
		}
		if err != nil {
			return err
		}
		parts := allocate(locked, in.Quantity, 0)
		if parts == nil {
			if lockAll {
				return &SoldOutError{Remaining: totalFree(locked)}
			}
			return errRetry
		}

		shards := make([]int16, len(parts))
		qtys := make([]int32, len(parts))
		for i, p := range parts {
			shards[i], qtys[i] = p.Shard, int32(p.Qty)
		}
		total := catalog.NewQuote(tier, e.Currency, in.Quantity).TotalMinor
		if err := tx.QueryRow(ctx, `
			INSERT INTO ticket_order (public_ref, event_id, buyer_id, idempotency_key, request_digest,
				purchaser_name, email, phone, age, currency, total_minor, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now() + make_interval(mins => $12))
			RETURNING id::text`,
			newRef(), e.ID, req.BuyerID, req.IdempotencyKey, sum,
			in.Name, in.Email, in.Phone, in.Age, e.Currency, total, e.HoldMinutes,
		).Scan(&orderID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_line (order_id, ticket_type_id, quantity, unit_price_minor, unit_fee_minor)
			VALUES ($1, $2, $3, $4, $5)`, orderID, tier.ID, in.Quantity, tier.PriceMinor, tier.FeeMinor); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO hold (order_id, ticket_type_id, shard, quantity)
			SELECT $1, $2, p.shard, p.qty FROM unnest($3::smallint[], $4::int[]) AS p(shard, qty)`,
			orderID, tier.ID, shards, qtys); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE inventory_shard s SET held = s.held + p.qty
			FROM unnest($2::smallint[], $3::int[]) AS p(shard, qty)
			WHERE s.ticket_type_id = $1 AND s.shard = p.shard`, tier.ID, shards, qtys)
		return err
	})
	return orderID, err
}

// lockChosen reads free stock without locks, picks shards starting from a
// random one, then locks just those in ascending order and returns their
// current stock. A buyer therefore rarely waits on another buyer's shard.
func lockChosen(ctx context.Context, tx pgx.Tx, freeSQL, tierID string, qty int) ([]shardFree, error) {
	free, err := dbx.All[shardFree](ctx, tx, freeSQL+` ORDER BY shard`, tierID)
	if err != nil {
		return nil, err
	}
	if totalFree(free) < qty {
		return nil, errLockAll
	}
	chosen := allocate(free, qty, intn(len(free)))
	ids := make([]int16, len(chosen))
	for i, p := range chosen {
		ids[i] = p.Shard
	}
	afterChoose()
	return dbx.All[shardFree](ctx, tx, freeSQL+` AND shard = ANY($2) ORDER BY shard FOR UPDATE`, tierID, ids)
}

var errNoop = errors.New("nothing to release")

// release moves a held order to status `to` and returns its places to stock,
// exactly once. With onlyIfExpired it does nothing to a hold that is still live.
func release(ctx context.Context, db dbx.DB, orderID, to string, onlyIfExpired bool) (bool, error) {
	err := dbx.InTx(ctx, db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			SELECT 1 FROM inventory_shard s
			JOIN hold h ON h.ticket_type_id = s.ticket_type_id AND h.shard = s.shard
			WHERE h.order_id = $1 ORDER BY s.ticket_type_id, s.shard FOR UPDATE OF s`, orderID); err != nil {
			return err
		}
		cur, err := dbx.One[struct {
			Status  string `db:"status"`
			Expired bool   `db:"expired"`
		}](ctx, tx, `SELECT status, expires_at <= now() AS expired FROM ticket_order WHERE id = $1 FOR UPDATE`, orderID)
		if err != nil {
			return err
		}
		if cur.Status != "held" || (onlyIfExpired && !cur.Expired) {
			return errNoop
		}
		if _, err := tx.Exec(ctx, `
			UPDATE inventory_shard s SET held = s.held - h.quantity
			FROM hold h
			WHERE h.order_id = $1 AND s.ticket_type_id = h.ticket_type_id AND s.shard = h.shard`, orderID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE ticket_order SET status = $2, closed_at = now() WHERE id = $1`, orderID, to)
		return err
	})
	if errors.Is(err, errNoop) {
		return false, nil
	}
	return err == nil, err
}

// Cancel releases the buyer's active hold.
func Cancel(ctx context.Context, db dbx.DB, buyerID, ref string) error {
	o, err := Get(ctx, db, buyerID, ref)
	if err != nil {
		return err
	}
	released, err := release(ctx, db, o.ID, "cancelled", false)
	if err != nil {
		return err
	}
	if !released {
		return ErrNotHeld
	}
	return nil
}

// ExpireDue releases up to limit holds whose deadline has passed and returns
// how many it released. It is safe to run from several workers at once.
func ExpireDue(ctx context.Context, db dbx.DB, limit int) (int, error) {
	due, err := dbx.All[struct {
		ID string `db:"id"`
	}](ctx, db, `
		SELECT id::text FROM ticket_order
		WHERE status = 'held' AND expires_at <= now()
		ORDER BY expires_at LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range due {
		released, err := release(ctx, db, d.ID, "expired", true)
		if err != nil {
			return n, err
		}
		if released {
			n++
		}
	}
	return n, nil
}
