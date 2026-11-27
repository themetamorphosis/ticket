// Package admissions decides entry at the gate (plan section 6). Each ticket
// admits once: the decision locks the ticket row, so two gates scanning the
// same ticket at the same moment, or a scan racing a refund or reissue, are
// serialized and exactly one admission can succeed. A device's retry of the
// same request returns the recorded outcome instead of deciding again.
package admissions

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/tickets"
)

// Outcomes shown to gate staff.
const (
	Admitted    = "admitted"     // let them in
	AlreadyUsed = "already_used" // admitted before: FirstGate and FirstAt say where and when
	Unknown     = "unknown"      // no such ticket
	WrongEvent  = "wrong_event"  // a ticket for another event
	Void        = "void"         // voided, reissued or refund pending
	NotOpen     = "not_open"     // before admission opens
	Closed      = "closed"       // after admission closes
)

var (
	// ErrBadScan means the scanned text or typed code is malformed (a typo,
	// or a QR code that is not one of our tickets' staff URLs). Nothing is recorded.
	ErrBadScan = errors.New("admissions: not a ticket code")
	// ErrGate means the gate does not exist.
	ErrGate = errors.New("admissions: unknown gate")
)

// Scan is one admission request from a gate device.
type Scan struct {
	DeviceID  string
	RequestID string
	GateID    string
	StaffID   string
	// Exactly one of QR (the scanned text) or Code (typed fallback code).
	QR   string
	Code string
}

// Result is the decision for one scan.
type Result struct {
	Outcome string
	// Replay is true when this answers a retry of an earlier request.
	Replay      bool
	TicketLabel string
	OrderRef    string
	FirstGate   string
	FirstAt     time.Time
	DecidedAt   time.Time
}

var credentialPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// digest turns the scan into the ticket lookup key, accepting only our own
// staff scanner URL (gateOrigin + "/scan#" + credential) or a valid fallback code.
func digest(gateOrigin string, s Scan) (column string, key []byte, method string, err error) {
	if s.Code != "" {
		norm, ok := tickets.NormalizeFallback(s.Code)
		if !ok {
			return "", nil, "", ErrBadScan
		}
		return "fallback_digest", tickets.Digest(norm), "code", nil
	}
	cred, ok := strings.CutPrefix(strings.TrimSpace(s.QR), gateOrigin+"/scan#")
	if !ok || !credentialPattern.MatchString(cred) {
		return "", nil, "", ErrBadScan
	}
	return "credential_digest", tickets.Digest(cred), "qr", nil
}

type ticketRow struct {
	ID        string     `db:"id"`
	Status    string     `db:"status"`
	EventID   string     `db:"event_id"`
	OrderRef  string     `db:"public_ref"`
	Label     string     `db:"label"`
	Opens     time.Time  `db:"opens"`
	Closes    time.Time  `db:"closes"`
	Now       time.Time  `db:"now"`
	FirstGate *string    `db:"first_gate"`
	FirstAt   *time.Time `db:"first_at"`
}

const ticketColumns = `t.id::text, t.status, o.event_id::text, o.public_ref,
	tt.name || ' · ticket ' || t.ordinal || ' of ' || (SELECT count(*) FROM ticket x WHERE x.order_id = t.order_id) AS label,
	coalesce(e.admission_opens_at, e.starts_at - interval '3 hours') AS opens,
	coalesce(e.admission_closes_at, e.starts_at + interval '12 hours') AS closes,
	now() AS now, g.name AS first_gate, a.admitted_at AS first_at
	FROM ticket t
	JOIN ticket_order o ON o.id = t.order_id
	JOIN event e ON e.id = o.event_id
	JOIN ticket_type tt ON tt.id = t.ticket_type_id
	LEFT JOIN admission a ON a.ticket_id = t.id
	LEFT JOIN gate g ON g.id = a.gate_id`

