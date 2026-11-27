// Package ops holds the read models and small writes behind the organizer
// dashboard: the sales summary, order search and detail, the finance queue,
// and gates with their staff assignments.
package ops

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"kacheem/backend/internal/audit"
	"kacheem/backend/internal/dbx"
)

// ErrNotFound means no such order, gate or staff member.
var ErrNotFound = errors.New("ops: not found")

// Summary is the live sales picture for one event.
type Summary struct {
	EventID        string `db:"event_id" json:"event_id"`
	Title          string `db:"title" json:"title"`
	Status         string `db:"status" json:"status"`
	Revision       int    `db:"published_revision" json:"published_revision"`
	Capacity       int    `db:"capacity" json:"capacity"`
	Sold           int    `db:"sold" json:"sold"`
	Held           int    `db:"held" json:"held"`
	PaidOrders     int    `db:"paid_orders" json:"paid_orders"`
	RevenueMinor   int64  `db:"revenue_minor" json:"revenue_minor"`
	Currency       string `db:"currency" json:"currency"`
	Admitted       int    `db:"admitted" json:"admitted"`
	FailedDelivery int    `db:"failed_delivery" json:"failed_delivery"`
}

// GetSummary returns the summary for the event with this slug.
func GetSummary(ctx context.Context, db dbx.DB, slug string) (Summary, error) {
	s, err := dbx.One[Summary](ctx, db, `
		SELECT e.id::text AS event_id, e.title, e.status, e.published_revision, e.capacity, e.currency,
			(SELECT coalesce(sum(sh.sold), 0) FROM inventory_shard sh JOIN ticket_type t ON t.id = sh.ticket_type_id WHERE t.event_id = e.id)::int AS sold,
			(SELECT coalesce(sum(sh.held), 0) FROM inventory_shard sh JOIN ticket_type t ON t.id = sh.ticket_type_id WHERE t.event_id = e.id)::int AS held,
			(SELECT count(*) FROM ticket_order o WHERE o.event_id = e.id AND o.status = 'paid')::int AS paid_orders,
			(SELECT coalesce(sum(o.total_minor), 0) FROM ticket_order o WHERE o.event_id = e.id AND o.status = 'paid')::bigint AS revenue_minor,
			(SELECT count(*) FROM admission a JOIN gate g ON g.id = a.gate_id WHERE g.event_id = e.id)::int AS admitted,
			(SELECT count(DISTINCT j.order_id) FROM outbox_job j JOIN ticket_order o ON o.id = j.order_id
				WHERE o.event_id = e.id AND j.kind LIKE 'deliver_%' AND (j.status = 'failed' OR j.attempts > 1) AND j.status <> 'done')::int AS failed_delivery
		FROM event e WHERE e.slug = $1`, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return Summary{}, ErrNotFound
	}
	return s, err
}

// OrderRow is one search result.
type OrderRow struct {
	Ref        string    `db:"public_ref" json:"public_ref"`
	Status     string    `db:"status" json:"status"`
	Channel    string    `db:"channel" json:"channel"`
	Name       string    `db:"purchaser_name" json:"purchaser_name"`
	Email      string    `db:"email" json:"email"`
	Phone      string    `db:"phone" json:"phone"`
	Quantity   int       `db:"quantity" json:"quantity"`
	TotalMinor int64     `db:"total_minor" json:"total_minor"`
	Currency   string    `db:"currency" json:"currency"`
	CreatedAt  time.Time `db:"created_at" json:"created_at"`
}

// Mask hides most of an email or phone number, for roles that only need to
// recognize a buyer they have already verified.
func Mask(s string) string {
	if at := strings.IndexByte(s, '@'); at > 0 {
		return s[:1] + strings.Repeat("*", max(at-1, 1)) + s[at:]
	}
	if len(s) > 7 {
		return s[:4] + strings.Repeat("*", len(s)-7) + s[len(s)-3:]
	}
	return strings.Repeat("*", len(s))
}

