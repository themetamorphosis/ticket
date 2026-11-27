package catalog_test

import (
	"errors"
	"testing"
	"time"

	"kacheem/backend/internal/audit"
	"kacheem/backend/internal/catalog"
	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/pgtest"
)

var sys = audit.Actor{Kind: "system"}

func TestSetCapacityRedistributesAndRespectsCommitted(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Pool(t)
	ids := pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live", Shards: []int32{5, 5, 5}})
	_, _ = pool.Exec(ctx, `UPDATE inventory_shard SET held = 2, sold = 1 WHERE shard = 0`)
	_, _ = pool.Exec(ctx, `UPDATE inventory_shard SET sold = 4 WHERE shard = 1`)

	err := catalog.SetCapacity(ctx, pool, sys, ids.EventID, 6)
	var below *catalog.BelowCommittedError
	if !errors.As(err, &below) || below.Min != 7 || below.Error() != "capacity cannot go below 7 (places held or sold)" {
		t.Fatalf("below committed: %v", err)
	}
	if err := catalog.SetCapacity(ctx, pool, sys, ids.EventID, 12); err != nil {
		t.Fatal(err)
	}
	rows, _ := pool.Query(ctx, `SELECT capacity - held - sold FROM inventory_shard ORDER BY shard`)
	var free []int
	for rows.Next() {
		var f int
		_ = rows.Scan(&f)
		free = append(free, f)
	}
	a, err := catalog.GetAvailability(ctx, pool, "live")
	if len(free) != 3 || free[0] != 2 || free[1] != 2 || free[2] != 1 || err != nil || a.Remaining != 5 {
		t.Fatalf("free per shard %v, remaining %d", free, a.Remaining)
	}
	e, _ := catalog.GetEvent(ctx, pool, "live")
	if e.Capacity != 12 || e.Tiers[0].Capacity != 12 {
		t.Fatalf("event %d tier %d", e.Capacity, e.Tiers[0].Capacity)
	}
	if err := catalog.SetCapacity(ctx, pool, sys, "00000000-0000-0000-0000-000000000000", 1); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	pgtest.EachFault(t, pool, func(db dbx.DB) error { return catalog.SetCapacity(ctx, db, sys, ids.EventID, 20) }, func(int, error) {})
}

func details() catalog.Details {
	start := time.Date(2027, 2, 14, 13, 30, 0, 0, time.UTC)
	doors := start.Add(-time.Hour)
	return catalog.Details{Title: "Renamed", Organizer: "Kacheem Comms", VenueName: "Hall", VenueAddress: "Dhaka",
		Description: "D", StartsAt: start, DoorsAt: &doors, TimeZone: "Asia/Dhaka",
		SaleOpensAt: start.Add(-30 * 24 * time.Hour), SaleClosesAt: start.Add(-2 * time.Hour),
		MaxPerOrder: 6, MinAge: 18, PriceMinor: 200000, FeeMinor: 6000}
}

func TestUpdateDetails(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Pool(t)
	ids := pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live"})
	if err := catalog.Update(ctx, pool, sys, ids.EventID, details()); err != nil {
		t.Fatal(err)
	}
	e, _ := catalog.GetEvent(ctx, pool, "live")
	if e.Title != "Renamed" || e.MaxPerOrder != 6 || e.MinAge != 18 || e.Tiers[0].PriceMinor != 200000 || len(e.Lineup) != 0 {
		t.Fatalf("event %+v", e)
	}
	bad := []func(*catalog.Details){
		func(d *catalog.Details) { d.TimeZone = "Mars/Olympus" },
		func(d *catalog.Details) { d.Title = " " },
		func(d *catalog.Details) { d.MaxPerOrder = 99 },
		func(d *catalog.Details) { d.SaleClosesAt = d.SaleOpensAt },
	}
	for i, mutate := range bad {
		d := details()
		mutate(&d)
		if err := catalog.Update(ctx, pool, sys, ids.EventID, d); !errors.Is(err, catalog.ErrInvalid) {
			t.Errorf("case %d: %v", i, err)
		}
	}
	if err := catalog.Update(ctx, pool, sys, "00000000-0000-0000-0000-000000000000", details()); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	pgtest.EachFault(t, pool, func(db dbx.DB) error { return catalog.Update(ctx, db, sys, ids.EventID, details()) }, func(int, error) {})
}
