package payments_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/orders"
	"kacheem/backend/internal/payments"
	"kacheem/backend/internal/payments/fakegw"
	"kacheem/backend/internal/pgtest"
)

var ctx = context.Background()

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	gw   *fakegw.Gateway
	svc  *payments.Service
	n    int
}

func setup(t *testing.T) *env {
	t.Helper()
	pool := pgtest.Pool(t)
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live", Shards: []int32{100, 100}})
	gw := &fakegw.Gateway{DB: pool, BaseURL: "http://localhost:8088"}
	return &env{t: t, pool: pool, gw: gw, svc: &payments.Service{DB: pool, Gateway: gw, ReturnURL: "http://localhost:8088/payments/return/fake"}}
}

// reserve holds two places for a new buyer and returns (buyer, order ref).
func (e *env) reserve() (string, string) {
	e.t.Helper()
	e.n++
	buyer := fmt.Sprintf("buyer-%d", e.n)
	o, _, err := orders.Reserve(ctx, e.pool, orders.Request{EventSlug: "live", BuyerID: buyer, IdempotencyKey: "key-00000001",
		Input: orders.Input{Quantity: 2, Name: "Nusrat Jahan", Email: "nusrat@example.com", Phone: "+8801711000000", Age: 28}})
	if err != nil {
		e.t.Fatal(err)
	}
	return buyer, o.Ref
}

func (e *env) checkout(buyer, ref string) string {
	e.t.Helper()
	redirect, err := e.svc.Checkout(ctx, buyer, ref)
	if err != nil {
		e.t.Fatalf("Checkout: %v", err)
	}
	return redirect
}

func providerRef(redirect string) string {
	u, _ := url.Parse(redirect)
	return u.Path[len("/payments/fake/pay/"):]
}

// pay approves on the hosted page and follows the return redirect.
func (e *env) pay(redirect, action string) string {
	e.t.Helper()
	back, err := e.gw.Decide(ctx, providerRef(redirect), action)
	if err != nil {
		e.t.Fatal(err)
	}
	u, _ := url.Parse(back)
	ref, err := e.svc.Return(ctx, u.Query())
	if err != nil {
		e.t.Fatalf("Return: %v", err)
	}
	return ref
}

