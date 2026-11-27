package paystation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"kacheem/backend/internal/payments"
)

var ctx = context.Background()

// fakePayStation replays scripted replies and records requests. Reply shapes
// were confirmed against the PayStation sandbox.
type fakePayStation struct {
	mu       sync.Mutex
	replies  map[string][]reply
	requests []recorded
}

type reply struct {
	status int
	body   string
}

type recorded struct {
	path   string
	header http.Header
	form   url.Values
}

func (f *fakePayStation) script(path string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[path] = append(f.replies[path], reply{status, body})
}

func (f *fakePayStation) last() recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

func (f *fakePayStation) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, recorded{r.URL.Path, r.Header.Clone(), r.PostForm})
	q := f.replies[r.URL.Path]
	if len(q) == 0 {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	f.replies[r.URL.Path] = q[1:]
	w.WriteHeader(q[0].status)
	fmt.Fprint(w, q[0].body)
}

func newGateway(t *testing.T) (*Gateway, *fakePayStation, *httptest.Server) {
	t.Helper()
	f := &fakePayStation{replies: map[string][]reply{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return New(Config{BaseURL: srv.URL, MerchantID: "104-1653730183", Password: "secret"}, HTTPClient()), f, srv
}

func statusBody(trxStatus, trxID, amount string) string {
	return `{"status_code":"200","status":"success","message":"Transaction found","data":{"invoice_number":"KC-1",` +
		`"trx_status":"` + trxStatus + `","trx_id":"` + trxID + `","request_amount":"3100.00","payment_amount":"` + amount + `"}}`
}

func TestStartInitiatesAHostedPayment(t *testing.T) {
	g, f, _ := newGateway(t)
	if g.Provider() != "paystation" || g.CapturesOnComplete() {
		t.Fatal("identity")
	}
	f.script("/initiate-payment", 200, `{"status_code":"200","status":"success","message":"Payment Link Created Successfully.",
		"payment_amount":"3100","invoice_number":"KC-1","payment_url":"https:\/\/sandbox.paystation.com.bd\/checkout\/1\/abc"}`)
	s, err := g.Start(ctx, payments.StartRequest{MerchantRef: "KC-1", AmountMinor: 310000, Currency: "BDT",
		ReturnURL: "https://tickets.test/payments/return/paystation", Name: "Nusrat Jahan", Email: "n@example.com", Phone: "+8801711000000"})
	if err != nil || s.ProviderRef != "KC-1" || s.RedirectURL != "https://sandbox.paystation.com.bd/checkout/1/abc" {
		t.Fatalf("start %+v %v", s, err)
	}
	r := f.last()
	want := map[string]string{"merchantId": "104-1653730183", "password": "secret", "invoice_number": "KC-1", "currency": "BDT",
		"payment_amount": "3100", "pay_with_charge": "0", "cust_name": "Nusrat Jahan", "cust_phone": "01711000000",
		"cust_email": "n@example.com", "callback_url": "https://tickets.test/payments/return/paystation"}
	for k, v := range want {
		if r.form.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, r.form.Get(k), v)
		}
	}
	if r.path != "/initiate-payment" || r.header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		t.Fatalf("request %+v", r)
	}

	// Paisa amounts keep two decimals; foreign numbers are sent unchanged.
	f.script("/initiate-payment", 200, `{"status_code":"200","payment_url":"u"}`)
	_, _ = g.Start(ctx, payments.StartRequest{MerchantRef: "KC-2", AmountMinor: 155050, Phone: "+441234567890"})
	if r := f.last(); r.form.Get("payment_amount") != "1550.50" || r.form.Get("cust_phone") != "+441234567890" {
		t.Fatalf("form %v", r.form)
	}

	f.script("/initiate-payment", 200, `{"status_code":"1008","status":"failed","message":"Duplicate invoice number."}`)
	var apiErr *APIError
	if _, err := g.Start(ctx, payments.StartRequest{}); !errors.As(err, &apiErr) || apiErr.Code != "1008" ||
		apiErr.Error() != "paystation: 1008 Duplicate invoice number." {
		t.Fatalf("duplicate: %v", err)
	}
	f.script("/initiate-payment", 200, `{"status_code":"200"}`)
	if _, err := g.Start(ctx, payments.StartRequest{}); !errors.Is(err, ErrUnknown) {
		t.Fatalf("no url: %v", err)
	}
}