// Admit decides one scan. gateOrigin is the staff scanner origin printed in
// every QR code.
func Admit(ctx context.Context, db dbx.DB, gateOrigin string, s Scan) (Result, error) {
	column, key, method, err := digest(gateOrigin, s)
	if err != nil {
		return Result{}, err
	}
	var res Result
	err = dbx.InTx(ctx, db, func(tx pgx.Tx) error {
		var replayed bool
		res, replayed, err = replay(ctx, tx, s)
		if err != nil || replayed {
			return err
		}
		var gateEvent string
		if err := tx.QueryRow(ctx, `SELECT event_id::text FROM gate WHERE id = $1`, s.GateID).Scan(&gateEvent); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrGate
			}
			return err
		}
		// The row lock serializes every decision about this ticket. The
		// details are read by a second statement: after waiting for the lock,
		// READ COMMITTED re-checks only the locked row, so a join in the same
		// statement would miss an admission committed meanwhile.
		var id string
		err := tx.QueryRow(ctx, `SELECT id::text FROM ticket WHERE `+column+` = $1 FOR UPDATE`, key).Scan(&id)
		var ticketID *string
		var t ticketRow
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			res = Result{Outcome: Unknown}
		case err != nil:
			return err
		default:
			if t, err = dbx.One[ticketRow](ctx, tx, `SELECT `+ticketColumns+` WHERE t.id = $1`, id); err != nil {
				return err
			}
			ticketID = &t.ID
			res = decide(t, gateEvent)
		}
		if res.Outcome == Admitted {
			if _, err := tx.Exec(ctx, `INSERT INTO admission (ticket_id, gate_id, staff_id, device_id, request_id)
				VALUES ($1, $2, $3, $4, $5)`, t.ID, s.GateID, s.StaffID, s.DeviceID, s.RequestID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE ticket SET status = 'used' WHERE id = $1`, t.ID); err != nil {
				return err
			}
		}
		return tx.QueryRow(ctx, `INSERT INTO scan_attempt (device_id, request_id, gate_id, staff_id, ticket_id, method, outcome)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING created_at`,
			s.DeviceID, s.RequestID, s.GateID, s.StaffID, ticketID, method, res.Outcome).Scan(&res.DecidedAt)
	})
	if dbx.IsUniqueViolation(err, "scan_attempt_device_id_request_id_key") {
		// The same request arrived twice at once; the first one decided.
		return Admit(ctx, db, gateOrigin, s)
	}
	return res, err
}

func decide(t ticketRow, gateEvent string) Result {
	res := Result{TicketLabel: t.Label, OrderRef: t.OrderRef}
	switch {
	case t.EventID != gateEvent:
		res.Outcome = WrongEvent
	case t.Status == "used":
		res.Outcome, res.FirstGate, res.FirstAt = AlreadyUsed, *t.FirstGate, *t.FirstAt
	case t.Status != "valid":
		res.Outcome = Void
	case t.Now.Before(t.Opens):
		res.Outcome = NotOpen
	case !t.Now.Before(t.Closes):
		res.Outcome = Closed
	default:
		res.Outcome = Admitted
	}
	return res
}

// replay returns the recorded outcome of an earlier identical request.
func replay(ctx context.Context, tx pgx.Tx, s Scan) (Result, bool, error) {
	prev, err := dbx.One[struct {
		Outcome  string    `db:"outcome"`
		TicketID *string   `db:"ticket_id"`
		At       time.Time `db:"created_at"`
	}](ctx, tx, `SELECT outcome, ticket_id::text, created_at FROM scan_attempt WHERE device_id = $1 AND request_id = $2`,
		s.DeviceID, s.RequestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, err
	}
	res := Result{Outcome: prev.Outcome, Replay: true, DecidedAt: prev.At}
	if prev.TicketID != nil {
		t, err := dbx.One[ticketRow](ctx, tx, `SELECT `+ticketColumns+` WHERE t.id = $1`, *prev.TicketID)
		if err != nil {
			return Result{}, false, err
		}
		res.TicketLabel, res.OrderRef = t.Label, t.OrderRef
		if t.FirstAt != nil {
			res.FirstGate, res.FirstAt = *t.FirstGate, *t.FirstAt
		}
	}
	return res, true, nil
}

// Counts are live gate totals for the dashboard.
type Counts struct {
	Gate     string `db:"gate"`
	Admitted int    `db:"admitted"`
	Refused  int    `db:"refused"`
}

// GateCounts returns admissions and refusals per gate for an event.
func GateCounts(ctx context.Context, db dbx.DB, eventID string) ([]Counts, error) {
	return dbx.All[Counts](ctx, db, `
		SELECT g.name AS gate,
			count(*) FILTER (WHERE s.outcome = 'admitted')::int AS admitted,
			count(*) FILTER (WHERE s.outcome <> 'admitted')::int AS refused
		FROM gate g LEFT JOIN scan_attempt s ON s.gate_id = g.id
		WHERE g.event_id = $1 GROUP BY g.name ORDER BY g.name`, eventID)
}
