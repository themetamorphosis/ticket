package pgtest

import (
	"context"
	"database/sql"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"kacheem/backend/internal/postgres"
	"kacheem/backend/migrations"
)

// Pool returns a pool on a fresh, fully migrated database, closed at cleanup.
func Pool(t TB) *pgxpool.Pool {
	t.Helper()
	url := NewDatabase(t)
	ctx := context.Background()
	cfg, _ := pgx.ParseConfig(url)
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	if err := migrateUp(ctx, db); err != nil {
		t.Fatalf("pgtest: migrate: %v", err)
		return nil
	}
	pool, err := openPool(ctx, url)
	if err != nil {
		t.Fatalf("pgtest: open pool: %v", err)
		return nil
	}
	t.Cleanup(pool.Close)
	return pool
}

// openPool and migrateUp are seams so tests can exercise failure branches.
var openPool = postgres.Open

var migrateUp = func(ctx context.Context, db *sql.DB) error {
	p, _ := migrations.NewProvider(db, migrations.Files)
	_, err := p.Up(ctx)
	return err
}

// Event describes a fixture event with one ticket tier split into shards.
type Event struct {
	Slug        string
	Status      string        // "published" (default) or "draft"
	OpensIn     time.Duration // relative to the database clock; default -1h (open)
	ClosesIn    time.Duration // default +24h
	MaxPerOrder int           // default 10
	MinAge      int
	HoldMinutes int     // default 10
	PriceMinor  int64   // default 150000 (1,500.00)
	FeeMinor    int64   // default 5000 (50.00)
	Shards      []int32 // shard capacities; default eight shards of 10
}

// EventIDs are the identifiers CreateEvent assigns.
type EventIDs struct {
	EventID, TicketTypeID string
}

// CreateEvent inserts e with its tier and shards in one statement.
func CreateEvent(t TB, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, e Event) EventIDs {
	t.Helper()
	if e.Status == "" {
		e.Status = "published"
	}
	if e.OpensIn == 0 {
		e.OpensIn = -time.Hour
	}
	if e.ClosesIn == 0 {
		e.ClosesIn = 24 * time.Hour
	}
	if e.MaxPerOrder == 0 {
		e.MaxPerOrder = 10
	}
	if e.HoldMinutes == 0 {
		e.HoldMinutes = 10
	}
	if e.PriceMinor == 0 {
		e.PriceMinor = 150000
	}
	if e.FeeMinor == 0 {
		e.FeeMinor = 5000
	}
	if e.Shards == nil {
		e.Shards = []int32{10, 10, 10, 10, 10, 10, 10, 10}
	}
	var total int32
	for _, c := range e.Shards {
		total += c
	}
	var ids EventIDs
	err := db.QueryRow(context.Background(), `
		WITH ev AS (
			INSERT INTO event (slug, title, organizer, venue_name, venue_address, description, lineup,
				starts_at, doors_at, time_zone, currency, capacity, max_per_order, min_age, hold_minutes,
				sale_opens_at, sale_closes_at, status, published_revision, published_at)
			VALUES ($1, 'Test Concert', 'Kacheem Comms', 'Test Hall', '1 Test Road, Dhaka', 'A test event.',
				ARRAY['Artist One', 'Artist Two'], now() + interval '30 days', now() + interval '30 days' - interval '1 hour',
				'Asia/Dhaka', 'BDT', $2, $3, $4, $5,
				now() + make_interval(secs => $6), now() + make_interval(secs => $7), $8,
				CASE WHEN $8 = 'published' THEN 1 ELSE 0 END, CASE WHEN $8 = 'published' THEN now() END)
			RETURNING id
		), tt AS (
			INSERT INTO ticket_type (event_id, name, price_minor, fee_minor, capacity)
			SELECT id, 'General admission', $9, $10, $2 FROM ev
			RETURNING id
		), sh AS (
			INSERT INTO inventory_shard (ticket_type_id, shard, capacity)
			SELECT tt.id, s.ord - 1, s.cap FROM tt, unnest($11::int[]) WITH ORDINALITY AS s(cap, ord)
		)
		SELECT ev.id::text, tt.id::text FROM ev, tt`,
		e.Slug, total, e.MaxPerOrder, e.MinAge, e.HoldMinutes,
		e.OpensIn.Seconds(), e.ClosesIn.Seconds(), e.Status,
		e.PriceMinor, e.FeeMinor, e.Shards,
	).Scan(&ids.EventID, &ids.TicketTypeID)
	if err != nil {
		t.Fatalf("pgtest: create event %q: %v", e.Slug, err)
	}
	return ids
}
