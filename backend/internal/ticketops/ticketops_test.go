package ticketops

import (
	"context"
	"crypto/rand"
	"errors"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"kacheem/backend/internal/admissions"
	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/orders"
	"kacheem/backend/internal/payments"
	"kacheem/backend/internal/payments/fakegw"
	"kacheem/backend/internal/pgtest"
	"kacheem/backend/internal/sealbox"
	"kacheem/backend/internal/tickets"
)

var ctx = context.Background()

type env struct {
	t     *testing.T
	pool  *pgxpool.Pool
	iss   *tickets.Issuer
	gw    *fakegw.Gateway
	svc   *payments.Service
	staff string
	gate  string
	n     int
}

func setup(t *testing.T) *env {
	t.Helper()
	pool := pgtest.Pool(t)
	ids := pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live", Shards: []int32{100}, MaxPerOrder: 5})
	_, _ = pool.Exec(ctx, `UPDATE event SET admission_opens_at = now() - interval '1 hour'`)
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	box, _ := sealbox.New(k)
	iss := &tickets.Issuer{Box: box}
	gw := &fakegw.Gateway{DB: pool, BaseURL: "https://tickets.test"}
	e := &env{t: t, pool: pool, iss: iss, gw: gw,
		svc: &payments.Service{DB: pool, Gateway: gw, ReturnURL: "https://tickets.test/payments/return/fake", OnPaid: iss.IssueInTx}}
	_ = pool.QueryRow(ctx, `INSERT INTO staff (email, name, role) VALUES ('s@example.org', 'Support', 'support') RETURNING id`).Scan(&e.staff)
	_ = pool.QueryRow(ctx, `INSERT INTO gate (event_id, name) VALUES ($1, 'North') RETURNING id`, ids.EventID).Scan(&e.gate)
	return e
}

// paid returns a paid order's ID and its ticket IDs.
func (e *env) paid(qty int) (string, []string) {
	e.t.Helper()
	e.n++
	buyer := "buyer-" + string(rune('a'+e.n))
	o, _, err := orders.Reserve(ctx, e.pool, orders.Request{EventSlug: "live", BuyerID: buyer, IdempotencyKey: "key-00000001",
		Input: orders.Input{Quantity: qty, Name: "A Buyer", Email: "a@example.com", Phone: "+8801711000000", Age: 30}})
	if err != nil {
		e.t.Fatal(err)
	}
	redirect, _ := e.svc.Checkout(ctx, buyer, o.Ref)
	u, _ := url.Parse(redirect)
	back, _ := e.gw.Decide(ctx, u.Path[len("/payments/fake/pay/"):], "approve")
	bu, _ := url.Parse(back)
	if _, err := e.svc.Return(ctx, bu.Query()); err != nil {
		e.t.Fatal(err)
	}
	rows, _ := e.pool.Query(ctx, `SELECT id FROM ticket WHERE order_id = $1 ORDER BY ordinal`, o.ID)
	var ids []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	return o.ID, ids
}

func (e *env) secrets(ticketID string) tickets.Secrets {
	e.t.Helper()
	var sealed []byte
	_ = e.pool.QueryRow(ctx, `SELECT credential_sealed FROM ticket WHERE id = $1`, ticketID).Scan(&sealed)
	s, err := e.iss.Open(sealed)
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *env) admit(cred, request string) string {
	e.t.Helper()
	res, err := admissions.Admit(ctx, e.pool, "https://gate.test", admissions.Scan{DeviceID: "device-north", RequestID: request,
		GateID: e.gate, StaffID: e.staff, QR: "https://gate.test/scan#" + cred})
	if err != nil {
		e.t.Fatal(err)
	}
	return res.Outcome
}

func (e *env) status(ticketID string) string {
	var s string
	_ = e.pool.QueryRow(ctx, `SELECT status FROM ticket WHERE id = $1`, ticketID).Scan(&s)
	return s
}

