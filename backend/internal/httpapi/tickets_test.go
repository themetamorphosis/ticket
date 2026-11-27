package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/delivery"
	"kacheem/backend/internal/fulfil"
	"kacheem/backend/internal/payments"
	"kacheem/backend/internal/payments/fakegw"
	"kacheem/backend/internal/pgtest"
	"kacheem/backend/internal/sealbox"
	"kacheem/backend/internal/session"
	"kacheem/backend/internal/tickets"
)

// smsBox captures texts so the test can follow the pickup link.
type smsBox struct {
	mu   sync.Mutex
	last string
}

func (s *smsBox) Send(_ context.Context, _, text string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last = text
	return "sms-1", nil
}

type ticketEnv struct {
	t    *testing.T
	pool *pgxpool.Pool
	srv  *httptest.Server
	gw   *fakegw.Gateway
	f    *fulfil.Fulfiller
	sms  *smsBox
	h    func(db dbx.DB, dev http.Handler) http.Handler
}

func newTicketEnv(t *testing.T) *ticketEnv {
	t.Helper()
	pool := pgtest.Pool(t)
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live", Shards: []int32{50, 50}})
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	box, _ := sealbox.New(k)
	iss := &tickets.Issuer{Box: box}
	sms := &smsBox{}
	e := &ticketEnv{t: t, pool: pool, sms: sms, gw: &fakegw.Gateway{DB: pool},
		f: &fulfil.Fulfiller{DB: pool, Issuer: iss, Mailer: delivery.DevMailer{DB: pool}, SMS: sms,
			PublicOrigin: "https://tickets.test", GateOrigin: "https://gate.test"}}
	sessions := session.NewBuyer(pool, 0, nil)
	e.h = func(db dbx.DB, dev http.Handler) http.Handler {
		svc := &payments.Service{DB: db, Gateway: e.gw, ReturnURL: e.gw.BaseURL + "/payments/return/fake",
			OnPaid: func(ctx context.Context, tx pgx.Tx, id string) error { return iss.Issue(ctx, tx, id) }}
		return NewPublic(PublicDeps{DB: db, Sessions: sessions, Origin: testOrigin, Logger: slog.New(slog.DiscardHandler),
			Payments: svc, FakeGatewayPage: e.gw, DevOutbox: dev})
	}
	// Start first, then build the handler: the return URL needs the address.
	var inner http.Handler
	e.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { inner.ServeHTTP(w, r) }))
	t.Cleanup(e.srv.Close)
	e.gw.BaseURL = e.srv.URL
	inner = e.h(pool, delivery.DevOutbox(pool))
	return e
}

// paid books and pays qty tickets for a new browser and returns the order ref.
func (e *ticketEnv) paid(b *browser, qty int, key string) string {
	e.t.Helper()
	ref := b.write("/api/v1/orders", booking(qty), key).body["ref"].(string)
	redirect := b.write("/api/v1/orders/"+ref+"/checkout", nil, "").body["redirect_url"].(string)
	_, back := b.follow(http.MethodPost, redirect, url.Values{"action": {"approve"}})
	b.follow(http.MethodGet, back, nil)
	return ref
}