func (e *env) orderStatus(ref string) string {
	e.t.Helper()
	var s string
	if err := e.pool.QueryRow(ctx, `SELECT status FROM ticket_order WHERE public_ref = $1`, ref).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *env) attemptStatuses(ref string) []string {
	e.t.Helper()
	rows, _ := e.pool.Query(ctx, `SELECT a.status FROM payment_attempt a JOIN ticket_order o ON o.id = a.order_id
		WHERE o.public_ref = $1 ORDER BY a.created_at`, ref)
	var out []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func (e *env) expire(ref string) {
	e.t.Helper()
	if _, err := e.pool.Exec(ctx, `UPDATE ticket_order SET expires_at = now() - interval '1 second' WHERE public_ref = $1`, ref); err != nil {
		e.t.Fatal(err)
	}
}

// assertBooks checks the inventory invariant and that the ledger balances.
func (e *env) assertBooks() payments.Ledger {
	e.t.Helper()
	var bad int
	_ = e.pool.QueryRow(ctx, `
		SELECT count(*) FROM inventory_shard s
		WHERE s.held <> coalesce((SELECT sum(h.quantity) FROM hold h JOIN ticket_order o ON o.id = h.order_id
			WHERE o.status = 'held' AND h.ticket_type_id = s.ticket_type_id AND h.shard = s.shard), 0)
		OR s.sold <> coalesce((SELECT sum(h.quantity) FROM hold h JOIN ticket_order o ON o.id = h.order_id
			WHERE o.status = 'paid' AND h.ticket_type_id = s.ticket_type_id AND h.shard = s.shard), 0)`).Scan(&bad)
	if bad != 0 {
		e.t.Fatalf("inventory invariant broken on %d shards", bad)
	}
	l, err := payments.GetLedger(ctx, e.pool)
	if err != nil || !l.Balanced() {
		e.t.Fatalf("ledger not balanced: %+v (err %v)", l, err)
	}
	return l
}

func TestPaidOrderSellsItsPlaces(t *testing.T) {
	e := setup(t)
	buyer, ref := e.reserve()
	var before time.Time
	_ = e.pool.QueryRow(ctx, `SELECT expires_at FROM ticket_order WHERE public_ref = $1`, ref).Scan(&before)
	redirect := e.checkout(buyer, ref)

	var after time.Time
	_ = e.pool.QueryRow(ctx, `SELECT expires_at FROM ticket_order WHERE public_ref = $1`, ref).Scan(&after)
	if d := after.Sub(before); d != payments.CheckoutExtension {
		t.Fatalf("hold extended by %v", d)
	}
	if e.checkout(buyer, ref) != redirect {
		t.Fatal("a second checkout click opened a new payment instead of resuming")
	}
	var again time.Time
	_ = e.pool.QueryRow(ctx, `SELECT expires_at FROM ticket_order WHERE public_ref = $1`, ref).Scan(&again)
	if !again.Equal(after) {
		t.Fatal("hold extended twice")
	}

	if got := e.pay(redirect, "approve"); got != ref {
		t.Fatalf("return order %s", got)
	}
	if e.orderStatus(ref) != "paid" {
		t.Fatalf("order %s", e.orderStatus(ref))
	}
	l := e.assertBooks()
	if l.CapturedMinor != 310000 || l.PaidOrdersMinor != 310000 {
		t.Fatalf("ledger %+v", l)
	}
	// The same return arriving again changes nothing.
	u, _ := url.Parse(redirect)
	if got, err := e.svc.Return(ctx, url.Values{"ref": {providerRef(u.String())}, "status": {"success"}}); err != nil || got != ref {
		t.Fatalf("repeat return %s %v", got, err)
	}
	if _, err := e.svc.Checkout(ctx, buyer, ref); !errors.Is(err, payments.ErrHoldEnded) {
		t.Fatalf("checkout after paying: %v", err)
	}

	var orderID string
	_ = e.pool.QueryRow(ctx, `SELECT id FROM ticket_order WHERE public_ref = $1`, ref).Scan(&orderID)
	tl, err := payments.Timeline(ctx, e.pool, orderID)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, entry := range tl {
		kinds = append(kinds, entry.Kind)
	}
	want := []string{"order_created", "payment_started", "order_paid", "payment_succeeded"}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Fatalf("timeline %v, want %v", kinds, want)
	}
	if s, _ := payments.LatestStatus(ctx, e.pool, orderID); s != "succeeded" {
		t.Fatalf("latest %s", s)
	}
}

func TestDeclineAndCancelLeaveTheHoldForAnotherTry(t *testing.T) {
	e := setup(t)
	buyer, ref := e.reserve()
	for _, action := range []string{"decline", "cancel"} {
		e.pay(e.checkout(buyer, ref), action)
		if e.orderStatus(ref) != "held" {
			t.Fatalf("%s: order %s", action, e.orderStatus(ref))
		}
	}
	e.pay(e.checkout(buyer, ref), "approve")
	if fmt.Sprint(e.attemptStatuses(ref)) != "[failed failed succeeded]" || e.orderStatus(ref) != "paid" {
		t.Fatalf("attempts %v order %s", e.attemptStatuses(ref), e.orderStatus(ref))
	}
	e.assertBooks()
}

func TestCheckoutRefusals(t *testing.T) {
	e := setup(t)
	if _, err := e.svc.Checkout(ctx, "nobody", "NOPE0-NOPE0"); !errors.Is(err, orders.ErrOrderNotFound) {
		t.Fatalf("unknown order: %v", err)
	}
	buyer, ref := e.reserve()
	_, _ = e.pool.Exec(ctx, `UPDATE ticket_order SET expires_at = now() + interval '30 seconds' WHERE public_ref = $1`, ref)
	if _, err := e.svc.Checkout(ctx, buyer, ref); !errors.Is(err, payments.ErrHoldEnded) {
		t.Fatalf("too little time: %v", err)
	}
}

