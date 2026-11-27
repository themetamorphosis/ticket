package orders

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kacheem/backend/internal/catalog"
	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/pgtest"
)

var ctx = context.Background()

func request(buyer, key string, qty int) Request {
	return Request{
		EventSlug:      "live",
		BuyerID:        buyer,
		IdempotencyKey: key,
		Input:          Input{Quantity: qty, Name: "Nusrat Jahan", Email: "nusrat@example.com", Phone: "+8801711000000", Age: 28},
	}
}

// assertInvariants checks every shard's held count equals the quantity held
// by orders that are still 'held', and that held + sold never exceeds capacity.
func assertInvariants(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var bad int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FROM inventory_shard s
		WHERE s.held <> coalesce((
			SELECT sum(h.quantity) FROM hold h JOIN ticket_order o ON o.id = h.order_id
			WHERE o.status = 'held' AND h.ticket_type_id = s.ticket_type_id AND h.shard = s.shard), 0)
		OR s.held + s.sold > s.capacity`).Scan(&bad)
	if err != nil || bad != 0 {
		t.Fatalf("inventory invariant broken on %d shards (err %v)", bad, err)
	}
}

func held(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT coalesce(sum(held), 0) FROM inventory_shard`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func expire(t *testing.T, pool *pgxpool.Pool, ref string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `UPDATE ticket_order SET expires_at = now() - interval '1 second' WHERE public_ref = $1`, ref); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T, e pgtest.Event) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.Pool(t)
	e.Slug = "live"
	pgtest.CreateEvent(t, pool, e)
	return pool
}

func TestReserveHoldsPlacesWithServerQuote(t *testing.T) {
	pool := setup(t, pgtest.Event{})
	o, created, err := Reserve(ctx, pool, request("buyer-1", "key-00000001", 3))
	if err != nil || !created {
		t.Fatalf("Reserve: created %v err %v", created, err)
	}
	if o.Status != "held" || o.Quantity != 3 || o.UnitPriceMinor != 150000 || o.UnitFeeMinor != 5000 ||
		o.TotalMinor != 465000 || o.Currency != "BDT" || o.EventSlug != "live" || o.EventTimeZone != "Asia/Dhaka" ||
		o.Name != "Nusrat Jahan" || o.Phone != "+8801711000000" || o.Age != 28 {
		t.Fatalf("order = %+v", o)
	}
	if hold := o.ExpiresAt.Sub(o.CreatedAt); hold < 10*time.Minute-time.Second || hold > 10*time.Minute+time.Second {
		t.Fatalf("hold length %v, want 10m", hold)
	}
	if held(t, pool) != 3 {
		t.Fatalf("held = %d", held(t, pool))
	}
	got, err := Get(ctx, pool, "buyer-1", o.Ref)
	if err != nil || got.ID != o.ID {
		t.Fatalf("Get: %+v %v", got, err)
	}
	if _, err := Get(ctx, pool, "someone-else", o.Ref); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("other buyer Get err = %v", err)
	}
	assertInvariants(t, pool)
}

func TestReserveReplaysSameKeyAndRejectsChangedContents(t *testing.T) {
	pool := setup(t, pgtest.Event{})
	first, _, err := Reserve(ctx, pool, request("buyer-1", "key-00000001", 2))
	if err != nil {
		t.Fatal(err)
	}
	again, created, err := Reserve(ctx, pool, request("buyer-1", "key-00000001", 2))
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("replay: created %v id %s err %v", created, again.ID, err)
	}
	if _, _, err := Reserve(ctx, pool, request("buyer-1", "key-00000001", 3)); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed quantity err = %v", err)
	}
	if held(t, pool) != 2 {
		t.Fatalf("held = %d after replays", held(t, pool))
	}
}

