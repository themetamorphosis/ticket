package bkash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/payments"
	"kacheem/backend/internal/pgtest"
)

var ctx = context.Background()

type reply struct {
	status int
	body   string
}

type request struct {
	path   string
	header http.Header
	body   map[string]string
}

// fakeBkash answers token calls itself and plays scripted replies for the rest.
type fakeBkash struct {
	mu        sync.Mutex
	grants    int
	refreshes int
	expiresIn string
	tokenFail string
	replies   map[string][]reply
	requests  []request
}

func (f *fakeBkash) script(path string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[path] = append(f.replies[path], reply{status, body})
}

func (f *fakeBkash) last(path string) request {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.requests) - 1; i >= 0; i-- {
		if f.requests[i].path == path {
			return f.requests[i]
		}
	}
	return request{}
}

func (f *fakeBkash) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]string
	_ = json.Unmarshal(raw, &body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request{r.URL.Path, r.Header.Clone(), body})
	switch r.URL.Path {
	case "/auth/grant-token", "/auth/refresh-token":
		if f.tokenFail != "" {
			fmt.Fprint(w, f.tokenFail)
			return
		}
		if r.URL.Path == "/auth/grant-token" {
			f.grants++
		} else {
			f.refreshes++
		}
		fmt.Fprintf(w, `{"statusCode":"0000","statusMessage":"Successful","token_type":"Bearer",
			"id_token":"tok-%d-%d","refresh_token":"rt-%d","expires_in":%s}`, f.grants, f.refreshes, f.grants, f.expiresIn)
		return
	}
	q := f.replies[r.URL.Path]
	if len(q) == 0 {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	f.replies[r.URL.Path] = q[1:]
	w.WriteHeader(q[0].status)
	fmt.Fprint(w, q[0].body)
}