func TestGatewayOutageAtStartClosesTheAttempt(t *testing.T) {
	e := setup(t)
	buyer, ref := e.reserve()
	e.gw.FailNext("start", errors.New("connection refused"))
	if _, err := e.svc.Checkout(ctx, buyer, ref); !errors.Is(err, payments.ErrGatewayUnavailable) {
		t.Fatalf("err = %v", err)
	}
	e.checkout(buyer, ref)
	if fmt.Sprint(e.attemptStatuses(ref)) != "[failed pending]" {
		t.Fatalf("attempts %v", e.attemptStatuses(ref))
	}
}

// Sandbox scenario: the buyer approves and closes the browser.
func TestClosedBrowserIsCapturedByReconciliation(t *testing.T) {
	e := setup(t)
	buyer, ref := e.reserve()
	redirect := e.checkout(buyer, ref)
	if _, err := e.gw.Decide(ctx, providerRef(redirect), "approve"); err != nil {
		t.Fatal(err)
	}
	if n, err := e.svc.Reconcile(ctx, time.Hour, 10); err != nil || n != 0 {
		t.Fatalf("recent attempt reconciled too early: %d %v", n, err)
	}
	if n, err := e.svc.Reconcile(ctx, 0, 10); err != nil || n != 1 {
		t.Fatalf("Reconcile = %d, %v", n, err)
	}
	if e.orderStatus(ref) != "paid" {
		t.Fatalf("order %s", e.orderStatus(ref))
	}
	e.assertBooks()
}

// Sandbox scenario: the result is unknown (timeout) and settles later.
func TestPendingResultSettlesLater(t *testing.T) {
	e := setup(t)
	buyer, ref := e.reserve()
	redirect := e.checkout(buyer, ref)
	e.gw.FailNext("complete", errors.New("timeout"))
	e.pay(redirect, "approve")
	if e.orderStatus(ref) != "held" || fmt.Sprint(e.attemptStatuses(ref)) != "[pending]" {
		t.Fatalf("after timeout: order %s attempts %v", e.orderStatus(ref), e.attemptStatuses(ref))
	}
	e.gw.FailNext("query", errors.New("timeout"))
	if _, err := e.svc.Reconcile(ctx, 0, 10); err != nil {
		t.Fatal(err)
	}
	if e.orderStatus(ref) != "held" {
		t.Fatal("settled without an answer")
	}
	if _, err := e.svc.Reconcile(ctx, 0, 10); err != nil {
		t.Fatal(err)
	}
	if e.orderStatus(ref) != "paid" {
		t.Fatalf("order %s", e.orderStatus(ref))
	}
	e.assertBooks()
}

// Sandbox scenario: the buyer returns after the hold expired. bKash-style
// gateways capture only on Complete, so no money is taken at all.
func TestStaleHoldIsNeverCaptured(t *testing.T) {
	e := setup(t)
	buyer, ref := e.reserve()
	redirect := e.checkout(buyer, ref)
	e.expire(ref)
	e.pay(redirect, "approve")
	if fmt.Sprint(e.attemptStatuses(ref)) != "[abandoned]" {
		t.Fatalf("attempts %v", e.attemptStatuses(ref))
	}
	if r, _ := e.gw.Query(ctx, providerRef(redirect)); r.Status != payments.Authorized {
		t.Fatalf("gateway shows %s: money was captured", r.Status)
	}
	if l := e.assertBooks(); l.CapturedMinor != 0 {
		t.Fatalf("ledger %+v", l)
	}
}

// expiringGateway captures, then lets the hold expire before the result is
// recorded: the narrow race a capture-on-complete gateway still has.
type expiringGateway struct {
	*fakegw.Gateway
	pool *pgxpool.Pool
	ref  string
}

func (g *expiringGateway) Complete(ctx context.Context, providerRef string) (payments.Result, error) {
	res, err := g.Gateway.Complete(ctx, providerRef)
	_, _ = g.pool.Exec(ctx, `UPDATE ticket_order SET expires_at = now() - interval '1 second' WHERE public_ref = $1`, g.ref)
	return res, err
}

