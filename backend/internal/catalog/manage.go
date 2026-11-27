package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"kacheem/backend/internal/audit"
	"kacheem/backend/internal/dbx"
)

// BelowCommittedError means a capacity change would drop below the places
// already held or sold; Min is the lowest allowed capacity.
type BelowCommittedError struct{ Min int }

func (e *BelowCommittedError) Error() string {
	return fmt.Sprintf("capacity cannot go below %d (places held or sold)", e.Min)
}

// ErrInvalid means event details failed validation.
var ErrInvalid = errors.New("catalog: invalid event details")

// SetCapacity changes the single tier's capacity (and the event's) and
// redistributes the free places evenly across its shards. It locks every
// shard in ascending order, the same order holds use, so it is serialized
// with reservations and payments.
func SetCapacity(ctx context.Context, db dbx.DB, actor audit.Actor, eventID string, capacity int) error {
	return dbx.InTx(ctx, db, func(tx pgx.Tx) error {
		shards, err := dbx.All[struct {
			TicketTypeID string `db:"ticket_type_id"`
			Shard        int16  `db:"shard"`
			Committed    int    `db:"committed"`
		}](ctx, tx, `SELECT s.ticket_type_id::text, s.shard, s.held + s.sold AS committed
			FROM inventory_shard s JOIN ticket_type t ON t.id = s.ticket_type_id
			WHERE t.event_id::text = $1 ORDER BY s.ticket_type_id, s.shard FOR UPDATE OF s`, eventID)
		if err != nil {
			return err
		}
		if len(shards) == 0 {
			return ErrNotFound
		}
		committed := 0
		for _, s := range shards {
			committed += s.Committed
		}
		if capacity < committed {
			return &BelowCommittedError{Min: committed}
		}
		free := capacity - committed
		n := len(shards)
		for i, s := range shards {
			share := free / n
			if i < free%n {
				share++
			}
			if _, err := tx.Exec(ctx, `UPDATE inventory_shard SET capacity = $3 WHERE ticket_type_id = $1 AND shard = $2`,
				s.TicketTypeID, s.Shard, s.Committed+share); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE ticket_type SET capacity = $2 WHERE id = $1`, shards[0].TicketTypeID, capacity); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE event SET capacity = $2 WHERE id::text = $1`, eventID, capacity); err != nil {
			return err
		}
		return audit.Record(ctx, tx, actor, "event.capacity", "event", eventID, "", map[string]any{"capacity": capacity})
	})
}

// Details are the event fields an organizer edits. Times are absolute; the
// time zone names how they are shown.
type Details struct {
	Title        string     `json:"title"`
	Organizer    string     `json:"organizer"`
	VenueName    string     `json:"venue_name"`
	VenueAddress string     `json:"venue_address"`
	Description  string     `json:"description"`
	Lineup       []string   `json:"lineup"`
	StartsAt     time.Time  `json:"starts_at"`
	DoorsAt      *time.Time `json:"doors_at"`
	TimeZone     string     `json:"time_zone"`
	SaleOpensAt  time.Time  `json:"sale_opens_at"`
	SaleClosesAt time.Time  `json:"sale_closes_at"`
	MaxPerOrder  int        `json:"max_per_order"`
	MinAge       int        `json:"min_age"`
	PriceMinor   int64      `json:"price_minor"`
	FeeMinor     int64      `json:"fee_minor"`
}

// Update saves edited details as the event's draft content. Buyers see them
// after the next publish; a new price applies to new quotes only, since
// orders keep the price they were quoted.
func Update(ctx context.Context, db dbx.DB, actor audit.Actor, eventID string, d Details) error {
	if _, err := time.LoadLocation(d.TimeZone); err != nil || strings.TrimSpace(d.Title) == "" {
		return ErrInvalid
	}
	if d.Lineup == nil {
		d.Lineup = []string{}
	}
	err := dbx.InTx(ctx, db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE event SET title = $2, organizer = $3, venue_name = $4, venue_address = $5,
				description = $6, lineup = $7, starts_at = $8, doors_at = $9, time_zone = $10,
				sale_opens_at = $11, sale_closes_at = $12, max_per_order = $13, min_age = $14
			WHERE id::text = $1`, eventID, strings.TrimSpace(d.Title), d.Organizer, d.VenueName, d.VenueAddress,
			d.Description, d.Lineup, d.StartsAt, d.DoorsAt, d.TimeZone, d.SaleOpensAt, d.SaleClosesAt, d.MaxPerOrder, d.MinAge)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if _, err := tx.Exec(ctx, `UPDATE ticket_type SET price_minor = $2, fee_minor = $3 WHERE event_id::text = $1`,
			eventID, d.PriceMinor, d.FeeMinor); err != nil {
			return err
		}
		return audit.Record(ctx, tx, actor, "event.update", "event", eventID, "", nil)
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23514" { // check_violation
		return ErrInvalid
	}
	return err
}
