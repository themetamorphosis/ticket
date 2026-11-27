package orders

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kacheem/backend/internal/catalog"
	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/pgtest"
)

func newStaff(t *testing.T, pool *pgxpool.Pool, email string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO staff (email, name, role) VALUES ($1, 'Desk', 'support') RETURNING id::text`,
		email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func sale(staffID, key string, qty int, method, ref string) OfflineSale {
	return OfflineSale{
		EventSlug: "live", StaffID: staffID, IdempotencyKey: key, Method: method, Reference: ref,
		Input: Input{Quantity: qty, Name: "Rahim Uddin", Email: "rahim@example.com", Phone: "+8801811000000", Age: 34},
	}
}

// paidHook records the orders it was called for, inside the sale transaction.
type paidHook struct {
	ids []string
	err error
}

func (h *paidHook) fn(ctx context.Context, tx pgx.Tx, orderID string) error {
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM ticket_order WHERE id = $1`, orderID).Scan(&status); err != nil || status != "paid" {
		return errors.New("hook ran before the order was paid")
	}
	h.ids = append(h.ids, orderID)
	return h.err
}

func TestSellOfflineRecordsAPaidOrderWithoutTheOnlineFee(t *testing.T) {
	pool := setup(t, pgtest.Event{})
	staff := newStaff(t, pool, "desk@example.org")
	hook := &paidHook{}
	o, created, err := SellOffline(ctx, pool, sale(staff, "offline-0001", 2, "bkash", "  8AB12CD34E  "), hook.fn)
	if err != nil || !created {
		t.Fatalf("%v %v", created, err)
	}
	if o.Status != "paid" || o.Channel != "offline" || o.Quantity != 2 || o.UnitFeeMinor != 0 ||
		o.TotalMinor != 300000 || o.Name != "Rahim Uddin" || o.Email != "rahim@example.com" {
		t.Fatalf("%+v", o)
	}
	if len(hook.ids) != 1 || hook.ids[0] != o.ID {
		t.Fatalf("hook %v", hook.ids)
	}
	var sold, heldN int
	_ = pool.QueryRow(ctx, `SELECT sum(sold), sum(held) FROM inventory_shard`).Scan(&sold, &heldN)
	if sold != 2 || heldN != 0 {
		t.Fatalf("sold %d held %d", sold, heldN)
	}
	assertInvariants(t, pool)

	var (
		provider, status, method, ref, by, trx string
		amount, captured                       int64
	)
	if err := pool.QueryRow(ctx, `SELECT provider, status, offline_method, offline_reference, received_by::text,
			provider_trx_id, amount_minor, captured_minor FROM payment_attempt WHERE order_id = $1`, o.ID).
		Scan(&provider, &status, &method, &ref, &by, &trx, &amount, &captured); err != nil {
		t.Fatal(err)
	}
	if provider != "offline" || status != "succeeded" || method != "bkash" || ref != "8AB12CD34E" || by != staff ||
		!strings.HasPrefix(trx, "OF-") || amount != 300000 || captured != 300000 {
		t.Fatalf("%s %s %s %q %s %s %d %d", provider, status, method, ref, by, trx, amount, captured)
	}
	var events, audits int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM payment_event e JOIN payment_attempt a ON a.id = e.attempt_id
		WHERE a.order_id = $1 AND e.kind = 'offline_received'`, o.ID).Scan(&events)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_event WHERE action = 'order.offline_sale'
		AND actor_id = $1 AND object_id = $2 AND detail->>'method' = 'bkash'`, staff, o.ID).Scan(&audits)
	if events != 1 || audits != 1 {
		t.Fatalf("events %d audits %d", events, audits)
	}
}

func TestSellOfflineWithoutReferenceStoresNull(t *testing.T) {
	pool := setup(t, pgtest.Event{})
	staff := newStaff(t, pool, "desk@example.org")
	o, _, err := SellOffline(ctx, pool, sale(staff, "offline-0001", 1, "cash", "   "), nil)
	if err != nil {
		t.Fatal(err)
	}
	var ref *string
	_ = pool.QueryRow(ctx, `SELECT offline_reference FROM payment_attempt WHERE order_id = $1`, o.ID).Scan(&ref)
	if ref != nil {
		t.Fatalf("reference %q", *ref)
	}
}