func TestLatePaymentIsRefunded(t *testing.T) {
	e := setup(t)
	buyer, ref := e.reserve()
	e.svc.Gateway = &expiringGateway{Gateway: e.gw, pool: e.pool, ref: ref}
	e.pay(e.checkout(buyer, ref), "approve")
	if e.orderStatus(ref) != "expired" || fmt.Sprint(e.attemptStatuses(ref)) != "[succeeded]" {
		t.Fatalf("order %s attempts %v", e.orderStatus(ref), e.attemptStatuses(ref))
	}
	l := e.assertBooks()
	if l.RefundOpenMinor != 310000 || l.PaidOrdersMinor != 0 {
		t.Fatalf("ledger %+v", l)
	}
	if n, err := e.svc.ProcessRefunds(ctx, 10); err != nil || n != 1 {
		t.Fatalf("ProcessRefunds = %d, %v", n, err)
	}
	if l := e.assertBooks(); l.RefundedMinor != 310000 || l.RefundOpenMinor != 0 {
		t.Fatalf("ledger after refund %+v", l)
	}
	var reason string
	_ = e.pool.QueryRow(ctx, `SELECT reason FROM refund`).Scan(&reason)
	if reason != "late_payment" {
		t.Fatalf("reason %s", reason)
	}
}

func attemptID(t *testing.T, pool *pgxpool.Pool, ref string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx, `SELECT a.id FROM payment_attempt a JOIN ticket_order o ON o.id = a.order_id
		WHERE o.public_ref = $1 AND a.status = 'pending'`, ref).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Sandbox scenario: a second successful charge for a paid order.
func TestDuplicateChargeIsRefunded(t *testing.T) {
	e := setup(t)
	buyer, ref := e.reserve()
	first := e.checkout(buyer, ref)
	// Make the first session unresumable so a second one opens.
	_, _ = e.pool.Exec(ctx, `UPDATE payment_attempt SET redirect_url = NULL`)
	second := e.checkout(buyer, ref)
	var secondID string
	_ = e.pool.QueryRow(ctx, `SELECT id FROM payment_attempt WHERE provider_ref = $1`, providerRef(second)).Scan(&secondID)
	e.pay(first, "approve")
	if err := payments.Finalize(e.svc, ctx, secondID, payments.Result{Status: payments.Succeeded,
		ProviderRef: providerRef(second), TrxID: "TRX-dup", AmountMinor: 310000, Currency: "BDT"}); err != nil {
		t.Fatal(err)
	}
	var reason string
	_ = e.pool.QueryRow(ctx, `SELECT reason FROM refund`).Scan(&reason)
	if reason != "duplicate_charge" || e.orderStatus(ref) != "paid" {
		t.Fatalf("reason %q order %s", reason, e.orderStatus(ref))
	}
	if l := e.assertBooks(); l.CapturedMinor != 620000 || l.RefundOpenMinor != 310000 {
		t.Fatalf("ledger %+v", l)
	}
}

// Sandbox scenario: the captured amount does not match the order.
func TestAmountMismatchGoesToReviewWithoutSelling(t *testing.T) {
	for name, res := range map[string]payments.Result{
		"amount":       {AmountMinor: 1, Currency: "BDT"},
		"currency":     {AmountMinor: 310000, Currency: "USD"},
		"merchant ref": {AmountMinor: 310000, Currency: "BDT", MerchantRef: "KC-someone-else"},
	} {
		e := setup(t)
		buyer, ref := e.reserve()
		e.checkout(buyer, ref)
		res.Status, res.TrxID = payments.Succeeded, "TRX-"+name
		if err := payments.Finalize(e.svc, ctx, attemptID(t, e.pool, ref), res); err != nil {
			t.Fatal(err)
		}
		if e.orderStatus(ref) != "held" || fmt.Sprint(e.attemptStatuses(ref)) != "[review]" {
			t.Fatalf("%s: order %s attempts %v", name, e.orderStatus(ref), e.attemptStatuses(ref))
		}
		if l := e.assertBooks(); l.ReviewMinor != res.AmountMinor {
			t.Fatalf("%s: ledger %+v", name, l)
		}
	}
}

