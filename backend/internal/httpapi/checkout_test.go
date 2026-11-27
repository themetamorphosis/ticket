package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"

	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/payments"
	"kacheem/backend/internal/payments/fakegw"
	"kacheem/backend/internal/pgtest"
	"kacheem/backend/internal/session"
)

// follow sends a request and returns the redirect target without following it.
func (b *browser) follow(method, rawURL string, form url.Values) (int, string) {
	b.t.Helper()
	client := *b.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var resp *http.Response
	var err error
	if method == http.MethodPost {
		resp, err = client.PostForm(rawURL, form)
	} else {
		resp, err = client.Get(rawURL)
	}
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Location")
}

func TestBuyerPaysThroughTheHostedGateway(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live"})
	srv, _, _ := newPaymentServer(t, pool, pool)
	b := newBrowser(t, srv)
	b.startSession()
	ref := b.write("/api/v1/orders", booking(2), "key-00000001").body["ref"].(string)

	if r := b.do("GET", "/api/v1/orders/"+ref, nil, nil); r.body["payment_status"] != nil || r.body["payment_provider"] != "fake" {
		t.Fatalf("before checkout: %v", r.body)
	}
	co := b.write("/api/v1/orders/"+ref+"/checkout", nil, "")
	redirect, _ := co.body["redirect_url"].(string)
	if co.status != 200 || !strings.HasPrefix(redirect, srv.URL+"/payments/fake/pay/FAKE-") {
		t.Fatalf("checkout: %d %v", co.status, co.body)
	}
	if status, _ := b.follow(http.MethodGet, redirect, nil); status != 200 {
		t.Fatalf("hosted page %d", status)
	}
	status, back := b.follow(http.MethodPost, redirect, url.Values{"action": {"approve"}})
	if status != http.StatusSeeOther || !strings.Contains(back, "/payments/return/fake?") {
		t.Fatalf("approve: %d %s", status, back)
	}
	status, target := b.follow(http.MethodGet, back, nil)
	if status != http.StatusSeeOther || target != "/order/?ref="+url.QueryEscape(ref) {
		t.Fatalf("return: %d %s", status, target)
	}

	r := b.do("GET", "/api/v1/orders/"+ref, nil, nil)
	if r.body["status"] != "paid" || r.body["payment_status"] != "succeeded" {
		t.Fatalf("after paying: %v", r.body)
	}
	timeline, _ := r.body["timeline"].([]any)
	var kinds []string
	for _, e := range timeline {
		kinds = append(kinds, e.(map[string]any)["kind"].(string))
	}
	if fmt.Sprint(kinds) != "[order_created payment_started order_paid payment_succeeded]" {
		t.Fatalf("timeline %v", kinds)
	}
	if r := b.write("/api/v1/orders/"+ref+"/checkout", nil, ""); r.status != 409 || r.errCode() != "hold_ended" {
		t.Fatalf("checkout after paying: %d %v", r.status, r.body)
	}
}

func TestCheckoutErrors(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live"})
	srv, logs, gw := newPaymentServer(t, pool, pool)
	b := newBrowser(t, srv)
	b.startSession()
	ref := b.write("/api/v1/orders", booking(1), "key-00000001").body["ref"].(string)

	gw.FailNext("start", errors.New("connection refused"))
	if r := b.write("/api/v1/orders/"+ref+"/checkout", nil, ""); r.status != 503 || r.errCode() != "payment_unavailable" ||
		!strings.Contains(logs.String(), "payment start failed") {
		t.Fatalf("outage: %d %v", r.status, r.body)
	}
	if r := b.write("/api/v1/orders/NOPE0-NOPE0/checkout", nil, ""); r.status != 404 {
		t.Fatalf("unknown order: %d %v", r.status, r.body)
	}
	if r := b.do("POST", "/api/v1/orders/"+ref+"/checkout", nil, nil); r.status != 403 {
		t.Fatalf("unprotected checkout: %d", r.status)
	}
	_, _ = pool.Exec(t.Context(), `UPDATE ticket_order SET expires_at = now() - interval '1 second'`)
	if r := b.write("/api/v1/orders/"+ref+"/checkout", nil, ""); r.status != 409 || r.errCode() != "hold_ended" {
		t.Fatalf("expired: %d %v", r.status, r.body)
	}
}

func TestPaymentReturnEdges(t *testing.T) {
	pool := pgtest.Pool(t)
	srv, logs, _ := newPaymentServer(t, pool, pool)
	b := newBrowser(t, srv)
	if status, target := b.follow(http.MethodGet, srv.URL+"/payments/return/fake?ref=FAKE-nope&status=success", nil); status != 303 || target != "/" {
		t.Fatalf("unknown payment: %d %s", status, target)
	}
	if !strings.Contains(logs.String(), "payment return") {
		t.Fatal("unknown return not logged")
	}
	if r := b.do("GET", "/payments/return/bkash?paymentID=x", nil, nil); r.status != 404 || r.errCode() != "not_found" {
		t.Fatalf("other provider: %d %v", r.status, r.body)
	}
}