// Search finds orders by exact reference, email or phone (or, from three
// characters, a name), newest first. Contact details are masked unless
// unmasked is true.
func Search(ctx context.Context, db dbx.DB, query string, unmasked bool) ([]OrderRow, error) {
	q := strings.TrimSpace(query)
	rows, err := dbx.All[OrderRow](ctx, db, `
		SELECT o.public_ref, o.status, o.channel, o.purchaser_name, o.email, o.phone, l.quantity, o.total_minor, o.currency, o.created_at
		FROM ticket_order o JOIN order_line l ON l.order_id = o.id
		WHERE o.public_ref = upper($1) OR lower(o.email) = lower($1)
			OR o.phone = replace(replace($1, ' ', ''), '-', '')
			OR (length($1) >= 3 AND o.purchaser_name ILIKE '%' || $1 || '%')
		ORDER BY o.created_at DESC LIMIT 50`, q)
	if err != nil {
		return nil, err
	}
	if !unmasked {
		for i := range rows {
			rows[i].Email, rows[i].Phone = Mask(rows[i].Email), Mask(rows[i].Phone)
		}
	}
	return rows, nil
}

// TicketRow is a ticket with its admission, for the order detail.
type TicketRow struct {
	ID         string     `db:"id" json:"id"`
	Ordinal    int        `db:"ordinal" json:"ordinal"`
	Status     string     `db:"status" json:"status"`
	Version    int        `db:"credential_version" json:"credential_version"`
	AdmittedAt *time.Time `db:"admitted_at" json:"admitted_at"`
	Gate       *string    `db:"gate" json:"gate"`
}