func TestReconcileClosesWhatCanNoLongerSettle(t *testing.T) {
	e := setup(t)
	// A crash between writing the attempt and calling the provider.
	buyer1, ref1 := e.reserve()
	e.gw.FailNext("start", errors.New("boom"))
	_, _ = e.svc.Checkout(ctx, buyer1, ref1)
	_, _ = e.pool.Exec(ctx, `UPDATE payment_attempt SET status = 'starting'`)
	// A session the buyer never finished, past the provider's 24 hours.
	buyer2, ref2 := e.reserve()
	e.checkout(buyer2, ref2)
	_, _ = e.pool.Exec(ctx, `UPDATE payment_attempt a SET created_at = now() - interval '25 hours'
		FROM ticket_order o WHERE o.id = a.order_id AND o.public_ref = $1`, ref2)
	// An unfinished session whose hold has ended.
	buyer3, ref3 := e.reserve()
	e.checkout(buyer3, ref3)
	e.expire(ref3)
	// An unfinished session that is still in time: left alone.
	buyer4, ref4 := e.reserve()
	e.checkout(buyer4, ref4)
	// A session the buyer declined without returning.
	buyer5, ref5 := e.reserve()
	r5 := e.checkout(buyer5, ref5)
	_, _ = e.gw.Decide(ctx, providerRef(r5), "decline")

	if n, err := e.svc.Reconcile(ctx, 0, 10); err != nil || n != 5 {
		t.Fatalf("Reconcile = %d, %v", n, err)
	}
	for ref, want := range map[string]string{ref1: "[failed]", ref2: "[failed]", ref3: "[abandoned]", ref4: "[pending]", ref5: "[failed]"} {
		if got := fmt.Sprint(e.attemptStatuses(ref)); got != want {
			t.Errorf("%s: %s, want %s", ref, got, want)
		}
	}
	e.assertBooks()
}

func TestReturnRejectsUnknownPayments(t *testing.T) {
	e := setup(t)
	for _, q := range []url.Values{{}, {"ref": {"FAKE-nope"}, "status": {"success"}}} {
		if _, err := e.svc.Return(ctx, q); !errors.Is(err, payments.ErrUnknownPayment) {
			t.Errorf("Return(%v) err = %v", q, err)
		}
	}
}

func TestRefundRetriesUntilTheProviderAnswers(t *testing.T) {
	e := setup(t)
	buyer, ref := e.reserve()
	e.svc.Gateway = &expiringGateway{Gateway: e.gw, pool: e.pool, ref: ref}
	e.pay(e.checkout(buyer, ref), "approve")
	e.gw.FailNext("refund", errors.New("timeout"))
	if n, _ := e.svc.ProcessRefunds(ctx, 10); n != 1 {
		t.Fatalf("tried %d", n)
	}
	var status, lastErr string
	var tries int
	var waiting bool
	_ = e.pool.QueryRow(ctx, `SELECT status, tries, coalesce(last_error, ''), next_try_at > now() FROM refund`).
		Scan(&status, &tries, &lastErr, &waiting)
	if status != "pending" || tries != 1 || lastErr != "timeout" || !waiting {
		t.Fatalf("after timeout: %s %d %q waiting=%v", status, tries, lastErr, waiting)
	}
	if n, _ := e.svc.ProcessRefunds(ctx, 10); n != 0 {
		t.Fatal("refund retried before its backoff")
	}
	_, _ = e.pool.Exec(ctx, `UPDATE refund SET next_try_at = now()`)
	if n, _ := e.svc.ProcessRefunds(ctx, 10); n != 1 {
		t.Fatal("refund not retried")
	}
	if l := e.assertBooks(); l.RefundedMinor != 310000 {
		t.Fatalf("ledger %+v", l)
	}
}