func TestSellOfflineIsIdempotentPerStaffMember(t *testing.T) {
	pool := setup(t, pgtest.Event{})
	a, b := newStaff(t, pool, "a@example.org"), newStaff(t, pool, "b@example.org")
	first, _, err := SellOffline(ctx, pool, sale(a, "offline-0001", 1, "cash", ""), nil)
	if err != nil {
		t.Fatal(err)
	}
	again, created, err := SellOffline(ctx, pool, sale(a, "offline-0001", 1, "cash", ""), nil)
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("%v %v %v", again.ID, created, err)
	}
	if _, _, err := SellOffline(ctx, pool, sale(a, "offline-0001", 1, "bank", ""), nil); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed method: %v", err)
	}
	other, created, err := SellOffline(ctx, pool, sale(b, "offline-0001", 1, "cash", ""), nil)
	if err != nil || !created || other.ID == first.ID {
		t.Fatalf("%v %v", created, err)
	}
	var sold int
	_ = pool.QueryRow(ctx, `SELECT sum(sold) FROM inventory_shard`).Scan(&sold)
	if sold != 2 {
		t.Fatalf("sold %d", sold)
	}
}

func TestSellOfflineReturnsTheConcurrentDuplicate(t *testing.T) {
	pool := setup(t, pgtest.Event{})
	staff := newStaff(t, pool, "desk@example.org")
	var dup Order
	beforeOfflineTx = func() {
		beforeOfflineTx = func() {}
		var err error
		if dup, _, err = SellOffline(ctx, pool, sale(staff, "offline-0001", 1, "cash", ""), nil); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { beforeOfflineTx = func() {} })
	o, created, err := SellOffline(ctx, pool, sale(staff, "offline-0001", 1, "cash", ""), nil)
	if err != nil || created || o.ID != dup.ID {
		t.Fatalf("%v %v %v", o.ID, created, err)
	}
	assertInvariants(t, pool)
}

func TestSellOfflineValidatesEveryField(t *testing.T) {
	pool := setup(t, pgtest.Event{MaxPerOrder: 4})
	staff := newStaff(t, pool, "desk@example.org")
	s := sale(staff, "offline-0001", 5, "card", strings.Repeat("x", 65))
	s.Input.Name = ""
	_, _, err := SellOffline(ctx, pool, s, nil)
	var fields FieldErrors
	if !errors.As(err, &fields) || len(fields) != 4 ||
		fields["method"] == "" || fields["reference"] == "" || fields["quantity"] == "" || fields["name"] == "" {
		t.Fatalf("%v", fields)
	}
	s = sale(staff, "offline-0001", 1, "cash", "line\nbreak")
	if _, _, err := SellOffline(ctx, pool, s, nil); !errors.As(err, &fields) || len(fields) != 1 || fields["reference"] == "" {
		t.Fatalf("%v", err)
	}
	// 64 characters is the longest reference.
	if _, _, err := SellOffline(ctx, pool, sale(staff, "offline-0002", 1, "bank", strings.Repeat("ব", 64)), nil); err != nil {
		t.Fatal(err)
	}
}

func TestSellOfflineWorksOutsideTheOnlineWindowUntilTheEventStarts(t *testing.T) {
	for name, e := range map[string]pgtest.Event{
		"before online sale": {OpensIn: time.Hour, ClosesIn: 2 * time.Hour},
		"after online sale":  {OpensIn: -2 * time.Hour, ClosesIn: -time.Hour},
	} {
		t.Run(name, func(t *testing.T) {
			pool := setup(t, e)
			staff := newStaff(t, pool, "desk@example.org")
			if _, _, err := SellOffline(ctx, pool, sale(staff, "offline-0001", 1, "cash", ""), nil); err != nil {
				t.Fatal(err)
			}
		})
	}

	pool := setup(t, pgtest.Event{})
	staff := newStaff(t, pool, "desk@example.org")
	_, _ = pool.Exec(ctx, `UPDATE event SET starts_at = now() - interval '1 minute', doors_at = NULL`)
	var notOnSale *NotOnSaleError
	if _, _, err := SellOffline(ctx, pool, sale(staff, "offline-0001", 1, "cash", ""), nil); !errors.As(err, &notOnSale) ||
		notOnSale.State != StateStarted {
		t.Fatalf("%v", err)
	}
}