func TestFakeGatewayPageIsOnlyMountedWhenConfigured(t *testing.T) {
	h := NewPublic(PublicDeps{Sessions: scs.New(), Logger: slog.New(slog.DiscardHandler)})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/payments/fake/pay/FAKE-1", nil))
	if rec.Code != 404 || !bytes.Contains(rec.Body.Bytes(), []byte("not_found")) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestCheckoutAndPaidOrderUnderDatabaseFailures(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live", Shards: []int32{100, 100}})
	sessions := session.NewBuyer(pool, 0, nil)
	n := 0
	pgtest.EachFault(t, pool, func(db dbx.DB) error {
		var logs bytes.Buffer
		svc, gw := paymentDeps(db, pool)
		srv := httptest.NewTLSServer(NewPublic(PublicDeps{DB: pool, Sessions: sessions, Origin: testOrigin,
			Logger: slog.New(slog.NewJSONHandler(&logs, nil)), Payments: svc, FakeGatewayPage: gw}))
		defer srv.Close()
		gw.BaseURL, svc.ReturnURL = srv.URL, srv.URL+"/payments/return/fake"
		b := newBrowser(t, srv)
		b.startSession()
		n++
		ref, _ := b.write("/api/v1/orders", booking(1), fmt.Sprintf("key-fault-%04d", n)).body["ref"].(string)
		if r := b.write("/api/v1/orders/"+ref+"/checkout", nil, ""); r.status == 500 {
			return errors.New("internal")
		}
		return nil
	}, func(int, error) {})
}

// notifyingFake adds PayStation-style notifications to the fake gateway.
type notifyingFake struct{ *fakegw.Gateway }

func (notifyingFake) ParseNotification(body []byte) (string, error) {
	var n struct {
		Invoice string `json:"invoice_number"`
	}
	if err := json.Unmarshal(body, &n); err != nil || n.Invoice == "" {
		return "", errors.New("bad notification")
	}
	return n.Invoice, nil
}

func TestPaymentNotifications(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live"})
	srv, logs, gw := newPaymentServer(t, pool, pool)
	b := newBrowser(t, srv)

	notify := func(provider, body string) response {
		return b.do("POST", "/payments/notify/"+provider, body, map[string]string{"Content-Type": "application/json"})
	}
	// The fake gateway sends no notifications.
	if r := notify("fake", `{"invoice_number":"x"}`); r.status != 404 || r.errCode() != "unknown_payment" {
		t.Fatalf("unsupported: %d %v", r.status, r.body)
	}
	if r := notify("paystation", `{}`); r.status != 404 || r.errCode() != "not_found" {
		t.Fatalf("other provider: %d %v", r.status, r.body)
	}
	if r := notify("fake", strings.Repeat("x", MaxBodyBytes+1)); r.status != 413 {
		t.Fatalf("oversize: %d %v", r.status, r.body)
	}

	// Swap in a notifying gateway on the same server.
	svc := &payments.Service{DB: pool, Gateway: notifyingFake{gw}, ReturnURL: srv.URL + "/payments/return/fake"}
	h := NewPublic(PublicDeps{DB: pool, Sessions: session.NewBuyer(pool, 0, nil), Origin: testOrigin,
		Logger: slog.New(slog.NewJSONHandler(logs, nil)), Payments: svc, FakeGatewayPage: gw})
	srv2 := httptest.NewTLSServer(h)
	defer srv2.Close()
	gw.BaseURL = srv2.URL
	b2 := newBrowser(t, srv2)
	b2.startSession()
	ref := b2.write("/api/v1/orders", booking(1), "key-00000001").body["ref"].(string)
	redirect := b2.write("/api/v1/orders/"+ref+"/checkout", nil, "").body["redirect_url"].(string)
	pref := redirect[strings.LastIndex(redirect, "/")+1:]
	_, _ = gw.Decide(t.Context(), pref, "approve")
	_, _ = gw.Complete(t.Context(), pref)

	body := `{"invoice_number":"` + pref + `","trx_status":"Success","trx_amount":1550}`
	gw.FailNext("query", errors.New("timeout"))
	if r := b2.do("POST", "/payments/notify/fake", body, nil); r.status != 503 || r.errCode() != "retry" {
		t.Fatalf("retry: %d %v", r.status, r.body)
	}
	if r := b2.do("POST", "/payments/notify/fake", body, nil); r.status != 200 || r.body["status"] != "success" {
		t.Fatalf("notify: %d %v", r.status, r.body)
	}
	if r := b2.do("GET", "/api/v1/orders/"+ref, nil, nil); r.body["status"] != "paid" {
		t.Fatalf("order %v", r.body)
	}
	if r := b2.do("POST", "/payments/notify/fake", `{"invoice_number":"FAKE-unknown"}`, nil); r.status != 404 {
		t.Fatalf("unknown: %d", r.status)
	}
	if !strings.Contains(logs.String(), "payment notification failed") || !strings.Contains(logs.String(), "payment notification rejected") {
		t.Fatalf("logs %s", logs.String())
	}
}
