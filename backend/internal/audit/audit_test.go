package audit

import (
	"context"
	"testing"

	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/pgtest"
)

func TestRecordAndRecent(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	var staffID string
	_ = pool.QueryRow(ctx, `INSERT INTO staff (email, name, role) VALUES ('o@example.org', 'Org One', 'organizer') RETURNING id`).Scan(&staffID)
	if err := Record(ctx, pool, Staff(staffID), "ticket.void", "ticket", "t1", "lost phone", map[string]any{"version": 2}); err != nil {
		t.Fatal(err)
	}
	if err := Record(ctx, pool, Actor{Kind: "system"}, "hold.expire", "order", "o1", "", nil); err != nil {
		t.Fatal(err)
	}
	got, err := Recent(ctx, pool, 10)
	if err != nil || len(got) != 2 || got[0].Actor != "system" || got[1].Actor != "Org One <o@example.org>" ||
		got[1].Reason != "lost phone" || got[1].Action != "ticket.void" || got[0].At == "" {
		t.Fatalf("%+v %v", got, err)
	}
	pgtest.EachFault(t, pool, func(db dbx.DB) error { _, err := Recent(ctx, db, 1); return err }, func(int, error) {})
}