func TestReserveAllowsOneActiveHoldPerBuyer(t *testing.T) {
	pool := setup(t, pgtest.Event{})
	first, _, _ := Reserve(ctx, pool, request("buyer-1", "key-00000001", 1))
	_, _, err := Reserve(ctx, pool, request("buyer-1", "key-00000002", 1))
	var active *ActiveHoldError
	if !errors.As(err, &active) || active.Ref != first.Ref || err.Error() == "" {
		t.Fatalf("err = %v", err)
	}
	// Once the first hold has expired the buyer can book again at once.
	expire(t, pool, first.Ref)
	second, created, err := Reserve(ctx, pool, request("buyer-1", "key-00000002", 1))
	if err != nil || !created {
		t.Fatalf("after expiry: %v", err)
	}
	old, _ := Get(ctx, pool, "buyer-1", first.Ref)
	if old.Status != "expired" || second.Status != "held" || held(t, pool) != 1 {
		t.Fatalf("old %s new %s held %d", old.Status, second.Status, held(t, pool))
	}
	assertInvariants(t, pool)
}

func TestReserveRejectsOutsideSaleWindow(t *testing.T) {
	for name, e := range map[string]pgtest.Event{
		catalog.StateNotOpen: {OpensIn: time.Hour, ClosesIn: 2 * time.Hour},
		catalog.StateClosed:  {OpensIn: -2 * time.Hour, ClosesIn: -time.Hour},
	} {
		pool := setup(t, e)
		_, _, err := Reserve(ctx, pool, request("buyer-1", "key-00000001", 1))
		var notOnSale *NotOnSaleError
		if !errors.As(err, &notOnSale) || notOnSale.State != name || err.Error() == "" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestReserveRejectsBadRequests(t *testing.T) {
	pool := setup(t, pgtest.Event{})
	if _, _, err := Reserve(ctx, pool, Request{EventSlug: "missing"}); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("missing event: %v", err)
	}
	bad := request("buyer-1", "key-00000001", 1)
	bad.Input.Email = "nope"
	var fe FieldErrors
	if _, _, err := Reserve(ctx, pool, bad); !errors.As(err, &fe) || fe["email"] == "" {
		t.Errorf("invalid input: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ticket_type (event_id, name, price_minor, fee_minor, capacity)
		SELECT id, 'VIP', 1, 0, 0 FROM event WHERE slug = 'live'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Reserve(ctx, pool, request("buyer-1", "key-00000001", 1)); !errors.Is(err, catalog.ErrUnsupportedTiers) {
		t.Errorf("two tiers: %v", err)
	}
}

func TestReserveReportsRemainingWhenQuantityDoesNotFit(t *testing.T) {
	pool := setup(t, pgtest.Event{Shards: []int32{1, 1}})
	_, _, err := Reserve(ctx, pool, request("buyer-1", "key-00000001", 3))
	var soldOut *SoldOutError
	if !errors.As(err, &soldOut) || soldOut.Remaining != 2 || err.Error() != "only 2 places remain" {
		t.Fatalf("err = %v", err)
	}
	// Two places across two shards fit in one hold.
	o, _, err := Reserve(ctx, pool, request("buyer-1", "key-00000002", 2))
	if err != nil || o.Quantity != 2 {
		t.Fatalf("multi-shard hold: %v", err)
	}
	_, _, err = Reserve(ctx, pool, request("buyer-2", "key-00000003", 1))
	if !errors.As(err, &soldOut) || soldOut.Remaining != 0 {
		t.Fatalf("sold out err = %v", err)
	}
	assertInvariants(t, pool)
}

func TestReserveWithNoShardsIsSoldOut(t *testing.T) {
	pool := setup(t, pgtest.Event{Shards: []int32{}})
	_, _, err := Reserve(ctx, pool, request("buyer-1", "key-00000001", 1))
	var soldOut *SoldOutError
	if !errors.As(err, &soldOut) || soldOut.Remaining != 0 {
		t.Fatalf("err = %v", err)
	}
}

// withSeams sets the shard choice and the between-choose-and-lock hook.
func withSeams(t *testing.T, start int, hook func()) {
	t.Helper()
	origIntn, origHook := intn, afterChoose
	t.Cleanup(func() { intn, afterChoose = origIntn, origHook })
	intn = func(int) int { return start }
	afterChoose = hook
}

func TestReserveRetriesWhenChosenShardIsTakenFirst(t *testing.T) {
	pool := setup(t, pgtest.Event{Shards: []int32{2, 2}})
	fired := false
	withSeams(t, 0, func() {
		if !fired {
			fired = true
			if _, err := pool.Exec(ctx, `UPDATE inventory_shard SET sold = 2 WHERE shard = 0`); err != nil {
				t.Error(err)
			}
		}
	})
	o, _, err := Reserve(ctx, pool, request("buyer-1", "key-00000001", 2))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	var shard int
	_ = pool.QueryRow(ctx, `SELECT shard FROM hold WHERE order_id = $1`, o.ID).Scan(&shard)
	if shard != 1 {
		t.Fatalf("hold on shard %d, want 1", shard)
	}
	assertInvariants(t, pool)
}

func TestReserveFallsBackToLockingEveryShard(t *testing.T) {
	pool := setup(t, pgtest.Event{Shards: []int32{1, 1, 1, 1}})
	taken := 0
	withSeams(t, 0, func() {
		// Sell whichever shard was just chosen (the lowest free one), three times.
		if taken < optimisticAttempts {
			taken++
			if _, err := pool.Exec(ctx, `UPDATE inventory_shard SET sold = 1
				WHERE shard = (SELECT min(shard) FROM inventory_shard WHERE sold = 0)`); err != nil {
				t.Error(err)
			}
		}
	})
	if _, _, err := Reserve(ctx, pool, request("buyer-1", "key-00000001", 1)); err != nil {
		t.Fatalf("Reserve after %d retries: %v", taken, err)
	}
	if taken != optimisticAttempts || held(t, pool) != 1 {
		t.Fatalf("taken %d held %d", taken, held(t, pool))
	}
}

func TestConcurrentDuplicateSubmitReturnsTheWinningOrder(t *testing.T) {
	pool := setup(t, pgtest.Event{})
	var winner Order
	fired := false
	withSeams(t, 0, func() {
		if !fired {
			fired = true
			var err error
			if winner, _, err = Reserve(ctx, pool, request("buyer-1", "key-00000001", 1)); err != nil {
				t.Error(err)
			}
		}
	})
	o, created, err := Reserve(ctx, pool, request("buyer-1", "key-00000001", 1))
	if err != nil || created || o.ID != winner.ID {
		t.Fatalf("loser got %+v created %v err %v; winner %s", o, created, err, winner.ID)
	}
	assertInvariants(t, pool)
}

func TestConcurrentSecondHoldIsRejected(t *testing.T) {
	pool := setup(t, pgtest.Event{})
	var winner Order
	fired := false
	withSeams(t, 0, func() {
		if !fired {
			fired = true
			winner, _, _ = Reserve(ctx, pool, request("buyer-1", "key-00000002", 1))
		}
	})
	_, _, err := Reserve(ctx, pool, request("buyer-1", "key-00000001", 1))
	var active *ActiveHoldError
	if !errors.As(err, &active) || active.Ref != winner.Ref {
		t.Fatalf("err = %v, winner %s", err, winner.Ref)
	}
	assertInvariants(t, pool)
}

func TestCancelReleasesOnce(t *testing.T) {
	pool := setup(t, pgtest.Event{})
	o, _, _ := Reserve(ctx, pool, request("buyer-1", "key-00000001", 2))
	if err := Cancel(ctx, pool, "buyer-1", o.Ref); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := Cancel(ctx, pool, "buyer-1", o.Ref); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("second Cancel err = %v", err)
	}
	if err := Cancel(ctx, pool, "buyer-2", o.Ref); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("other buyer Cancel err = %v", err)
	}
	got, _ := Get(ctx, pool, "buyer-1", o.Ref)
	if got.Status != "cancelled" || held(t, pool) != 0 {
		t.Fatalf("status %s held %d", got.Status, held(t, pool))
	}
	assertInvariants(t, pool)
}

func TestExpireDueReleasesOnlyExpiredHolds(t *testing.T) {
	pool := setup(t, pgtest.Event{})
	live, _, _ := Reserve(ctx, pool, request("buyer-1", "key-00000001", 1))
	old, _, _ := Reserve(ctx, pool, request("buyer-2", "key-00000002", 2))
	expire(t, pool, old.Ref)
	if n, err := ExpireDue(ctx, pool, 100); err != nil || n != 1 {
		t.Fatalf("ExpireDue = %d, %v", n, err)
	}
	if n, err := ExpireDue(ctx, pool, 100); err != nil || n != 0 {
		t.Fatalf("second ExpireDue = %d, %v", n, err)
	}
	if released, err := release(ctx, pool, live.ID, "expired", true); err != nil || released {
		t.Fatalf("live hold released: %v %v", released, err)
	}
	if held(t, pool) != 1 {
		t.Fatalf("held = %d", held(t, pool))
	}
	assertInvariants(t, pool)
}

func TestExpireDueSkipsHoldsReleasedConcurrently(t *testing.T) {
	pool := setup(t, pgtest.Event{})
	o, _, _ := Reserve(ctx, pool, request("buyer-1", "key-00000001", 1))
	expire(t, pool, o.Ref)
	// Another worker releases the hold between this worker's scan and its release.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	tx, _ := conn.Begin(ctx)
	_, _ = tx.Exec(ctx, `SELECT 1 FROM ticket_order WHERE id = $1 FOR UPDATE`, o.ID)
	done := make(chan int)
	go func() {
		n, _ := ExpireDue(ctx, pool, 100)
		done <- n
	}()
	time.Sleep(200 * time.Millisecond)
	if _, err := release(ctx, tx, o.ID, "cancelled", false); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)
	if n := <-done; n != 0 {
		t.Fatalf("ExpireDue released %d already-cancelled holds", n)
	}
	assertInvariants(t, pool)
}