func TestRefusedRefundNeedsFinance(t *testing.T) {
	e := setup(t)
	buyer, ref := e.reserve()
	e.svc.Gateway = &expiringGateway{Gateway: e.gw, pool: e.pool, ref: ref}
	e.pay(e.checkout(buyer, ref), "approve")
	// The provider has already refunded more than this payment allows.
	_, _ = e.pool.Exec(ctx, `UPDATE fake_gateway_payment SET refunded_minor = amount_minor`)
	_, _ = e.svc.ProcessRefunds(ctx, 10)
	if l := e.assertBooks(); l.RefundFailedMinor != 310000 {
		t.Fatalf("ledger %+v", l)
	}
}

// providerCaptures is a gateway that captures inside its own page (as
// PayStation will): Complete only verifies.
type providerCaptures struct{ *fakegw.Gateway }

func (providerCaptures) CapturesOnComplete() bool { return false }

func (g providerCaptures) Complete(ctx context.Context, ref string) (payments.Result, error) {
	return g.Gateway.Query(ctx, ref)
}

func TestGatewaysThatCaptureThemselves(t *testing.T) {
	e := setup(t)
	e.svc.Gateway = providerCaptures{e.gw}
	buyer, ref := e.reserve()
	redirect := e.checkout(buyer, ref)
	// The provider's page captured the money; the return only verifies.
	back, _ := e.gw.Decide(ctx, providerRef(redirect), "approve")
	_, _ = e.gw.Complete(ctx, providerRef(redirect))
	u, _ := url.Parse(back)
	if _, err := e.svc.Return(ctx, u.Query()); err != nil || e.orderStatus(ref) != "paid" {
		t.Fatalf("order %s err %v", e.orderStatus(ref), err)
	}

	// A cancel claim while the provider still shows the payment open keeps
	// it open (the provider might still capture); a decline closes it.
	buyer2, ref2 := e.reserve()
	r2 := e.checkout(buyer2, ref2)
	_, _ = e.svc.Return(ctx, url.Values{"ref": {providerRef(r2)}, "status": {"cancel"}})
	if fmt.Sprint(e.attemptStatuses(ref2)) != "[pending]" {
		t.Fatalf("attempts %v", e.attemptStatuses(ref2))
	}
	// Reconciliation leaves an open, in-time session alone, and an
	// authorized one for the provider to capture.
	if _, err := e.svc.Reconcile(ctx, 0, 10); err != nil || fmt.Sprint(e.attemptStatuses(ref2)) != "[pending]" {
		t.Fatalf("reconcile: %v %v", err, e.attemptStatuses(ref2))
	}
	_, _ = e.gw.Decide(ctx, providerRef(r2), "approve")
	if _, err := e.svc.Reconcile(ctx, 0, 10); err != nil || fmt.Sprint(e.attemptStatuses(ref2)) != "[pending]" {
		t.Fatalf("authorized reconcile: %v %v", err, e.attemptStatuses(ref2))
	}
	_, _ = e.gw.Complete(ctx, providerRef(r2)) // the provider captures
	_, _ = e.svc.Reconcile(ctx, 0, 10)
	if e.orderStatus(ref2) != "paid" {
		t.Fatalf("order %s", e.orderStatus(ref2))
	}

	buyer3, ref3 := e.reserve()
	r3 := e.checkout(buyer3, ref3)
	back3, _ := e.gw.Decide(ctx, providerRef(r3), "decline")
	u3, _ := url.Parse(back3)
	_, _ = e.svc.Return(ctx, u3.Query())
	if fmt.Sprint(e.attemptStatuses(ref3)) != "[failed]" {
		t.Fatalf("attempts %v", e.attemptStatuses(ref3))
	}
	e.assertBooks()
}

// Return and reconciliation racing on the same payment capture and sell once.
func TestReturnAndReconcileRaceSettleOnce(t *testing.T) {
	e := setup(t)
	for round := range 5 {
		buyer, ref := e.reserve()
		redirect := e.checkout(buyer, ref)
		back, _ := e.gw.Decide(ctx, providerRef(redirect), "approve")
		u, _ := url.Parse(back)
		var wg sync.WaitGroup
		for i := range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if i%2 == 0 {
					_, _ = e.svc.Return(ctx, u.Query())
				} else {
					_, _ = e.svc.Reconcile(ctx, 0, 10)
				}
			}()
		}
		wg.Wait()
		if e.orderStatus(ref) != "paid" {
			t.Fatalf("round %d: order %s", round, e.orderStatus(ref))
		}
		if l := e.assertBooks(); l.RefundOpenMinor != 0 {
			t.Fatalf("round %d: settled twice: %+v", round, l)
		}
	}
}

