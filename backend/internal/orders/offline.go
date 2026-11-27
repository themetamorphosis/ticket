package orders

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"kacheem/backend/internal/audit"
	"kacheem/backend/internal/catalog"
	"kacheem/backend/internal/dbx"
)

// StateStarted is the NotOnSaleError state once the event has begun: staff
// can no longer sell offline.
const StateStarted = "started"

// OfflineMethods are the ways staff can receive money outside the gateway.
var OfflineMethods = map[string]bool{"cash": true, "bank": true, "bkash": true}

const maxReferenceRunes = 64

// OfflineSale is a sale a staff member records for money taken outside the
// payment gateway: cash, a bank transfer or a payment to the organizer's
// bKash number.
type OfflineSale struct {
	EventSlug      string
	StaffID        string
	IdempotencyKey string
	Input          Input
	// Method is "cash", "bank" or "bkash".
	Method string
	// Reference is optional proof: a bKash TrxID, bank reference or receipt number.
	Reference string
}

// PaidHook runs inside the transaction that marks an order paid (the ticket
// issuer), so tickets and their delivery commit with the sale or not at all.
type PaidHook func(ctx context.Context, tx pgx.Tx, orderID string) error

// beforeOfflineTx runs between the idempotency check and the sale
// transaction; a seam that lets tests commit a concurrent duplicate.
var beforeOfflineTx = func() {}

// SellOffline records a paid offline sale: it takes places from the shared
// stock, records the payment received by the staff member, and runs onPaid,
// all in one transaction. It is allowed from publication until the event
// starts, outside the online sale window too. The price is the ticket price
// without the online booking fee. A retry with the same idempotency key and
// contents returns the original order (created=false).
func SellOffline(ctx context.Context, db dbx.DB, s OfflineSale, onPaid PaidHook) (o Order, created bool, err error) {
	e, err := catalog.GetPublishedEvent(ctx, db, s.EventSlug)
	if err != nil {
		return Order{}, false, err
	}
	tier, err := e.Tier()
	if err != nil {
		return Order{}, false, err
	}
	in, method, ref, err := normalizeOffline(e, s)
	if err != nil {
		return Order{}, false, err
	}
	req := Request{EventSlug: e.Slug, BuyerID: "staff:" + s.StaffID, IdempotencyKey: s.IdempotencyKey}
	sum := offlineDigest(e.Slug, in, method, ref)
	if o, err := replay(ctx, db, req, sum); !errors.Is(err, pgx.ErrNoRows) {
		return o, false, err
	}

	var started bool
	if err := db.QueryRow(ctx, `SELECT now() >= starts_at FROM event WHERE id = $1`, e.ID).Scan(&started); err != nil {
		return Order{}, false, err
	}
	if started {
		return Order{}, false, &NotOnSaleError{State: StateStarted}
	}

	beforeOfflineTx()
	var id string
	err = dbx.InTx(ctx, db, func(tx pgx.Tx) error {
		locked, err := dbx.All[shardFree](ctx, tx, `SELECT shard, capacity - held - sold AS free
			FROM inventory_shard WHERE ticket_type_id = $1 ORDER BY shard FOR UPDATE`, tier.ID)
		if err != nil {
			return err
		}
		parts := allocate(locked, in.Quantity, 0)
		if parts == nil {
			return &SoldOutError{Remaining: totalFree(locked)}
		}
		shards := make([]int16, len(parts))
		qtys := make([]int32, len(parts))
		for i, p := range parts {
			shards[i], qtys[i] = p.Shard, int32(p.Qty)
		}
		total := int64(in.Quantity) * tier.PriceMinor

		if err := tx.QueryRow(ctx, `
			INSERT INTO ticket_order (public_ref, event_id, buyer_id, idempotency_key, request_digest,
				purchaser_name, email, phone, age, currency, total_minor, status, expires_at, closed_at, channel)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'paid', now(), now(), 'offline')
			RETURNING id::text`,
			newRef(), e.ID, req.BuyerID, req.IdempotencyKey, sum,
			in.Name, in.Email, in.Phone, in.Age, e.Currency, total,
		).Scan(&id); err != nil {
			return err
		}
		// The price snapshot, which shards the places came from, and the sale.
		if _, err := tx.Exec(ctx, `
			WITH line AS (
				INSERT INTO order_line (order_id, ticket_type_id, quantity, unit_price_minor, unit_fee_minor)
				VALUES ($1, $2, $3, $4, 0)
			), h AS (
				INSERT INTO hold (order_id, ticket_type_id, shard, quantity)
				SELECT $1, $2, p.shard, p.qty FROM unnest($5::smallint[], $6::int[]) AS p(shard, qty)
			)
			UPDATE inventory_shard s SET sold = s.sold + p.qty
			FROM unnest($5::smallint[], $6::int[]) AS p(shard, qty)
			WHERE s.ticket_type_id = $2 AND s.shard = p.shard`,
			id, tier.ID, in.Quantity, tier.PriceMinor, shards, qtys); err != nil {
			return err
		}
		receipt := newReceipt()
		if _, err := tx.Exec(ctx, `
			WITH a AS (
				INSERT INTO payment_attempt (merchant_ref, order_id, provider, provider_trx_id, amount_minor, currency,
					status, captured_minor, offline_method, offline_reference, received_by)
				VALUES ($1, $2, 'offline', $1, $3, $4, 'succeeded', $3, $5, nullif($6, ''), $7)
				RETURNING id
			)
			INSERT INTO payment_event (attempt_id, kind, detail)
			SELECT id, 'offline_received', jsonb_build_object('method', $5::text, 'staff_id', $7::text) FROM a`,
			receipt, id, total, e.Currency, method, ref, s.StaffID); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Staff(s.StaffID), "order.offline_sale", "order", id, "",
			map[string]any{"method": method, "reference": ref, "receipt": receipt, "quantity": in.Quantity, "total_minor": total}); err != nil {
			return err
		}
		if onPaid == nil {
			return nil
		}
		return onPaid(ctx, tx, id)
	})
	if dbx.IsUniqueViolation(err, "ticket_order_buyer_id_idempotency_key_key") {
		// A concurrent duplicate submit won; return its order.
		o, err := replay(ctx, db, req, sum)
		return o, false, err
	}
	if err != nil {
		return Order{}, false, err
	}
	o, err = getByID(ctx, db, id)
	return o, err == nil, err
}