func TestSellOfflineRefusesUnpublishedAndMultiTierEvents(t *testing.T) {
	pool := setup(t, pgtest.Event{Status: "draft"})
	staff := newStaff(t, pool, "desk@example.org")
	if _, _, err := SellOffline(ctx, pool, sale(staff, "offline-0001", 1, "cash", ""), nil); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("%v", err)
	}

	pool = setup(t, pgtest.Event{})
	staff = newStaff(t, pool, "desk@example.org")
	_, _ = pool.Exec(ctx, `INSERT INTO ticket_type (event_id, name, price_minor, fee_minor, capacity)
		SELECT id, 'VIP', 1, 0, 0 FROM event`)
	if _, _, err := SellOffline(ctx, pool, sale(staff, "offline-0001", 1, "cash", ""), nil); !errors.Is(err, catalog.ErrUnsupportedTiers) {
		t.Fatalf("%v", err)
	}
}

func TestSellOfflineReportsWhatIsLeft(t *testing.T) {
	pool := setup(t, pgtest.Event{Shards: []int32{1, 1}})
	staff := newStaff(t, pool, "desk@example.org")
	var soldOut *SoldOutError
	if _, _, err := SellOffline(ctx, pool, sale(staff, "offline-0001", 3, "cash", ""), nil); !errors.As(err, &soldOut) || soldOut.Remaining != 2 {
		t.Fatalf("%v", err)
	}
	// Two places in different shards still sell as one order.
	if _, _, err := SellOffline(ctx, pool, sale(staff, "offline-0002", 2, "cash", ""), nil); err != nil {
		t.Fatal(err)
	}
	assertInvariants(t, pool)
}

func TestSellOfflineRollsBackWhenIssuingFails(t *testing.T) {
	pool := setup(t, pgtest.Event{})
	staff := newStaff(t, pool, "desk@example.org")
	hook := &paidHook{err: errors.New("issuer down")}
	if _, _, err := SellOffline(ctx, pool, sale(staff, "offline-0001", 1, "cash", ""), hook.fn); err == nil {
		t.Fatal("sale committed despite the hook failing")
	}
	var orders, sold int
	_ = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM ticket_order), (SELECT sum(sold) FROM inventory_shard)`).Scan(&orders, &sold)
	if orders != 0 || sold != 0 {
		t.Fatalf("orders %d sold %d", orders, sold)
	}
}

func TestSellOfflineSurvivesEveryDatabaseFailure(t *testing.T) {
	pool := setup(t, pgtest.Event{})
	staff := newStaff(t, pool, "desk@example.org")
	pgtest.EachFault(t, pool, func(db dbx.DB) error {
		_, _, err := SellOffline(ctx, db, sale(staff, "offline-0001", 2, "cash", "R-1"), nil)
		return err
	}, func(int, error) {
		assertInvariants(t, pool)
		// Nothing, or (when only the read after commit failed) the whole sale.
		var orders, attempts, audits, sold int
		_ = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM ticket_order), (SELECT count(*) FROM payment_attempt),
			(SELECT count(*) FROM audit_event), (SELECT sum(sold) FROM inventory_shard)`).Scan(&orders, &attempts, &audits, &sold)
		if orders > 1 || attempts != orders || audits != orders || sold != 2*orders {
			t.Fatalf("partial sale: orders %d attempts %d audits %d sold %d", orders, attempts, audits, sold)
		}
	})
	// The replay path fails cleanly too.
	pgtest.EachFault(t, pool, func(db dbx.DB) error {
		_, _, err := SellOffline(ctx, db, sale(staff, "offline-0001", 2, "cash", "R-1"), nil)
		return err
	}, func(int, error) {})
}

// Release test "Capacity and money" for the desk: staff selling at the same
// time never oversell, and online holds and offline sales share the stock.
func TestOfflineAndOnlineSalesRaceForTheLastPlaces(t *testing.T) {
	pool := setup(t, pgtest.Event{Shards: []int32{3, 2}})
	wide := widePool(t, pool)
	staff := make([]string, 20)
	for i := range staff {
		staff[i] = newStaff(t, pool, "desk"+string(rune('a'+i))+"@example.org")
	}
	results := make([]error, 40)
	race(40, func(i int) {
		if i%2 == 0 {
			_, _, results[i] = SellOffline(ctx, wide, sale(staff[i/2], "offline-0001", 1, "cash", ""), nil)
			return
		}
		_, _, results[i] = Reserve(ctx, wide, request("buyer-"+string(rune('a'+i)), "key-00000001", 1))
	})
	ok := 0
	for _, err := range results {
		var soldOut *SoldOutError
		switch {
		case err == nil:
			ok++
		case !errors.As(err, &soldOut):
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 5 {
		t.Fatalf("%d sales or holds succeeded for 5 places", ok)
	}
	assertInvariants(t, pool)
}
