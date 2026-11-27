package ops

import (
	"context"
	"crypto/rand"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"kacheem/backend/internal/admissions"
	"kacheem/backend/internal/audit"
	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/orders"
	"kacheem/backend/internal/payments"
	"kacheem/backend/internal/payments/fakegw"
	"kacheem/backend/internal/pgtest"
	"kacheem/backend/internal/sealbox"
	"kacheem/backend/internal/ticketops"
	"kacheem/backend/internal/tickets"
)

var ctx = context.Background()

type env struct {
	t       *testing.T
	pool    *pgxpool.Pool
	iss     *tickets.Issuer
	svc     *payments.Service
	gw      *fakegw.Gateway
	eventID string
	staff   string
	n       int
}

func setup(t *testing.T) *env {
	t.Helper()
	pool := pgtest.Pool(t)
	ids := pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live", Shards: []int32{50, 50}})
	_, _ = pool.Exec(ctx, `UPDATE event SET admission_opens_at = now() - interval '1 hour'`)
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	box, _ := sealbox.New(k)
	iss := &tickets.Issuer{Box: box}
	gw := &fakegw.Gateway{DB: pool, BaseURL: "https://tickets.test"}
	e := &env{t: t, pool: pool, iss: iss, gw: gw, eventID: ids.EventID,
		svc: &payments.Service{DB: pool, Gateway: gw, ReturnURL: "https://tickets.test/r", OnPaid: iss.IssueInTx}}
	_ = pool.QueryRow(ctx, `INSERT INTO staff (email, name, role) VALUES ('g@example.org', 'Gate Person', 'gate') RETURNING id`).Scan(&e.staff)
	return e
}

func (e *env) paid(name, email string) (string, string) {
	e.t.Helper()
	e.n++
	buyer := strings.Repeat("b", e.n)
	o, _, err := orders.Reserve(ctx, e.pool, orders.Request{EventSlug: "live", BuyerID: buyer, IdempotencyKey: "key-00000001",
		Input: orders.Input{Quantity: 2, Name: name, Email: email, Phone: "+8801711000000", Age: 30}})
	if err != nil {
		e.t.Fatal(err)
	}
	redirect, _ := e.svc.Checkout(ctx, buyer, o.Ref)
	u, _ := url.Parse(redirect)
	back, _ := e.gw.Decide(ctx, u.Path[len("/payments/fake/pay/"):], "approve")
	bu, _ := url.Parse(back)
	_, _ = e.svc.Return(ctx, bu.Query())
	return o.ID, o.Ref
}