// normalizeOffline validates the purchaser like the online form and the
// payment fields, reporting every problem at once.
func normalizeOffline(e catalog.Event, s OfflineSale) (Input, string, string, error) {
	errs := FieldErrors{}
	in, err := Normalize(e, s.Input)
	if err != nil {
		errs = err.(FieldErrors)
	}
	if !OfflineMethods[s.Method] {
		errs["method"] = "Choose cash, bank or bKash."
	}
	ref := strings.TrimSpace(s.Reference)
	switch {
	case utf8.RuneCountInString(ref) > maxReferenceRunes:
		errs["reference"] = "Use at most 64 characters."
	case strings.IndexFunc(ref, unicode.IsControl) >= 0:
		errs["reference"] = "Remove unsupported characters."
	}
	if len(errs) > 0 {
		return Input{}, "", "", errs
	}
	return in, s.Method, ref, nil
}

// offlineDigest binds an idempotency key to the sale's exact contents.
func offlineDigest(eventSlug string, in Input, method, ref string) []byte {
	b, _ := json.Marshal(struct {
		Event     string `json:"event"`
		Method    string `json:"method"`
		Reference string `json:"reference"`
		Input
	}{eventSlug, method, ref, in})
	sum := sha256.Sum256(b)
	return sum[:]
}

// newReceipt returns the receipt number that stands in for a provider
// transaction ID on an offline payment.
func newReceipt() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return "OF-" + hex.EncodeToString(b)
}
