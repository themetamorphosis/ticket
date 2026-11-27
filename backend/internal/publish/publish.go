// Package publish writes the approved public event snapshot that the static
// buyer site renders (plan section 2, "Frontend publishing"). Content edits
// never rebuild the frontend: publishing writes revisions/event-<n>.json and
// then atomically renames event.json into place, which NGINX serves with a
// short cache lifetime. Capacity, sale state and totals stay live in the API.
package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"

	"kacheem/backend/internal/catalog"
	"kacheem/backend/internal/dbx"
)

// ErrInvalidEvent means the event cannot be published as configured.
var ErrInvalidEvent = errors.New("event cannot be published")

// Snapshot is the public, cacheable description of an event.
type Snapshot struct {
	Revision    int       `json:"revision"`
	PublishedAt time.Time `json:"published_at"`
	Event       Event     `json:"event"`
}

// Event is the public part of the event record. It never includes capacity,
// remaining stock or anything else that changes during the sale.
type Event struct {
	Slug         string     `json:"slug"`
	Title        string     `json:"title"`
	Organizer    string     `json:"organizer"`
	Venue        Venue      `json:"venue"`
	Description  string     `json:"description"`
	Lineup       []string   `json:"lineup"`
	StartsAt     time.Time  `json:"starts_at"`
	DoorsAt      *time.Time `json:"doors_at"`
	TimeZone     string     `json:"time_zone"`
	Currency     string     `json:"currency"`
	MaxPerOrder  int        `json:"max_per_order"`
	MinAge       int        `json:"min_age"`
	SaleOpensAt  time.Time  `json:"sale_opens_at"`
	SaleClosesAt time.Time  `json:"sale_closes_at"`
	TicketType   TicketType `json:"ticket_type"`
}

type Venue struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type TicketType struct {
	Name       string `json:"name"`
	PriceMinor int64  `json:"price_minor"`
	FeeMinor   int64  `json:"fee_minor"`
}

// Publish marks the event published, bumps its revision and writes the
// snapshot into dir, holding the event row locked throughout. The database
// change commits only after the files are written, so the recorded revision is
// always being served. If that final commit fails, event.json may already show
// the new content under the next revision number; the publish reports an
// error and publishing again overwrites it consistently.
func Publish(ctx context.Context, db dbx.DB, slug, dir string) (Snapshot, error) {
	var snap Snapshot
	err := dbx.InTx(ctx, db, func(tx pgx.Tx) error {
		var rev int
		err := tx.QueryRow(ctx, `SELECT published_revision FROM event WHERE slug = $1 FOR UPDATE`, slug).Scan(&rev)
		if errors.Is(err, pgx.ErrNoRows) {
			return catalog.ErrNotFound
		}
		if err != nil {
			return err
		}
		e, err := catalog.GetEvent(ctx, tx, slug)
		if err != nil {
			return err
		}
		tier, err := e.Tier()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidEvent, err)
		}
		if _, err := time.LoadLocation(e.TimeZone); err != nil {
			return fmt.Errorf("%w: unknown time zone %q", ErrInvalidEvent, e.TimeZone)
		}
		snap.Revision = rev + 1
		if err := tx.QueryRow(ctx, `
			UPDATE event SET status = 'published', published_revision = $2, published_at = now()
			WHERE id = $1 RETURNING published_at`, e.ID, snap.Revision).Scan(&snap.PublishedAt); err != nil {
			return err
		}
		snap.PublishedAt = snap.PublishedAt.UTC()
		snap.Event = Event{
			Slug: e.Slug, Title: e.Title, Organizer: e.Organizer,
			Venue:       Venue{Name: e.VenueName, Address: e.VenueAddress},
			Description: e.Description, Lineup: e.Lineup,
			StartsAt: e.StartsAt.UTC(), DoorsAt: utc(e.DoorsAt), TimeZone: e.TimeZone, Currency: e.Currency,
			MaxPerOrder: e.MaxPerOrder, MinAge: e.MinAge,
			SaleOpensAt: e.SaleOpensAt.UTC(), SaleClosesAt: e.SaleClosesAt.UTC(),
			TicketType: TicketType{Name: tier.Name, PriceMinor: tier.PriceMinor, FeeMinor: tier.FeeMinor},
		}
		return write(dir, snap)
	})
	if err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func write(dir string, snap Snapshot) error {
	data, _ := json.MarshalIndent(snap, "", "  ")
	data = append(data, '\n')
	revisions := filepath.Join(dir, "revisions")
	if err := os.MkdirAll(revisions, 0o755); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(revisions, fmt.Sprintf("event-%d.json", snap.Revision)), data); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, "event.json"), data)
}

// writeAtomic writes a sibling temporary file and renames it over path, so
// readers see either the old file or the new one, never a partial write.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