func TestMask(t *testing.T) {
	for in, want := range map[string]string{
		"nusrat@example.com": "n*****@example.com", "a@b.c": "a*@b.c", "+8801711000000": "+880*******000", "12345": "*****",
	} {
		if got := Mask(in); got != want {
			t.Errorf("Mask(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSummarySearchAndDetail(t *testing.T) {
	e := setup(t)
	orderID, ref := e.paid("Nusrat Jahan", "nusrat@example.com")
	e.paid("Karim Ahmed", "karim@example.com")
	gateID, err := AddGate(ctx, e.pool, audit.Staff(e.staff), e.eventID, " North ")
	if err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	var ticketID string
	_ = e.pool.QueryRow(ctx, `SELECT id, credential_sealed FROM ticket WHERE order_id = $1 AND ordinal = 1`, orderID).Scan(&ticketID, &sealed)
	sec, _ := e.iss.Open(sealed)
	if _, err := admissions.Admit(ctx, e.pool, "https://gate.test", admissions.Scan{DeviceID: "device-north", RequestID: "request-0001",
		GateID: gateID, StaffID: e.staff, QR: "https://gate.test/scan#" + sec.Credential}); err != nil {
		t.Fatal(err)
	}
	var other string
	_ = e.pool.QueryRow(ctx, `SELECT id FROM ticket WHERE order_id = $1 AND ordinal = 2`, orderID).Scan(&other)
	_ = ticketops.Reissue(ctx, e.pool, e.iss, e.staff, other, "lost phone")

	s, err := GetSummary(ctx, e.pool, "live")
	if err != nil || s.Sold != 4 || s.PaidOrders != 2 || s.RevenueMinor != 620000 || s.Admitted != 1 || s.Capacity != 100 ||
		s.Status != "published" || s.Currency != "BDT" || s.Held != 0 {
		t.Fatalf("summary %+v %v", s, err)
	}
	if _, err := GetSummary(ctx, e.pool, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}

	for q, want := range map[string]int{ref: 1, strings.ToLower(ref): 1, "NUSRAT@example.com": 1, "+880 1711-000000": 2, "jahan": 1, "zz": 0} {
		rows, err := Search(ctx, e.pool, q, true)
		if err != nil || len(rows) != want {
			t.Errorf("Search(%q) = %d rows, %v", q, len(rows), err)
		}
	}
	masked, _ := Search(ctx, e.pool, ref, false)
	if masked[0].Email != "n*****@example.com" || masked[0].Phone != "+880*******000" || masked[0].Quantity != 2 {
		t.Fatalf("masked %+v", masked[0])
	}

	d, err := GetDetail(ctx, e.pool, strings.ToLower(ref), true)
	if err != nil || d.ID != orderID || len(d.Tickets) != 2 || d.Tickets[0].Gate == nil || *d.Tickets[0].Gate != "North" ||
		d.Tickets[1].Version != 2 || len(d.Attempts) != 1 || d.Attempts[0].Status != "succeeded" || len(d.Refunds) != 0 ||
		len(d.History) != 1 || d.History[0].Action != "ticket.reissue" || d.History[0].Actor != "Gate Person" {
		t.Fatalf("detail %+v %v", d, err)
	}
	if _, err := GetDetail(ctx, e.pool, "NOPE0-NOPE0", true); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestFinanceQueue(t *testing.T) {
	e := setup(t)
	orderID, ref := e.paid("A", "a@example.com")
	_ = ticketops.RequestRefund(ctx, e.pool, e.staff, orderID, "clash")
	_, _ = e.pool.Exec(ctx, `UPDATE payment_attempt SET status = 'review'`)
	items, err := FinanceQueue(ctx, e.pool)
	if err != nil || len(items) != 2 || items[0].OrderRef != ref {
		t.Fatalf("%+v %v", items, err)
	}
	kinds := items[0].Kind + "," + items[1].Kind
	if kinds != "payment_review,refund" && kinds != "refund,payment_review" {
		t.Fatalf("kinds %s", kinds)
	}
}

func TestGates(t *testing.T) {
	e := setup(t)
	north, _ := AddGate(ctx, e.pool, audit.Staff(e.staff), e.eventID, "North")
	south, _ := AddGate(ctx, e.pool, audit.Staff(e.staff), e.eventID, "South")
	if _, err := AddGate(ctx, e.pool, audit.Staff(e.staff), e.eventID, "North"); err == nil {
		t.Fatal("duplicate gate accepted")
	}
	for range 2 {
		if err := Assign(ctx, e.pool, audit.Staff(e.staff), north, e.staff); err != nil {
			t.Fatal(err)
		}
	}
	if err := Assign(ctx, e.pool, audit.Staff(e.staff), north, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown staff: %v", err)
	}
	gates, err := Gates(ctx, e.pool, e.eventID)
	if err != nil || len(gates) != 2 || gates[0].Name != "North" || len(gates[0].Staff) != 1 || len(gates[1].Staff) != 0 {
		t.Fatalf("%+v %v", gates, err)
	}
	mine, _ := GatesFor(ctx, e.pool, e.staff, "gate", e.eventID)
	all, _ := GatesFor(ctx, e.pool, e.staff, "organizer", e.eventID)
	if len(mine) != 1 || mine[0].ID != north || len(all) != 2 || all[1].ID != south {
		t.Fatalf("mine %+v all %+v", mine, all)
	}
}

func TestFaults(t *testing.T) {
	e := setup(t)
	_, ref := e.paid("A", "a@example.com")
	north, _ := AddGate(ctx, e.pool, audit.Staff(e.staff), e.eventID, "North")
	n := 0
	for _, op := range []func(dbx.DB) error{
		func(db dbx.DB) error { _, err := GetSummary(ctx, db, "live"); return err },
		func(db dbx.DB) error { _, err := Search(ctx, db, ref, false); return err },
		func(db dbx.DB) error { _, err := GetDetail(ctx, db, ref, false); return err },
		func(db dbx.DB) error { _, err := FinanceQueue(ctx, db); return err },
		func(db dbx.DB) error { _, err := Gates(ctx, db, e.eventID); return err },
		func(db dbx.DB) error { _, err := GatesFor(ctx, db, e.staff, "gate", e.eventID); return err },
		func(db dbx.DB) error {
			n++
			_, err := AddGate(ctx, db, audit.Staff(e.staff), e.eventID, strings.Repeat("G", n))
			return err
		},
		func(db dbx.DB) error { return Assign(ctx, db, audit.Staff(e.staff), north, e.staff) },
	} {
		pgtest.EachFault(t, e.pool, op, func(int, error) {})
	}
}