func (e *env) audits(action string) int {
	var n int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM audit_event WHERE action = $1 AND actor_id = $2`, action, e.staff).Scan(&n)
	return n
}

func TestVoidStopsAdmission(t *testing.T) {
	e := setup(t)
	_, ids := e.paid(1)
	cred := e.secrets(ids[0]).Credential
	if err := Void(ctx, e.pool, e.staff, ids[0], ""); !errors.Is(err, ErrReason) {
		t.Fatal("void without reason")
	}
	if err := Void(ctx, e.pool, e.staff, ids[0], "duplicate booking"); err != nil {
		t.Fatal(err)
	}
	if out := e.admit(cred, "request-0001"); out != admissions.Void || e.audits("ticket.void") != 1 {
		t.Fatalf("after void: %s", out)
	}
	if err := Void(ctx, e.pool, e.staff, ids[0], "again"); !errors.Is(err, ErrNotValid) {
		t.Fatalf("void twice: %v", err)
	}
	if err := Void(ctx, e.pool, e.staff, "nope", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestReissueReplacesTheCredentials(t *testing.T) {
	e := setup(t)
	orderID, ids := e.paid(2)
	old := e.secrets(ids[0])
	_, _ = e.pool.Exec(ctx, `INSERT INTO download_grant (order_id, secret_digest, expires_at) VALUES ($1, $2, now() + interval '1 day')`,
		orderID, tickets.Digest("old-link"))
	if err := Reissue(ctx, e.pool, e.iss, e.staff, ids[0], ""); !errors.Is(err, ErrReason) {
		t.Fatal("reissue without reason")
	}
	if err := Reissue(ctx, e.pool, e.iss, e.staff, ids[0], "buyer lost phone"); err != nil {
		t.Fatal(err)
	}
	fresh := e.secrets(ids[0])
	var version int
	_ = e.pool.QueryRow(ctx, `SELECT credential_version FROM ticket WHERE id = $1`, ids[0]).Scan(&version)
	if fresh.Credential == old.Credential || fresh.Fallback == old.Fallback || version != 2 {
		t.Fatal("credentials not replaced")
	}
	if _, err := tickets.Redeem(ctx, e.pool, "old-link"); !errors.Is(err, tickets.ErrGrantInvalid) {
		t.Fatal("old pickup link still works")
	}
	var jobs int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_job WHERE kind = 'render_tickets' AND dedupe_key LIKE '%:reissue:%'`).Scan(&jobs)
	if jobs != 1 || e.audits("ticket.reissue") != 1 {
		t.Fatalf("jobs %d", jobs)
	}
	if out := e.admit(old.Credential, "request-0001"); out != admissions.Unknown {
		t.Fatalf("old QR: %s", out)
	}
	if out := e.admit(fresh.Credential, "request-0002"); out != admissions.Admitted {
		t.Fatalf("new QR: %s", out)
	}
	if err := Reissue(ctx, e.pool, e.iss, e.staff, ids[0], "again"); !errors.Is(err, ErrAdmitted) {
		t.Fatalf("reissue after admission: %v", err)
	}
	if err := Reissue(ctx, e.pool, e.iss, e.staff, "nope", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestRefundBlocksAtOnceAndVoidsWhenSettled(t *testing.T) {
	e := setup(t)
	orderID, ids := e.paid(2)
	creds := []string{e.secrets(ids[0]).Credential, e.secrets(ids[1]).Credential}
	if err := RequestRefund(ctx, e.pool, e.staff, orderID, ""); !errors.Is(err, ErrReason) {
		t.Fatal("refund without reason")
	}
	if err := RequestRefund(ctx, e.pool, e.staff, orderID, "event clash"); err != nil {
		t.Fatal(err)
	}
	if e.status(ids[0]) != "refund_pending" || e.audits("order.refund_requested") != 1 {
		t.Fatal("tickets not blocked")
	}
	if l, _ := payments.GetLedger(ctx, e.pool); !l.Balanced() || l.RefundOpenMinor != 310000 || l.PaidOrdersMinor != 0 {
		t.Fatalf("ledger while pending %+v", l)
	}
	if out := e.admit(creds[0], "request-0001"); out != admissions.Void {
		t.Fatalf("blocked ticket admitted: %s", out)
	}
	if err := RequestRefund(ctx, e.pool, e.staff, orderID, "again"); !errors.Is(err, ErrNotValid) {
		t.Fatalf("second request: %v", err)
	}
	if _, err := e.svc.ProcessRefunds(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if e.status(ids[0]) != "void" || e.status(ids[1]) != "void" {
		t.Fatal("tickets not voided after the refund settled")
	}
	l, _ := payments.GetLedger(ctx, e.pool)
	if !l.Balanced() || l.RefundedMinor != 310000 {
		t.Fatalf("ledger %+v", l)
	}
}

// An offline sale's money is handed back by the desk, so its refund is
// settled when it is recorded and the tickets stop working at once.
func TestOfflineRefundSettlesAtOnce(t *testing.T) {
	e := setup(t)
	o, _, err := orders.SellOffline(ctx, e.pool, orders.OfflineSale{EventSlug: "live", StaffID: e.staff,
		IdempotencyKey: "offline-0001", Method: "cash",
		Input: orders.Input{Quantity: 2, Name: "A Buyer", Email: "a@example.com", Phone: "+8801711000000", Age: 30}},
		e.iss.IssueInTx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	rows, _ := e.pool.Query(ctx, `SELECT id::text FROM ticket WHERE order_id = $1 ORDER BY ordinal`, o.ID)
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	if len(ids) != 2 {
		t.Fatalf("issued %d tickets", len(ids))
	}
	if err := RequestRefund(ctx, e.pool, e.staff, o.ID, "buyer cannot come"); err != nil {
		t.Fatal(err)
	}
	if e.status(ids[0]) != "void" || e.status(ids[1]) != "void" || e.audits("order.refund_requested") != 1 {
		t.Fatal("offline refund did not void the tickets")
	}
	var status, providerID string
	_ = e.pool.QueryRow(ctx, `SELECT r.status, r.provider_refund_id FROM refund r
		JOIN payment_attempt a ON a.id = r.attempt_id WHERE a.order_id = $1`, o.ID).Scan(&status, &providerID)
	if status != "completed" || providerID != "manual" {
		t.Fatalf("refund %s %s", status, providerID)
	}
	// The gateway's refund worker never touches it.
	if n, err := e.svc.ProcessRefunds(ctx, 10); err != nil || n != 0 {
		t.Fatalf("worker took %d offline refunds (%v)", n, err)
	}
	if l, _ := payments.GetLedger(ctx, e.pool); !l.Balanced() || l.RefundedMinor != 300000 || l.RefundOpenMinor != 0 {
		t.Fatalf("ledger %+v", l)
	}
}

func TestRefundRefusals(t *testing.T) {
	e := setup(t)
	orderID, ids := e.paid(2)
	e.admit(e.secrets(ids[1]).Credential, "request-0001")
	if err := RequestRefund(ctx, e.pool, e.staff, orderID, "x"); !errors.Is(err, ErrAdmitted) {
		t.Fatalf("admitted order: %v", err)
	}
	if e.status(ids[0]) != "valid" {
		t.Fatal("partial block")
	}
	if err := RequestRefund(ctx, e.pool, e.staff, "00000000-0000-0000-0000-000000000000", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	// An order whose tickets exist but whose payment was already refunded.
	other, _ := e.paid(1)
	_, _ = e.pool.Exec(ctx, `INSERT INTO refund (refund_ref, attempt_id, amount_minor, currency, reason)
		SELECT 'RF-x', id, captured_minor, currency, 'duplicate_charge' FROM payment_attempt WHERE order_id = $1`, other)
	if err := RequestRefund(ctx, e.pool, e.staff, other, "x"); !errors.Is(err, ErrNoPayment) {
		t.Fatalf("no payment: %v", err)
	}
}

// A refund and a gate scan racing on the same order are serialized: either
// the scan admits and the refund is refused, or the refund blocks and the
// scan refuses. Never both.
func TestRefundRacingAScan(t *testing.T) {
	e := setup(t)
	for round := range 10 {
		orderID, ids := e.paid(1)
		cred := e.secrets(ids[0]).Credential
		done := make(chan string)
		go func() { done <- e.admit(cred, "race-"+string(rune('a'+round))+"-scan") }()
		refundErr := RequestRefund(ctx, e.pool, e.staff, orderID, "race")
		scan := <-done
		switch {
		case scan == admissions.Admitted && errors.Is(refundErr, ErrAdmitted):
		case scan == admissions.Void && refundErr == nil:
		default:
			t.Fatalf("round %d: scan %s, refund %v", round, scan, refundErr)
		}
	}
}

func TestFaults(t *testing.T) {
	e := setup(t)
	for _, op := range []func(db dbx.DB) error{
		func(db dbx.DB) error { _, ids := e.paid(1); return Void(ctx, db, e.staff, ids[0], "x") },
		func(db dbx.DB) error { _, ids := e.paid(1); return Reissue(ctx, db, e.iss, e.staff, ids[0], "x") },
		func(db dbx.DB) error { o, _ := e.paid(1); return RequestRefund(ctx, db, e.staff, o, "x") },
	} {
		pgtest.EachFault(t, e.pool, op, func(int, error) {})
	}
}