func (e *ticketEnv) deliver() {
	e.t.Helper()
	for range 5 {
		if _, err := e.f.Run(context.Background(), 10); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (b *browser) raw(path string) (*http.Response, []byte) {
	b.t.Helper()
	resp, err := b.client.Get(b.srv.URL + path)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

func TestBuyerDownloadsEachTicketOnceDelivered(t *testing.T) {
	e := newTicketEnv(t)
	b := newBrowser(t, e.srv)
	b.startSession()
	ref := e.paid(b, 3, "key-00000001")

	before := b.do("GET", "/api/v1/orders/"+ref, nil, nil)
	ts := before.body["tickets"].([]any)
	if before.body["status"] != "paid" || len(ts) != 3 || ts[0].(map[string]any)["ready"] != false {
		t.Fatalf("before rendering: %v", before.body)
	}
	if resp, _ := b.raw(ts[0].(map[string]any)["pdf_url"].(string)); resp.StatusCode != 404 {
		t.Fatalf("unrendered pdf %d", resp.StatusCode)
	}

	e.deliver()
	after := b.do("GET", "/api/v1/orders/"+ref, nil, nil)
	deliveries := after.body["deliveries"].(map[string]any)
	if deliveries["email"].(map[string]any)["status"] != "sent" || deliveries["sms"].(map[string]any)["status"] != "sent" {
		t.Fatalf("deliveries %v", deliveries)
	}
	seen := map[string]bool{}
	for i, raw := range after.body["tickets"].([]any) {
		tk := raw.(map[string]any)
		resp, pdf := b.raw(tk["pdf_url"].(string))
		want := fmt.Sprintf(`attachment; filename="ticket-%d-of-3.pdf"`, i+1)
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/pdf" || resp.Header.Get("Content-Disposition") != want ||
			resp.Header.Get("Referrer-Policy") != "no-referrer" || resp.Header.Get("Cache-Control") != "no-store" ||
			!bytes.HasPrefix(pdf, []byte("%PDF-")) || tk["ready"] != true || tk["number"] != float64(i+1) {
			t.Fatalf("ticket %d: %d %v", i+1, resp.StatusCode, resp.Header)
		}
		seen[string(pdf)] = true
	}
	if len(seen) != 3 {
		t.Fatal("tickets are not distinct")
	}

	// Another browser cannot fetch them, even with the IDs.
	other := newBrowser(t, e.srv)
	if resp, _ := other.raw(after.body["tickets"].([]any)[0].(map[string]any)["pdf_url"].(string)); resp.StatusCode != 404 {
		t.Fatalf("stranger got %d", resp.StatusCode)
	}
	for _, id := range []string{"not-a-uuid", "00000000-0000-0000-0000-000000000000"} {
		if r := b.do("GET", "/api/v1/tickets/"+id+"/pdf", nil, nil); r.status != 404 || r.errCode() != "ticket_not_found" {
			t.Fatalf("%s: %d", id, r.status)
		}
	}
}

func TestSMSPickupLinkOpensTheTicketsOnAnotherDevice(t *testing.T) {
	e := newTicketEnv(t)
	buyer := newBrowser(t, e.srv)
	buyer.startSession()
	ref := e.paid(buyer, 2, "key-00000001")
	e.deliver()
	m := regexp.MustCompile(`/pickup/#([A-Za-z0-9_-]+)$`).FindStringSubmatch(e.sms.last)
	if m == nil {
		t.Fatalf("sms %q", e.sms.last)
	}

	phone := newBrowser(t, e.srv)
	phone.startSession()
	if r := phone.do("GET", "/api/v1/pickup", nil, nil); r.status != 200 || len(r.body["orders"].([]any)) != 0 {
		t.Fatalf("empty pickup %v", r.body)
	}
	if r := phone.write("/api/v1/pickup/exchange", map[string]string{"secret": "wrong"}, ""); r.status != 404 || r.errCode() != "pickup_invalid" {
		t.Fatalf("wrong secret %d %v", r.status, r.body)
	}
	if r := phone.write("/api/v1/pickup/exchange", `{"secret":1}`, ""); r.status != 400 {
		t.Fatalf("bad json %d", r.status)
	}
	phone.startSession() // the exchange below renews the session token
	r := phone.write("/api/v1/pickup/exchange", map[string]string{"secret": m[1]}, "")
	orders := r.body["orders"].([]any)
	o := orders[0].(map[string]any)
	if r.status != 200 || o["ref"] != ref || len(o["tickets"].([]any)) != 2 || strings.Contains(fmt.Sprint(o), "nusrat@") {
		t.Fatalf("exchange %d %v", r.status, r.body)
	}
	pdfURL := o["tickets"].([]any)[1].(map[string]any)["pdf_url"].(string)
	if resp, _ := phone.raw(pdfURL); resp.StatusCode != 200 || resp.Header.Get("Content-Disposition") != `attachment; filename="ticket-2-of-2.pdf"` {
		t.Fatalf("pickup download %d", resp.StatusCode)
	}
	// After a refresh (the secret is gone from the URL) the list still works.
	if r := phone.do("GET", "/api/v1/pickup", nil, nil); len(r.body["orders"].([]any)) != 1 {
		t.Fatalf("pickup list %v", r.body)
	}
	// The pickup holder cannot read the order with its contact details.
	if r := phone.do("GET", "/api/v1/orders/"+ref, nil, nil); r.status != 404 {
		t.Fatalf("order visible to pickup holder: %d", r.status)
	}
}

func TestResend(t *testing.T) {
	e := newTicketEnv(t)
	b := newBrowser(t, e.srv)
	b.startSession()
	unpaid := b.write("/api/v1/orders", booking(1), "key-00000001").body["ref"].(string)
	if r := b.write("/api/v1/orders/"+unpaid+"/resend", nil, ""); r.status != 409 || r.errCode() != "not_paid" {
		t.Fatalf("unpaid %d %v", r.status, r.body)
	}
	b.write("/api/v1/orders/"+unpaid+"/cancel", nil, "")
	ref := e.paid(b, 1, "key-00000002")
	e.deliver()
	if r := b.write("/api/v1/orders/"+ref+"/resend", nil, ""); r.status != 202 || r.body["status"] != "queued" {
		t.Fatalf("resend %d %v", r.status, r.body)
	}
	if r := b.write("/api/v1/orders/"+ref+"/resend", nil, ""); r.status != 429 || r.errCode() != "too_soon" {
		t.Fatalf("second resend %d %v", r.status, r.body)
	}
	if r := b.write("/api/v1/orders/NOPE0-NOPE0/resend", nil, ""); r.status != 404 {
		t.Fatalf("unknown %d", r.status)
	}
}

func TestDevOutboxIsMountedOnlyWhenConfigured(t *testing.T) {
	e := newTicketEnv(t)
	b := newBrowser(t, e.srv)
	if resp, body := b.raw("/dev/outbox"); resp.StatusCode != 200 || !strings.Contains(string(body), "Development outbox") {
		t.Fatalf("dev outbox %d", resp.StatusCode)
	}
	srv := httptest.NewTLSServer(e.h(e.pool, nil))
	defer srv.Close()
	if r := newBrowser(t, srv).do("GET", "/dev/outbox", nil, nil); r.status != 404 || r.errCode() != "not_found" {
		t.Fatalf("unmounted %d", r.status)
	}
}

func TestTicketRoutesUnderDatabaseFailures(t *testing.T) {
	e := newTicketEnv(t)
	buyer := newBrowser(t, e.srv)
	buyer.startSession()
	ref := e.paid(buyer, 1, "key-00000001")
	e.deliver()
	m := regexp.MustCompile(`/pickup/#([A-Za-z0-9_-]+)$`).FindStringSubmatch(e.sms.last)
	ticketURL := buyer.do("GET", "/api/v1/orders/"+ref, nil, nil).body["tickets"].([]any)[0].(map[string]any)["pdf_url"].(string)
	cookies := buyer.client.Jar.Cookies(mustURL(e.srv.URL))

	requests := []func(b *browser) int{
		func(b *browser) int { return b.do("GET", "/api/v1/orders/"+ref, nil, nil).status },
		func(b *browser) int { resp, _ := b.raw(ticketURL); return resp.StatusCode },
		// Before the pickup cases: redeeming a link renews (and so retires)
		// the buyer's session token that every case reuses.
		func(b *browser) int {
			_, _ = e.pool.Exec(context.Background(), `DELETE FROM outbox_job WHERE dedupe_key LIKE '%:resend:%'`)
			return b.write("/api/v1/orders/"+ref+"/resend", nil, "").status
		},
		func(b *browser) int {
			return b.write("/api/v1/pickup/exchange", map[string]string{"secret": m[1]}, "").status
		},
		func(b *browser) int {
			if r := b.write("/api/v1/pickup/exchange", map[string]string{"secret": m[1]}, ""); r.status == 500 {
				return 500
			}
			b.startSession()
			return b.do("GET", "/api/v1/pickup", nil, nil).status
		},
	}
	for i, req := range requests {
		pgtest.EachFault(t, e.pool, func(db dbx.DB) error {
			srv := httptest.NewTLSServer(e.h(db, nil))
			defer srv.Close()
			b := newBrowser(t, srv)
			b.client.Jar.SetCookies(mustURL(srv.URL), cookies)
			b.startSession()
			if status := req(b); status == 500 {
				return fmt.Errorf("request %d: 500", i)
			}
			return nil
		}, func(int, error) {})
	}
}

func mustURL(s string) *url.URL {
	u, _ := url.Parse(s)
	return u
}
