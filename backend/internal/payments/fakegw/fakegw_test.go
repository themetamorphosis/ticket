package fakegw

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/payments"
	"kacheem/backend/internal/pgtest"
)

var ctx = context.Background()

func newGateway(t *testing.T) *Gateway {
	t.Helper()
	return &Gateway{DB: pgtest.Pool(t), BaseURL: "http://localhost:8088"}
}

func start(t *testing.T, g *Gateway) payments.Started {
	t.Helper()
	s, err := g.Start(ctx, payments.StartRequest{MerchantRef: "KC-1", AmountMinor: 155000, Currency: "BDT",
		ReturnURL: "http://localhost:8088/payments/return/fake?x=1"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestApproveThenCompleteCapturesOnce(t *testing.T) {
	g := newGateway(t)
	if g.Provider() != "fake" || !g.CapturesOnComplete() {
		t.Fatal("identity")
	}
	s := start(t, g)
	if !strings.HasPrefix(s.RedirectURL, "http://localhost:8088/payments/fake/pay/FAKE-") {
		t.Fatalf("redirect %s", s.RedirectURL)
	}
	if r, _ := g.Query(ctx, s.ProviderRef); r.Status != payments.Pending {
		t.Fatalf("before approval: %s", r.Status)
	}
	back, err := g.Decide(ctx, s.ProviderRef, "approve")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(back)
	if u.Query().Get("ref") != s.ProviderRef || u.Query().Get("status") != "success" || u.Query().Get("x") != "1" {
		t.Fatalf("return %s", back)
	}
	ref, outcome, err := g.ParseReturn(u.Query())
	if err != nil || ref != s.ProviderRef || outcome != payments.OutcomeSuccess {
		t.Fatalf("parse %s %s %v", ref, outcome, err)
	}
	if r, _ := g.Query(ctx, s.ProviderRef); r.Status != payments.Authorized {
		t.Fatalf("approved: %s", r.Status)
	}
	first, err := g.Complete(ctx, s.ProviderRef)
	if err != nil || first.Status != payments.Succeeded || first.TrxID == "" || first.AmountMinor != 155000 ||
		first.Currency != "BDT" || first.MerchantRef != "KC-1" {
		t.Fatalf("complete %+v %v", first, err)
	}
	again, _ := g.Complete(ctx, s.ProviderRef)
	if again.TrxID != first.TrxID || again.Status != payments.Succeeded {
		t.Fatalf("second complete %+v", again)
	}
	if _, err := g.Decide(ctx, s.ProviderRef, "cancel"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("decide after capture: %v", err)
	}
}

func TestCompleteWithoutApprovalClosesThePayment(t *testing.T) {
	g := newGateway(t)
	for _, action := range []string{"", "decline", "cancel"} {
		s := start(t, g)
		if action != "" {
			if _, err := g.Decide(ctx, s.ProviderRef, action); err != nil {
				t.Fatal(err)
			}
			if r, _ := g.Query(ctx, s.ProviderRef); r.Status != payments.Failed {
				t.Fatalf("%s: %s", action, r.Status)
			}
		}
		if r, _ := g.Complete(ctx, s.ProviderRef); r.Status != payments.Failed {
			t.Fatalf("%q complete: %s", action, r.Status)
		}
		// Approving after a completion attempt is too late.
		if _, err := g.Decide(ctx, s.ProviderRef, "approve"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%q approve later: %v", action, err)
		}
	}
}

func TestRefundsAreIdempotentAndBounded(t *testing.T) {
	g := newGateway(t)
	s := start(t, g)
	_, _ = g.Decide(ctx, s.ProviderRef, "approve")
	paid, _ := g.Complete(ctx, s.ProviderRef)
	req := payments.RefundRequest{RefundRef: "RF-1", ProviderRef: s.ProviderRef, TrxID: paid.TrxID, AmountMinor: 155000, Currency: "BDT"}
	first, err := g.Refund(ctx, req)
	if err != nil || first.Status != payments.RefundCompleted || first.ProviderRefundID == "" {
		t.Fatalf("refund %+v %v", first, err)
	}
	again, _ := g.Refund(ctx, req)
	if again != first {
		t.Fatalf("retry %+v != %+v", again, first)
	}
	more := req
	more.RefundRef = "RF-2"
	if r, _ := g.Refund(ctx, more); r.Status != payments.RefundFailed {
		t.Fatalf("over-refund %+v", r)
	}
	wrongTrx := more
	wrongTrx.TrxID, wrongTrx.AmountMinor = "nope", 1
	if r, _ := g.Refund(ctx, wrongTrx); r.Status != payments.RefundFailed {
		t.Fatalf("wrong trx %+v", r)
	}
	missing := more
	missing.ProviderRef = "FAKE-missing"
	if _, err := g.Refund(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing payment %v", err)
	}
}

func TestUnknownReferencesAndBadInput(t *testing.T) {
	g := newGateway(t)
	if _, err := g.Query(ctx, "FAKE-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := g.Complete(ctx, "FAKE-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := g.Decide(ctx, "FAKE-missing", "approve"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := g.Decide(ctx, "FAKE-missing", "steal"); err == nil {
		t.Fatal("unknown action accepted")
	}
	for _, q := range []url.Values{{}, {"ref": {"x"}, "status": {"maybe"}}} {
		if _, _, err := g.ParseReturn(q); err == nil {
			t.Errorf("ParseReturn(%v) accepted", q)
		}
	}
}

func TestFailNextInjectsOneFailurePerOperation(t *testing.T) {
	g := newGateway(t)
	boom := errors.New("timeout")
	s := start(t, g)
	calls := map[string]func() error{
		"start": func() error {
			_, err := g.Start(ctx, payments.StartRequest{MerchantRef: "KC-2", Currency: "BDT"})
			return err
		},
		"complete": func() error { _, err := g.Complete(ctx, s.ProviderRef); return err },
		"query":    func() error { _, err := g.Query(ctx, s.ProviderRef); return err },
		"refund": func() error {
			_, err := g.Refund(ctx, payments.RefundRequest{RefundRef: "RF-x", ProviderRef: s.ProviderRef})
			return err
		},
	}
	for op, call := range calls {
		g.FailNext(op, boom)
		if err := call(); !errors.Is(err, boom) {
			t.Errorf("%s: err = %v", op, err)
		}
		if err := call(); errors.Is(err, boom) {
			t.Errorf("%s: failure injected twice", op)
		}
	}
}

func TestDatabaseFailuresSurface(t *testing.T) {
	pool := pgtest.Pool(t)
	g := &Gateway{DB: pool, BaseURL: "http://x"}
	s := start(t, g)
	_, _ = g.Decide(ctx, s.ProviderRef, "approve")
	paid, _ := g.Complete(ctx, s.ProviderRef)
	n := 0
	ops := []func(db dbx.DB) error{
		func(db dbx.DB) error {
			_, err := (&Gateway{DB: db}).Start(ctx, payments.StartRequest{MerchantRef: "KC-3", Currency: "BDT"})
			return err
		},
		func(db dbx.DB) error { _, err := (&Gateway{DB: db}).Complete(ctx, s.ProviderRef); return err },
		func(db dbx.DB) error { _, err := (&Gateway{DB: db}).Query(ctx, s.ProviderRef); return err },
		func(db dbx.DB) error {
			n++
			_, err := (&Gateway{DB: db}).Refund(ctx, payments.RefundRequest{RefundRef: "RF-f" + string(rune('a'+n)),
				ProviderRef: s.ProviderRef, TrxID: paid.TrxID, AmountMinor: 1, Currency: "BDT"})
			return err
		},
		func(db dbx.DB) error {
			s2 := start(t, g)
			_, err := (&Gateway{DB: db}).Decide(ctx, s2.ProviderRef, "approve")
			return err
		},
	}
	for _, op := range ops {
		pgtest.EachFault(t, pool, op, func(int, error) {})
	}
}

func TestHostedPage(t *testing.T) {
	g := newGateway(t)
	mux := http.NewServeMux()
	mux.Handle("/payments/fake/pay/{ref}", g)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	s := start(t, g)
	resp, err := client.Get(srv.URL + "/payments/fake/pay/" + s.ProviderRef)
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body[:n]), "1550.00 BDT") || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("page %d %s", resp.StatusCode, body[:n])
	}
	resp, _ = client.PostForm(srv.URL+"/payments/fake/pay/"+s.ProviderRef, url.Values{"action": {"approve"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Location"), "status=success") {
		t.Fatalf("approve %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	// Once decided, the page is gone and a second decision is refused.
	resp, _ = client.Get(srv.URL + "/payments/fake/pay/" + s.ProviderRef)
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("decided page %d", resp.StatusCode)
	}
	resp, _ = client.PostForm(srv.URL+"/payments/fake/pay/"+s.ProviderRef, url.Values{"action": {"cancel"}})
	resp.Body.Close()
	if resp.StatusCode != 409 {
		t.Fatalf("second decision %d", resp.StatusCode)
	}
}