// AttemptRow is a payment attempt.
type AttemptRow struct {
	Provider  string    `db:"provider" json:"provider"`
	Status    string    `db:"status" json:"status"`
	Amount    int64     `db:"amount_minor" json:"amount_minor"`
	TrxID     *string   `db:"provider_trx_id" json:"provider_trx_id"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	// Set for offline payments: cash, bank or bkash, the proof typed in, and
	// the staff member who took the money.
	OfflineMethod    *string `db:"offline_method" json:"offline_method"`
	OfflineReference *string `db:"offline_reference" json:"offline_reference"`
	ReceivedBy       *string `db:"received_by" json:"received_by"`
}

// RefundRow is a refund.
type RefundRow struct {
	Reason    string  `db:"reason" json:"reason"`
	Status    string  `db:"status" json:"status"`
	Amount    int64   `db:"amount_minor" json:"amount_minor"`
	LastError *string `db:"last_error" json:"last_error"`
}

// Detail is everything about one order for support and finance.
type Detail struct {
	Order    OrderRow
	ID       string
	Tickets  []TicketRow
	Attempts []AttemptRow
	Refunds  []RefundRow
	History  []audit.Entry
}

// GetDetail returns an order's detail.
func GetDetail(ctx context.Context, db dbx.DB, ref string, unmasked bool) (Detail, error) {
	var d Detail
	err := db.QueryRow(ctx, `SELECT id::text FROM ticket_order WHERE public_ref = upper($1)`, ref).Scan(&d.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, err
	}
	rows, err := Search(ctx, db, ref, unmasked)
	if err != nil {
		return Detail{}, err
	}
	d.Order = rows[0]
	if d.Tickets, err = dbx.All[TicketRow](ctx, db, `
		SELECT t.id::text, t.ordinal, t.status, t.credential_version, a.admitted_at, g.name AS gate
		FROM ticket t LEFT JOIN admission a ON a.ticket_id = t.id LEFT JOIN gate g ON g.id = a.gate_id
		WHERE t.order_id = $1 ORDER BY t.ordinal`, d.ID); err != nil {
		return Detail{}, err
	}
	if d.Attempts, err = dbx.All[AttemptRow](ctx, db, `
		SELECT a.provider, a.status, a.amount_minor, a.provider_trx_id, a.created_at,
			a.offline_method, a.offline_reference, s.name AS received_by
		FROM payment_attempt a LEFT JOIN staff s ON s.id = a.received_by
		WHERE a.order_id = $1 ORDER BY a.created_at`, d.ID); err != nil {
		return Detail{}, err
	}
	if d.Refunds, err = dbx.All[RefundRow](ctx, db, `SELECT r.reason, r.status, r.amount_minor, r.last_error
		FROM refund r JOIN payment_attempt a ON a.id = r.attempt_id WHERE a.order_id = $1 ORDER BY r.created_at`, d.ID); err != nil {
		return Detail{}, err
	}
	d.History, err = dbx.All[audit.Entry](ctx, db, `
		SELECT to_char(e.occurred_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS at,
			coalesce(s.name, e.actor_kind) AS actor, e.action, e.object_kind, e.object_id, coalesce(e.reason, '') AS reason
		FROM audit_event e LEFT JOIN staff s ON e.actor_kind = 'staff' AND s.id::text = e.actor_id
		WHERE (e.object_kind = 'order' AND e.object_id = $1)
			OR (e.object_kind = 'ticket' AND e.object_id IN (SELECT id::text FROM ticket WHERE order_id::text = $1))
		ORDER BY e.id`, d.ID)
	return d, err
}

// FinanceItem is a payment or refund that needs finance.
type FinanceItem struct {
	Kind      string    `db:"kind" json:"kind"`
	OrderRef  string    `db:"public_ref" json:"public_ref"`
	Status    string    `db:"status" json:"status"`
	Amount    int64     `db:"amount_minor" json:"amount_minor"`
	Detail    string    `db:"detail" json:"detail"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// FinanceQueue lists captured payments under review and refunds that are
// failed or waiting (for example PayStation refunds made in its panel).
func FinanceQueue(ctx context.Context, db dbx.DB) ([]FinanceItem, error) {
	return dbx.All[FinanceItem](ctx, db, `
		SELECT 'payment_review' AS kind, o.public_ref, a.status, coalesce(a.captured_minor, 0) AS amount_minor,
			a.provider || ' ' || coalesce(a.provider_trx_id, '') AS detail, a.updated_at
		FROM payment_attempt a JOIN ticket_order o ON o.id = a.order_id WHERE a.status = 'review'
		UNION ALL
		SELECT 'refund', o.public_ref, r.status, r.amount_minor, r.reason || coalesce(': ' || r.last_error, ''), r.updated_at
		FROM refund r JOIN payment_attempt a ON a.id = r.attempt_id JOIN ticket_order o ON o.id = a.order_id
		WHERE r.status IN ('failed', 'pending', 'requested')
		ORDER BY updated_at`)
}

// Gate is a gate with the staff assigned to it.
type Gate struct {
	ID    string   `db:"id" json:"id"`
	Name  string   `db:"name" json:"name"`
	Staff []string `db:"staff" json:"staff"`
}

// Gates lists an event's gates and who may scan at each.
func Gates(ctx context.Context, db dbx.DB, eventID string) ([]Gate, error) {
	return dbx.All[Gate](ctx, db, `
		SELECT g.id::text, g.name, coalesce(array_agg(s.name ORDER BY s.name) FILTER (WHERE s.id IS NOT NULL), '{}') AS staff
		FROM gate g LEFT JOIN gate_assignment ga ON ga.gate_id = g.id LEFT JOIN staff s ON s.id = ga.staff_id
		WHERE g.event_id::text = $1 GROUP BY g.id ORDER BY g.name`, eventID)
}

// GatesFor lists the gates a staff member may scan at; organizers may scan
// at any gate of the event.
func GatesFor(ctx context.Context, db dbx.DB, staffID, role, eventID string) ([]Gate, error) {
	return dbx.All[Gate](ctx, db, `
		SELECT g.id::text, g.name, '{}'::text[] AS staff FROM gate g
		WHERE g.event_id::text = $3 AND ($2 = 'organizer'
			OR EXISTS (SELECT 1 FROM gate_assignment ga WHERE ga.gate_id = g.id AND ga.staff_id::text = $1))
		ORDER BY g.name`, staffID, role, eventID)
}

// AddGate creates a gate.
func AddGate(ctx context.Context, db dbx.DB, actor audit.Actor, eventID, name string) (string, error) {
	var id string
	err := dbx.InTx(ctx, db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO gate (event_id, name) VALUES ($1, $2) RETURNING id::text`,
			eventID, strings.TrimSpace(name)).Scan(&id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, actor, "gate.add", "gate", id, "", map[string]any{"name": name})
	})
	return id, err
}

// Assign lets a staff member scan at a gate.
func Assign(ctx context.Context, db dbx.DB, actor audit.Actor, gateID, staffID string) error {
	return dbx.InTx(ctx, db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO gate_assignment (gate_id, staff_id)
			SELECT g.id, s.id FROM gate g, staff s WHERE g.id::text = $1 AND s.id::text = $2
			ON CONFLICT (staff_id, gate_id) DO UPDATE SET gate_id = EXCLUDED.gate_id`, gateID, staffID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audit.Record(ctx, tx, actor, "gate.assign", "gate", gateID, "", map[string]any{"staff_id": staffID})
	})
}