func TestParseReturn(t *testing.T) {
	g := &Gateway{}
	cases := map[string]payments.Outcome{
		"Successful": payments.OutcomeSuccess, "success": payments.OutcomeSuccess,
		"canceled": payments.OutcomeCancel, "Cancelled": payments.OutcomeCancel, "failed": payments.OutcomeFailure,
	}
	for status, want := range cases {
		ref, got, err := g.ParseReturn(url.Values{"status": {status}, "invoice_number": {"KC-1"}, "trx_id": {"T"}})
		if err != nil || ref != "KC-1" || got != want {
			t.Errorf("%s: %s %s %v", status, ref, got, err)
		}
	}
	for _, q := range []url.Values{{"status": {"success"}}, {"invoice_number": {"KC-1"}}} {
		if _, _, err := g.ParseReturn(q); err == nil {
			t.Errorf("accepted %v", q)
		}
	}
}

func TestQueryAndCompleteMapStatuses(t *testing.T) {
	g, f, _ := newGateway(t)
	f.script("/transaction-status", 200, statusBody("Success", "TRX1", "3100.00"))
	res, err := g.Complete(ctx, "KC-1")
	if err != nil || res.Status != payments.Succeeded || res.TrxID != "TRX1" || res.AmountMinor != 310000 ||
		res.Currency != "BDT" || res.MerchantRef != "KC-1" {
		t.Fatalf("success %+v %v", res, err)
	}
	r := f.last()
	if r.header.Get("merchantId") != "104-1653730183" || r.form.Get("invoice_number") != "KC-1" {
		t.Fatalf("status request %+v", r)
	}
	cases := []struct {
		trx  string
		want payments.Status
	}{
		{"successful", payments.Succeeded},
		{"processing", payments.Pending},
		{"something_new", payments.Pending},
		{"Failed", payments.Failed},
		{"canceled", payments.Failed},
		{"refund", payments.Failed},
	}
	for _, c := range cases {
		f.script("/transaction-status", 200, statusBody(c.trx, "TRX1", "3100"))
		if res, err := g.Query(ctx, "KC-1"); err != nil || res.Status != c.want {
			t.Errorf("%s: %+v %v", c.trx, res, err)
		}
	}
	f.script("/transaction-status", 200, `{"status_code":"2001","status":"failed","message":"Transaction not found in system"}`)
	if res, err := g.Query(ctx, "KC-1"); err != nil || res.Status != payments.Pending {
		t.Fatalf("not found: %+v %v", res, err)
	}
	f.script("/transaction-status", 200, `{"status_code":"4001","message":"Invalid merchant"}`)
	var apiErr *APIError
	if _, err := g.Query(ctx, "KC-1"); !errors.As(err, &apiErr) {
		t.Fatalf("api error: %v", err)
	}
	f.script("/transaction-status", 200, statusBody("success", "TRX1", "31.000"))
	if _, err := g.Query(ctx, "KC-1"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("bad amount: %v", err)
	}
	f.script("/transaction-status", 200, `<html>maintenance</html>`)
	if _, err := g.Query(ctx, "KC-1"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("html: %v", err)
	}
	if _, err := g.Query(ctx, "KC-1"); !errors.Is(err, ErrUnknown) { // 502: no reply scripted
		t.Fatalf("502: %v", err)
	}
}

func TestRefundWaitsForTheMerchantPanel(t *testing.T) {
	g, f, _ := newGateway(t)
	req := payments.RefundRequest{RefundRef: "RF-1", ProviderRef: "KC-1", TrxID: "TRX1", AmountMinor: 310000}
	f.script("/transaction-status", 200, statusBody("success", "TRX1", "3100"))
	res, err := g.Refund(ctx, req)
	if err != nil || res.Status != payments.RefundPending || !strings.Contains(res.Detail, "merchant panel") {
		t.Fatalf("before panel refund: %+v %v", res, err)
	}
	f.script("/transaction-status", 200, statusBody("Refunded", "TRX1", "3100"))
	if res, err := g.Refund(ctx, req); err != nil || res.Status != payments.RefundCompleted || res.ProviderRefundID != "panel:TRX1" {
		t.Fatalf("after panel refund: %+v %v", res, err)
	}
	if _, err := g.Refund(ctx, req); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unreachable: %v", err)
	}
}

func TestParseNotification(t *testing.T) {
	g := &Gateway{}
	ref, err := g.ParseNotification([]byte(`{"invoice_number":"KC-1","trx_status":"Success","trx_id":"T","trx_amount":600}`))
	if err != nil || ref != "KC-1" {
		t.Fatalf("%s %v", ref, err)
	}
	for _, body := range []string{`not json`, `{}`} {
		if _, err := g.ParseNotification([]byte(body)); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
}

func TestNetworkFailureIsUnknown(t *testing.T) {
	g, _, srv := newGateway(t)
	srv.Close()
	_, err := g.Query(ctx, "KC-1")
	if !errors.Is(err, ErrUnknown) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("err = %v", err)
	}
}