func TestEveryFailurePointKeepsTheBooksBalanced(t *testing.T) {
	e := setup(t)
	use := func(db dbx.DB) *payments.Service {
		return &payments.Service{DB: db, Gateway: &fakegw.Gateway{DB: e.pool, BaseURL: "http://x"}, ReturnURL: e.svc.ReturnURL}
	}
	books := func(int, error) { e.assertBooks() }

	pgtest.EachFault(t, e.pool, func(db dbx.DB) error {
		buyer, ref := e.reserve()
		_, err := use(db).Checkout(ctx, buyer, ref)
		return err
	}, books)

	pgtest.EachFault(t, e.pool, func(db dbx.DB) error {
		buyer, ref := e.reserve()
		back, _ := e.gw.Decide(ctx, providerRef(e.checkout(buyer, ref)), "approve")
		u, _ := url.Parse(back)
		_, err := use(db).Return(ctx, u.Query())
		return err
	}, books)

	pgtest.EachFault(t, e.pool, func(db dbx.DB) error {
		buyer, ref := e.reserve()
		_, _ = e.gw.Decide(ctx, providerRef(e.checkout(buyer, ref)), "decline")
		_, err := use(db).Reconcile(ctx, 0, 1)
		return err
	}, books)

	pgtest.EachFault(t, e.pool, func(db dbx.DB) error {
		buyer, ref := e.reserve()
		e.svc.Gateway = &expiringGateway{Gateway: e.gw, pool: e.pool, ref: ref}
		e.pay(e.checkout(buyer, ref), "approve")
		_, err := use(db).ProcessRefunds(ctx, 10)
		return err
	}, books)

	// A provider outage at Start while the database also fails.
	pgtest.EachFault(t, e.pool, func(db dbx.DB) error {
		buyer, ref := e.reserve()
		svc := use(db)
		svc.Gateway.(*fakegw.Gateway).FailNext("start", errors.New("refused"))
		_, err := svc.Checkout(ctx, buyer, ref)
		if errors.Is(err, payments.ErrGatewayUnavailable) {
			return nil
		}
		return err
	}, books)

	// A late capture (hold expires during Complete) under every failure point.
	pgtest.EachFault(t, e.pool, func(db dbx.DB) error {
		buyer, ref := e.reserve()
		back, _ := e.gw.Decide(ctx, providerRef(e.checkout(buyer, ref)), "approve")
		u, _ := url.Parse(back)
		svc := use(db)
		svc.Gateway = &expiringGateway{Gateway: svc.Gateway.(*fakegw.Gateway), pool: e.pool, ref: ref}
		_, err := svc.Return(ctx, u.Query())
		return err
	}, books)

	// Reconciling an open, in-time session under every failure point.
	_, _ = e.pool.Exec(ctx, `UPDATE payment_attempt SET status = 'failed' WHERE status IN ('starting', 'pending')`)
	buyer, ref := e.reserve()
	e.checkout(buyer, ref)
	pgtest.EachFault(t, e.pool, func(db dbx.DB) error {
		_, err := use(db).Reconcile(ctx, 0, 10)
		return err
	}, books)

	for _, op := range []func(db dbx.DB) error{
		func(db dbx.DB) error { _, err := payments.GetLedger(ctx, db); return err },
		func(db dbx.DB) error {
			_, err := payments.Timeline(ctx, db, "00000000-0000-0000-0000-000000000000")
			return err
		},
		func(db dbx.DB) error {
			_, err := payments.LatestStatus(ctx, db, "00000000-0000-0000-0000-000000000000")
			return err
		},
	} {
		pgtest.EachFault(t, e.pool, op, func(int, error) {})
	}
}
