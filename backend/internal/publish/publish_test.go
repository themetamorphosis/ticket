package publish

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"kacheem/backend/internal/audit"
	"kacheem/backend/internal/catalog"
	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/pgtest"
)

var ctx = context.Background()

var sys = audit.Actor{Kind: "system"}

func read(t *testing.T, path string) Snapshot {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPublishWritesVersionedSnapshotAndSwitchesCurrent(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live", Status: "draft", MaxPerOrder: 6})
	dir := t.TempDir()

	snap, err := Publish(ctx, pool, sys, "live", dir)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	cur := read(t, filepath.Join(dir, "event.json"))
	if cur.Revision != 1 || snap.Revision != 1 || cur.Event.Title != "Test Concert" || cur.Event.TimeZone != "Asia/Dhaka" ||
		cur.Event.MaxPerOrder != 6 || cur.Event.TicketType.PriceMinor != 150000 || cur.Event.Venue.Name != "Test Hall" ||
		len(cur.Event.Lineup) != 2 || cur.Event.DoorsAt == nil || cur.PublishedAt.IsZero() {
		t.Fatalf("snapshot = %+v", cur)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "event.json"))
	var generic map[string]map[string]any
	_ = json.Unmarshal(raw, &generic)
	for _, secret := range []string{"capacity", "id", "remaining", "status"} {
		if _, ok := generic["event"][secret]; ok {
			t.Errorf("snapshot leaks %q", secret)
		}
	}

	e, _ := catalog.GetEvent(ctx, pool, "live")
	if e.Status != "published" || e.PublishedRevision != 1 || e.PublishedAt == nil {
		t.Fatalf("event after publish = %+v", e)
	}

	// Republishing bumps the revision and keeps the earlier revision on disk.
	_, _ = pool.Exec(ctx, `UPDATE event SET title = 'Renamed Concert' WHERE slug = 'live'`)
	if _, err := Publish(ctx, pool, sys, "live", dir); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(dir, "event.json")).Event.Title != "Renamed Concert" ||
		read(t, filepath.Join(dir, "revisions", "event-1.json")).Event.Title != "Test Concert" ||
		read(t, filepath.Join(dir, "revisions", "event-2.json")).Revision != 2 {
		t.Fatal("revisions not preserved")
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(matches) > 0 {
		t.Fatalf("temporary files left: %v", matches)
	}
}

func TestPublishRejectsInvalidEvents(t *testing.T) {
	pool := pgtest.Pool(t)
	dir := t.TempDir()
	if _, err := Publish(ctx, pool, sys, "missing", dir); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "badzone", Status: "draft"})
	_, _ = pool.Exec(ctx, `UPDATE event SET time_zone = 'Mars/Olympus' WHERE slug = 'badzone'`)
	if _, err := Publish(ctx, pool, sys, "badzone", dir); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("bad zone: %v", err)
	}
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "twotiers", Status: "draft"})
	_, _ = pool.Exec(ctx, `INSERT INTO ticket_type (event_id, name, price_minor, fee_minor, capacity)
		SELECT id, 'VIP', 1, 0, 0 FROM event WHERE slug = 'twotiers'`)
	if _, err := Publish(ctx, pool, sys, "twotiers", dir); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("two tiers: %v", err)
	}
	if e, _ := catalog.GetEvent(ctx, pool, "badzone"); e.Status != "draft" || e.PublishedRevision != 0 {
		t.Fatalf("failed publish changed the event: %+v", e)
	}
	if _, err := os.Stat(filepath.Join(dir, "event.json")); !os.IsNotExist(err) {
		t.Fatal("failed publish wrote event.json")
	}
}

func TestPublishWithoutDoorsTime(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live"})
	_, _ = pool.Exec(ctx, `UPDATE event SET doors_at = NULL`)
	snap, err := Publish(ctx, pool, sys, "live", t.TempDir())
	if err != nil || snap.Event.DoorsAt != nil || snap.Revision != 2 {
		t.Fatalf("snap %+v err %v", snap, err)
	}
}

func TestFilesystemFailuresRollBackThePublish(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live", Status: "draft"})
	base := t.TempDir()

	notADir := filepath.Join(base, "file")
	_ = os.WriteFile(notADir, nil, 0o644)

	readOnly := filepath.Join(base, "ro")
	_ = os.MkdirAll(filepath.Join(readOnly, "revisions"), 0o755)
	_ = os.Chmod(filepath.Join(readOnly, "revisions"), 0o555)
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(readOnly, "revisions"), 0o755) })

	blocked := filepath.Join(base, "blocked")
	_ = os.MkdirAll(filepath.Join(blocked, "event.json", "occupied"), 0o755)

	for name, dir := range map[string]string{"mkdir": notADir, "write": readOnly, "rename": blocked} {
		if _, err := Publish(ctx, pool, sys, "live", dir); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if e, _ := catalog.GetEvent(ctx, pool, "live"); e.Status != "draft" || e.PublishedRevision != 0 {
		t.Fatalf("event changed despite failed writes: %+v", e)
	}
}

func TestPublishDatabaseFailures(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live", Status: "draft"})
	dir := t.TempDir()
	pgtest.EachFault(t, pool, func(db dbx.DB) error {
		_, err := Publish(ctx, db, sys, "live", dir)
		return err
	}, func(int, error) {})
	if e, _ := catalog.GetEvent(ctx, pool, "live"); e.PublishedRevision != 1 {
		t.Fatalf("revision %d after one successful publish", e.PublishedRevision)
	}
}