func TestEveryFailurePointKeepsInventoryConsistent(t *testing.T) {
	pool := setup(t, pgtest.Event{Shards: []int32{50, 50}})
	buyer := 0
	ops := map[string]func(dbx.DB) error{
		"reserve": func(db dbx.DB) error {
			buyer++
			_, _, err := Reserve(ctx, db, request(fmt.Sprintf("fault-%d", buyer), "key-00000001", 2))
			return err
		},
		"replay": func(db dbx.DB) error {
			_, _, _ = Reserve(ctx, pool, request("replay", "key-00000001", 2))
			_, _, err := Reserve(ctx, db, request("replay", "key-00000001", 2))
			return err
		},
		"cancel": func(db dbx.DB) error {
			o, _, _ := Reserve(ctx, pool, request("cancel-me", "key-00000009", 1))
			return Cancel(ctx, db, "cancel-me", o.Ref)
		},
	}
	for _, name := range []string{"reserve", "replay", "cancel"} {
		pgtest.EachFault(t, pool, ops[name], func(failAt int, err error) {
			assertInvariants(t, pool)
		})
	}

	// Stale own hold cleared during Reserve, under every failure point.
	stale := 0
	pgtest.EachFault(t, pool, func(db dbx.DB) error {
		stale++
		buyer := fmt.Sprintf("stale-%d", stale)
		o, _, _ := Reserve(ctx, pool, request(buyer, "key-00000001", 1))
		expire(t, pool, o.Ref)
		_, _, err := Reserve(ctx, db, request(buyer, "key-00000002", 1))
		return err
	}, func(int, error) { assertInvariants(t, pool) })

	// Two expired holds released by ExpireDue, under every failure point.
	for i := range 2 {
		o, _, _ := Reserve(ctx, pool, request(fmt.Sprintf("expire-%d", i), "key-00000001", 1))
		expire(t, pool, o.Ref)
	}
	pgtest.EachFault(t, pool, func(db dbx.DB) error {
		_, err := ExpireDue(ctx, db, 100)
		return err
	}, func(int, error) { assertInvariants(t, pool) })
	assertInvariants(t, pool)
}

func TestActiveHoldLookupFailure(t *testing.T) {
	pool := setup(t, pgtest.Event{})
	if err := activeHold(ctx, pool, "00000000-0000-0000-0000-000000000000", "nobody"); err == nil {
		t.Fatal("expected error when the conflicting hold cannot be found")
	}
}