type harness struct {
	f    *fakeBkash
	srv  *httptest.Server
	pool *pgxpool.Pool
	g    *Gateway
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	f := &fakeBkash{expiresIn: "3600", replies: map[string][]reply{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	pool := pgtest.Pool(t)
	cfg := Config{BaseURL: srv.URL, Username: "merchant", Password: "secret", AppKey: "app-key", AppSecret: "app-secret"}
	return &harness{f: f, srv: srv, pool: pool, g: New(cfg, pool, HTTPClient())}
}

const completed = `{"paymentId":"P1","trxId":"TRX1","transactionStatus":"Completed","amount":"3100.00","currency":"BDT","merchantInvoiceNumber":"KC-1"}`

func TestTokenIsGrantedOnceAndSharedAcrossProcesses(t *testing.T) {
	h := newHarness(t)
	other := New(h.g.cfg, h.pool, HTTPClient())
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g := h.g
			if i%2 == 1 {
				g = other
			}
			if _, err := g.token(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if h.f.grants != 1 {
		t.Fatalf("%d grants; bKash allows two per hour", h.f.grants)
	}
	grant := h.f.last("/auth/grant-token")
	if grant.header.Get("username") != "merchant" || grant.header.Get("password") != "secret" ||
		grant.body["app_key"] != "app-key" || grant.body["app_secret"] != "app-secret" {
		t.Fatalf("grant request %+v", grant)
	}
}

func TestTokenRefreshesThenRegrants(t *testing.T) {
	h := newHarness(t)
	h.f.expiresIn = `"3600"` // documented as a string
	tok, _ := h.g.token(ctx)
	_, _ = h.pool.Exec(ctx, `UPDATE gateway_token SET access_expires_at = now()`)
	refreshed, err := h.g.token(ctx)
	if err != nil || refreshed == tok || h.f.refreshes != 1 || h.f.last("/auth/refresh-token").body["refresh_token"] != "rt-1" {
		t.Fatalf("refresh: %s %v refreshes=%d", refreshed, err, h.f.refreshes)
	}
	var keepsWindow bool
	_ = h.pool.QueryRow(ctx, `SELECT refresh_expires_at < now() + interval '29 days' FROM gateway_token`).Scan(&keepsWindow)
	if !keepsWindow {
		t.Fatal("refresh extended the refresh-token window")
	}
	_, _ = h.pool.Exec(ctx, `UPDATE gateway_token SET access_expires_at = now(), refresh_expires_at = now()`)
	if _, err := h.g.token(ctx); err != nil || h.f.grants != 2 {
		t.Fatalf("regrant: %v grants=%d", err, h.f.grants)
	}
}

func TestTokenFailures(t *testing.T) {
	h := newHarness(t)
	h.f.tokenFail = `{"statusCode":"9999","statusMessage":"Invalid or unrecognized access credentials"}`
	_, err := h.g.token(ctx)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "9999" {
		t.Fatalf("err = %v", err)
	}
	// Through a payment call, a token failure is "unknown", never a refusal.
	if _, err := h.g.Query(ctx, "P1"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("query err = %v", err)
	}
	h.f.tokenFail = `{"statusCode":"0000","expires_in":"soon"}`
	if _, err := h.g.token(ctx); err == nil {
		t.Fatal("malformed expires_in accepted")
	}
	h.f.tokenFail = ""
	pgtest.EachFault(t, h.pool, func(db dbx.DB) error {
		_, _ = h.pool.Exec(ctx, `DELETE FROM gateway_token`)
		_, err := New(h.g.cfg, db, HTTPClient()).token(ctx)
		return err
	}, func(int, error) {})
	// Refresh path under faults.
	pgtest.EachFault(t, h.pool, func(db dbx.DB) error {
		_, _ = h.pool.Exec(ctx, `UPDATE gateway_token SET access_expires_at = now()`)
		_, err := New(h.g.cfg, db, HTTPClient()).token(ctx)
		return err
	}, func(int, error) {})
	// A process that finds a fresh token after waiting for the lock uses it.
	_, _ = h.pool.Exec(ctx, `UPDATE gateway_token SET access_expires_at = now()`)
	tx, _ := h.pool.Begin(ctx)
	_, _ = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('gateway_token:bkash'))`)
	done := make(chan string)
	go func() { tok, _ := h.g.token(ctx); done <- tok }()
	time.Sleep(200 * time.Millisecond)
	_, _ = tx.Exec(ctx, `UPDATE gateway_token SET access_token = 'renewed-elsewhere', access_expires_at = now() + interval '1 hour'`)
	_ = tx.Commit(ctx)
	if tok := <-done; tok != "renewed-elsewhere" {
		t.Fatalf("token %q", tok)
	}
}

func TestStartCreatesAPayment(t *testing.T) {
	h := newHarness(t)
	h.f.script("/payment/create", 200, `{"paymentId":"P1","bkashURL":"https://sandbox.payment.bkash.com/?paymentId=P1","transactionStatus":"Initiated"}`)
	s, err := h.g.Start(ctx, payments.StartRequest{MerchantRef: "KC-1", AmountMinor: 310000, Currency: "BDT",
		ReturnURL: "https://tickets.test/payments/return/bkash", Phone: "+8801711000000"})
	if err != nil || s.ProviderRef != "P1" || !strings.Contains(s.RedirectURL, "paymentId=P1") {
		t.Fatalf("start %+v %v", s, err)
	}
	r := h.f.last("/payment/create")
	want := map[string]string{"mode": "0011", "payerReference": "01711000000", "callbackURL": "https://tickets.test/payments/return/bkash",
		"amount": "3100.00", "currency": "BDT", "intent": "sale", "merchantInvoiceNumber": "KC-1"}
	for k, v := range want {
		if r.body[k] != v {
			t.Errorf("%s = %q, want %q", k, r.body[k], v)
		}
	}
	if r.header.Get("Authorization") != "tok-1-0" || r.header.Get("X-App-Key") != "app-key" {
		t.Fatalf("headers %v", r.header)
	}

	// A non-Bangladeshi number cannot prefill the wallet.
	h.f.script("/payment/create", 200, `{"paymentID":"P2","bkashURL":"u"}`)
	if _, err := h.g.Start(ctx, payments.StartRequest{MerchantRef: "KC-2", Phone: "+441234567890"}); err != nil {
		t.Fatal(err)
	}
	if h.f.last("/payment/create").body["payerReference"] != "KC-2" {
		t.Fatal("payerReference fallback")
	}
	h.f.script("/payment/create", 200, `{"statusMessage":"odd"}`)
	if _, err := h.g.Start(ctx, payments.StartRequest{}); !errors.Is(err, ErrUnknown) {
		t.Fatalf("empty create: %v", err)
	}
	h.f.script("/payment/create", 400, `{"externalCode":"2007","errorMessageEn":"Invalid currency"}`)
	var apiErr *APIError
	if _, err := h.g.Start(ctx, payments.StartRequest{}); !errors.As(err, &apiErr) || apiErr.Code != "2007" || apiErr.Error() != "bkash: 2007 Invalid currency" {
		t.Fatalf("api error: %v", err)
	}
}

func TestParseReturn(t *testing.T) {
	g := &Gateway{}
	if g.Provider() != "bkash" || !g.CapturesOnComplete() {
		t.Fatal("identity")
	}
	ref, outcome, err := g.ParseReturn(url.Values{"paymentID": {"P1"}, "status": {"success"}, "signature": {"x"}})
	if err != nil || ref != "P1" || outcome != payments.OutcomeSuccess {
		t.Fatalf("%s %s %v", ref, outcome, err)
	}
	for _, q := range []url.Values{{"status": {"success"}}, {"paymentID": {"P1"}, "status": {"paid"}}} {
		if _, _, err := g.ParseReturn(q); err == nil {
			t.Errorf("accepted %v", q)
		}
	}
}

func TestCompleteExecutesOnceAndFallsBackToQuery(t *testing.T) {
	h := newHarness(t)
	h.f.script("/payment/execute", 200, completed)
	res, err := h.g.Complete(ctx, "P1")
	if err != nil || res.Status != payments.Succeeded || res.TrxID != "TRX1" || res.AmountMinor != 310000 || res.MerchantRef != "KC-1" {
		t.Fatalf("execute %+v %v", res, err)
	}
	if h.f.last("/payment/execute").body["paymentId"] != "P1" {
		t.Fatal("execute body")
	}

	// Already completed by an earlier, lost response: Query confirms it.
	h.f.script("/payment/execute", 400, `{"externalCode":"2062","errorMessageEn":"The payment has already been completed"}`)
	h.f.script("/query/payment", 200, `{"paymentId":"P1","trxId":"TRX1","transactionStatus":"Completed","amount":"3100","currency":"BDT","merchantInvoice":"KC-1"}`)
	if res, _ := h.g.Complete(ctx, "P1"); res.Status != payments.Succeeded || res.AmountMinor != 310000 || res.MerchantRef != "KC-1" {
		t.Fatalf("2062 %+v", res)
	}

	// Execute refused (wrong PIN etc.): the paymentId is spent, so it failed.
	h.f.script("/payment/execute", 400, `{"externalCode":"2014","errorMessageEn":"Wrong PIN"}`)
	h.f.script("/query/payment", 200, `{"paymentId":"P1","transactionStatus":"Initiated","verificationStatus":"Complete"}`)
	if res, _ := h.g.Complete(ctx, "P1"); res.Status != payments.Failed {
		t.Fatalf("refused %+v", res)
	}

	// Execute timed out and Query is unreachable: unknown, try again later.
	h.f.script("/payment/execute", 503, ``)
	if _, err := h.g.Complete(ctx, "P1"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown: %v", err)
	}

	// A Completed answer with an unreadable amount is not trusted.
	h.f.script("/payment/execute", 200, `{"transactionStatus":"Completed","amount":"31.000"}`)
	if _, err := h.g.Complete(ctx, "P1"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("bad amount: %v", err)
	}
}

func TestQueryMapsStatuses(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		status int
		body   string
		want   payments.Status
	}{
		{200, `{"transactionStatus":"Initiated","verificationStatus":"Incomplete"}`, payments.Pending},
		{200, `{"transactionStatus":"Initiated","verificationStatus":"Complete"}`, payments.Authorized},
		{200, `{"transactionStatus":"Cancelled"}`, payments.Failed},
		{400, `{"externalCode":"2002","errorMessageEn":"Invalid Payment ID"}`, payments.Failed},
	}
	for _, c := range cases {
		h.f.script("/query/payment", c.status, c.body)
		if res, err := h.g.Query(ctx, "P1"); err != nil || res.Status != c.want {
			t.Errorf("%s: %+v %v", c.body, res, err)
		}
	}
	h.f.script("/query/payment", 400, `{"externalCode":"2033","errorMessageEn":"Transaction not found"}`)
	var apiErr *APIError
	if _, err := h.g.Query(ctx, "P1"); !errors.As(err, &apiErr) {
		t.Fatalf("other API error: %v", err)
	}
	h.f.script("/query/payment", 200, `not json`)
	if _, err := h.g.Query(ctx, "P1"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("garbage: %v", err)
	}
	h.f.script("/query/payment", 404, `{}`)
	if _, err := h.g.Query(ctx, "P1"); !errors.As(err, &apiErr) || apiErr.Code != "404" {
		t.Fatalf("404: %v", err)
	}
}

func TestNetworkErrorsAreRedactedAndUnknown(t *testing.T) {
	h := newHarness(t)
	_, _ = h.g.token(ctx)
	h.srv.Close()
	_, err := h.g.Query(ctx, "P1")
	if !errors.Is(err, ErrUnknown) || strings.Contains(err.Error(), "?") {
		t.Fatalf("err = %v", err)
	}
	if got := redact(errors.New("plain")); got != "plain" {
		t.Fatalf("redact %q", got)
	}
	if got := redact(&url.Error{Op: "Post", URL: "https://x/y?secret=1", Err: errors.New("eof")}); got != "Post https://x/y: eof" {
		t.Fatalf("redact %q", got)
	}
}

func TestRefund(t *testing.T) {
	h := newHarness(t)
	req := payments.RefundRequest{RefundRef: "RF-1", ProviderRef: "P1", TrxID: "TRX1", AmountMinor: 310000, Currency: "BDT", Reason: "late_payment"}

	// No refund yet (status answers with an error), then the refund completes.
	h.f.script("/refund/payment/status", 400, `{"externalCode":"3045","errorMessageEn":"Invalid TrxID"}`)
	h.f.script("/refund/payment/transaction", 200, `{"refundTrxId":"RTRX1","refundTransactionStatus":"Completed"}`)
	res, err := h.g.Refund(ctx, req)
	if err != nil || res.Status != payments.RefundCompleted || res.ProviderRefundID != "RTRX1" {
		t.Fatalf("refund %+v %v", res, err)
	}
	r := h.f.last("/refund/payment/transaction")
	if r.body["refundAmount"] != "3100.00" || r.body["trxId"] != "TRX1" || r.body["paymentId"] != "P1" ||
		r.body["sku"] != "ticket" || !strings.Contains(r.body["reason"], "expired") {
		t.Fatalf("refund body %v", r.body)
	}

	// A retry after a lost response finds the completed refund and stops.
	h.f.script("/refund/payment/status", 200, `{"refundTransactions":[
		{"refundTrxId":"RX0","refundTransactionStatus":"Completed","refundAmount":"10.00"},
		{"refundTrxId":"RTRX1","refundTransactionStatus":"Completed","refundAmount":"3100.00"}]}`)
	if res, _ := h.g.Refund(ctx, req); res.ProviderRefundID != "RTRX1" {
		t.Fatalf("idempotent %+v", res)
	}

	cases := []struct {
		status int
		body   string
		want   payments.RefundStatus
	}{
		{400, `{"externalCode":"2023","errorMessageEn":"Insufficient Balance"}`, payments.RefundPending},
		{400, `{"externalCode":"2071","errorMessageEn":"Refund after 60 days not allowed"}`, payments.RefundFailed},
		{200, `{"refundTrxId":"R","refundTransactionStatus":"Processing"}`, payments.RefundPending},
	}
	for _, c := range cases {
		h.f.script("/refund/payment/status", 200, `{"refundTransactions":[]}`)
		h.f.script("/refund/payment/transaction", c.status, c.body)
		other := req
		other.Reason = "something_else"
		if res, err := h.g.Refund(ctx, other); err != nil || res.Status != c.want || (c.status == 400 && res.Detail == "") {
			t.Errorf("%s: %+v %v", c.body, res, err)
		}
	}
	if !strings.Contains(h.f.last("/refund/payment/transaction").body["reason"], "Ticket order refund") {
		t.Fatal("default reason")
	}

	// Unknown outcomes are reported as errors so the refund is retried.
	h.f.script("/refund/payment/status", 503, ``)
	if _, err := h.g.Refund(ctx, req); !errors.Is(err, ErrUnknown) {
		t.Fatalf("status unknown: %v", err)
	}
	h.f.script("/refund/payment/status", 200, `{}`)
	h.f.script("/refund/payment/transaction", 503, ``)
	if _, err := h.g.Refund(ctx, req); !errors.Is(err, ErrUnknown) {
		t.Fatalf("refund unknown: %v", err)
	}
}

func TestAmounts(t *testing.T) {
	for minor, s := range map[int64]string{0: "0.00", 5: "0.05", 310000: "3100.00", 123456: "1234.56"} {
		if got := formatAmount(minor); got != s {
			t.Errorf("formatAmount(%d) = %s", minor, got)
		}
	}
	for s, minor := range map[string]int64{"20": 2000, "20.5": 2050, "20.00": 2000, "2.90": 290} {
		if got, err := parseAmount(s); err != nil || got != minor {
			t.Errorf("parseAmount(%s) = %d, %v", s, got, err)
		}
	}
	for _, s := range []string{"x", "1.234", "1.x", ""} {
		if _, err := parseAmount(s); err == nil {
			t.Errorf("parseAmount(%q) accepted", s)
		}
	}
}
