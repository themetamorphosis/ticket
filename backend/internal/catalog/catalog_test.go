package catalog_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"kacheem/backend/internal/catalog"
	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/pgtest"
)

func TestGetEventReturnsEventWithTier(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	ids := pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live", Shards: []int32{6, 4}, MaxPerOrder: 4})
	e, err := catalog.GetPublishedEvent(ctx, pool, "live")
	if err != nil {
		t.Fatalf("GetPublishedEvent: %v", err)
	}
	if e.ID != ids.EventID || e.Title != "Test Concert" || e.TimeZone != "Asia/Dhaka" || e.Currency != "BDT" ||
		e.MaxPerOrder != 4 || e.Capacity != 10 || len(e.Lineup) != 2 || e.DoorsAt == nil || e.PublishedRevision != 1 {
		t.Fatalf("event = %+v", e)
	}
	tier, err := e.Tier()
	if err != nil || tier.ID != ids.TicketTypeID || tier.Capacity != 10 || tier.PriceMinor != 150000 || tier.FeeMinor != 5000 {
		t.Fatalf("tier = %+v, err %v", tier, err)
	}
}

func TestDraftEventsAreHiddenFromBuyers(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "draft", Status: "draft"})
	if _, err := catalog.GetPublishedEvent(ctx, pool, "draft"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("published lookup err = %v", err)
	}
	if _, err := catalog.GetAvailability(ctx, pool, "draft"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("availability err = %v", err)
	}
	e, err := catalog.GetEvent(ctx, pool, "draft")
	if err != nil || e.Status != "draft" {
		t.Fatalf("GetEvent = %+v, %v", e, err)
	}
	if _, err := catalog.GetEvent(ctx, pool, "missing"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("missing err = %v", err)
	}
}

func TestGetEventErrorPaths(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live"})
	for _, op := range []func(dbx.DB) error{
		func(db dbx.DB) error { _, err := catalog.GetEvent(ctx, db, "live"); return err },
		func(db dbx.DB) error { _, err := catalog.GetAvailability(ctx, db, "live"); return err },
	} {
		pgtest.EachFault(t, pool, op, func(int, error) {})
	}
}

func TestTierRequiresExactlyOne(t *testing.T) {
	for _, tiers := range [][]catalog.Tier{nil, {{}, {}}} {
		if _, err := (catalog.Event{Tiers: tiers}).Tier(); !errors.Is(err, catalog.ErrUnsupportedTiers) {
			t.Errorf("%d tiers: err = %v", len(tiers), err)
		}
	}
}

func TestAvailabilityTracksHeldAndSold(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	ids := pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live", Shards: []int32{2, 2}})
	check := func(wantState string, wantRemaining int) {
		t.Helper()
		a, err := catalog.GetAvailability(ctx, pool, "live")
		if err != nil || a.State != wantState || a.Remaining != wantRemaining || a.ServerTime.IsZero() {
			t.Fatalf("availability = %+v, err %v; want %s/%d", a, err, wantState, wantRemaining)
		}
	}
	set := func(held, sold int) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE inventory_shard SET held = $2, sold = $3 WHERE ticket_type_id = $1`,
			ids.TicketTypeID, held, sold); err != nil {
			t.Fatal(err)
		}
	}
	check(catalog.StateOnSale, 4)
	set(1, 1)
	check(catalog.StateReserved, 0)
	set(0, 2)
	check(catalog.StateSoldOut, 0)
}

func TestState(t *testing.T) {
	now := time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)
	opens, closes := now.Add(-time.Hour), now.Add(time.Hour)
	cases := []struct {
		now        time.Time
		free, held int
		want       string
	}{
		{now.Add(-2 * time.Hour), 5, 0, catalog.StateNotOpen},
		{closes, 5, 0, catalog.StateClosed},
		{now, 5, 0, catalog.StateOnSale},
		{now, 0, 3, catalog.StateReserved},
		{now, 0, 0, catalog.StateSoldOut},
		{opens, 1, 0, catalog.StateOnSale},
	}
	for _, c := range cases {
		if got := catalog.State(c.now, opens, closes, c.free, c.held); got != c.want {
			t.Errorf("State(%v, free %d, held %d) = %s, want %s", c.now, c.free, c.held, got, c.want)
		}
	}
}

func TestNewQuote(t *testing.T) {
	q := catalog.NewQuote(catalog.Tier{PriceMinor: 150000, FeeMinor: 5000}, "BDT", 3)
	want := catalog.Quote{Quantity: 3, UnitPriceMinor: 150000, UnitFeeMinor: 5000,
		SubtotalMinor: 450000, FeesMinor: 15000, TotalMinor: 465000, Currency: "BDT"}
	if q != want {
		t.Fatalf("quote = %+v", q)
	}
}
