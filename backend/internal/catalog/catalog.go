// Package catalog reads events, their ticket tiers and live availability.
// Prices shown to buyers come from the published snapshot; payable totals
// and sale state always come from here, computed on the database clock.
package catalog

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"kacheem/backend/internal/dbx"
)

var (
	// ErrNotFound means no event with that slug (or no published one).
	ErrNotFound = errors.New("event not found")
	// ErrUnsupportedTiers means the event does not have exactly one tier,
	// which is all the launch scope supports.
	ErrUnsupportedTiers = errors.New("event must have exactly one ticket tier")
)

// Tier is one ticket type. Money is in integer minor units.
type Tier struct {
	ID         string `db:"id"`
	Name       string `db:"name"`
	PriceMinor int64  `db:"price_minor"`
	FeeMinor   int64  `db:"fee_minor"`
	Capacity   int    `db:"capacity"`
}

// Event is the full event record with its tiers.
type Event struct {
	ID                string     `db:"id"`
	Slug              string     `db:"slug"`
	Title             string     `db:"title"`
	Organizer         string     `db:"organizer"`
	VenueName         string     `db:"venue_name"`
	VenueAddress      string     `db:"venue_address"`
	Description       string     `db:"description"`
	Lineup            []string   `db:"lineup"`
	StartsAt          time.Time  `db:"starts_at"`
	DoorsAt           *time.Time `db:"doors_at"`
	TimeZone          string     `db:"time_zone"`
	Currency          string     `db:"currency"`
	Capacity          int        `db:"capacity"`
	MaxPerOrder       int        `db:"max_per_order"`
	MinAge            int        `db:"min_age"`
	HoldMinutes       int        `db:"hold_minutes"`
	SaleOpensAt       time.Time  `db:"sale_opens_at"`
	SaleClosesAt      time.Time  `db:"sale_closes_at"`
	Status            string     `db:"status"`
	PublishedRevision int        `db:"published_revision"`
	PublishedAt       *time.Time `db:"published_at"`
	Tiers             []Tier     `db:"-"`
}

const eventColumns = `id::text, slug, title, organizer, venue_name, venue_address, description, lineup,
	starts_at, doors_at, time_zone, currency, capacity, max_per_order, min_age, hold_minutes,
	sale_opens_at, sale_closes_at, status, published_revision, published_at`

// GetEvent returns the event with any status.
func GetEvent(ctx context.Context, db dbx.DB, slug string) (Event, error) {
	return getEvent(ctx, db, `SELECT `+eventColumns+` FROM event WHERE slug = $1`, slug)
}

// GetPublishedEvent returns the event only if it is published.
func GetPublishedEvent(ctx context.Context, db dbx.DB, slug string) (Event, error) {
	return getEvent(ctx, db, `SELECT `+eventColumns+` FROM event WHERE slug = $1 AND status = 'published'`, slug)
}

func getEvent(ctx context.Context, db dbx.DB, sql, slug string) (Event, error) {
	e, err := dbx.One[Event](ctx, db, sql, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	if err != nil {
		return Event{}, err
	}
	e.Tiers, err = dbx.All[Tier](ctx, db, `
		SELECT id::text, name, price_minor, fee_minor, capacity
		FROM ticket_type WHERE event_id = $1 ORDER BY sort_order, name`, e.ID)
	if err != nil {
		return Event{}, err
	}
	return e, nil
}

// Tier returns the event's single launch tier.
func (e Event) Tier() (Tier, error) {
	if len(e.Tiers) != 1 {
		return Tier{}, ErrUnsupportedTiers
	}
	return e.Tiers[0], nil
}

// Sale states reported to buyers.
const (
	StateNotOpen  = "not_open" // before sale_opens_at
	StateOnSale   = "on_sale"  // places are free
	StateReserved = "reserved" // none free, but holds may still be released
	StateSoldOut  = "sold_out" // every place is sold
	StateClosed   = "closed"   // after sale_closes_at
)

// Availability is the live sale state of a published event.
type Availability struct {
	State      string
	Remaining  int // free places: capacity - held - sold, across all shards
	ServerTime time.Time
	OpensAt    time.Time
	ClosesAt   time.Time
}

// State derives the sale state. Sold out is shown only when every shard is
// empty and no holds are outstanding.
func State(now, opens, closes time.Time, free, held int) string {
	switch {
	case now.Before(opens):
		return StateNotOpen
	case !now.Before(closes):
		return StateClosed
	case free > 0:
		return StateOnSale
	case held > 0:
		return StateReserved
	default:
		return StateSoldOut
	}
}

type availabilityRow struct {
	Now    time.Time `db:"now"`
	Opens  time.Time `db:"sale_opens_at"`
	Closes time.Time `db:"sale_closes_at"`
	Free   int       `db:"free"`
	Held   int       `db:"held"`
}

// GetAvailability reads the live state of a published event.
func GetAvailability(ctx context.Context, db dbx.DB, slug string) (Availability, error) {
	r, err := dbx.One[availabilityRow](ctx, db, `
		SELECT now() AS now, e.sale_opens_at, e.sale_closes_at,
			coalesce(sum(s.capacity - s.held - s.sold), 0)::int AS free,
			coalesce(sum(s.held), 0)::int AS held
		FROM event e
		LEFT JOIN ticket_type t ON t.event_id = e.id
		LEFT JOIN inventory_shard s ON s.ticket_type_id = t.id
		WHERE e.slug = $1 AND e.status = 'published'
		GROUP BY e.id`, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return Availability{}, ErrNotFound
	}
	if err != nil {
		return Availability{}, err
	}
	return Availability{
		State:      State(r.Now, r.Opens, r.Closes, r.Free, r.Held),
		Remaining:  r.Free,
		ServerTime: r.Now,
		OpensAt:    r.Opens,
		ClosesAt:   r.Closes,
	}, nil
}

// Quote is a server-computed price for a quantity of one tier.
type Quote struct {
	Quantity       int
	UnitPriceMinor int64
	UnitFeeMinor   int64
	SubtotalMinor  int64
	FeesMinor      int64
	TotalMinor     int64
	Currency       string
}

// NewQuote prices qty places of t.
func NewQuote(t Tier, currency string, qty int) Quote {
	n := int64(qty)
	return Quote{
		Quantity:       qty,
		UnitPriceMinor: t.PriceMinor,
		UnitFeeMinor:   t.FeeMinor,
		SubtotalMinor:  n * t.PriceMinor,
		FeesMinor:      n * t.FeeMinor,
		TotalMinor:     n * (t.PriceMinor + t.FeeMinor),
		Currency:       currency,
	}
}
