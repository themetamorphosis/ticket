package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/pgtest"
	"kacheem/backend/internal/session"
)

const testOrigin = "https://tickets.test"

// browser is a buyer with a cookie jar talking to a TLS test server, so the
// Secure session cookie behaves as it does in production.
type browser struct {
	t      *testing.T
	srv    *httptest.Server
	client *http.Client
	csrf   string
	key    int
}

type response struct {
	status int
	body   map[string]any
	header http.Header
}

func (r response) errCode() string {
	e, _ := r.body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func (r response) errField(name string) any {
	e, _ := r.body["error"].(map[string]any)
	return e[name]
}

func newServer(t *testing.T, db dbx.DB, pool *pgxpool.Pool) (*httptest.Server, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	h := NewPublic(PublicDeps{
		DB:       db,
		Sessions: session.NewBuyer(pool, 0, SessionError(logger)),
		Origin:   testOrigin,
		Logger:   logger,
	})
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return srv, &logs
}

func newBrowser(t *testing.T, srv *httptest.Server) *browser {
	t.Helper()
	// srv.Client() is shared, so each browser gets its own client and jar.
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, srv: srv, client: &http.Client{Transport: srv.Client().Transport, Jar: jar}}
}

func (b *browser) do(method, path string, body any, header map[string]string) response {
	b.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
		if s, ok := body.(string); ok {
			rd = strings.NewReader(s)
		}
	}
	req, _ := http.NewRequest(method, b.srv.URL+path, rd)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		b.t.Fatalf("%s %s: non-JSON body: %v", method, path, err)
	}
	return response{resp.StatusCode, out, resp.Header}
}

func (b *browser) startSession() {
	b.t.Helper()
	r := b.do("GET", "/api/v1/session", nil, nil)
	b.csrf, _ = r.body["csrf_token"].(string)
	if r.status != 200 || b.csrf == "" {
		b.t.Fatalf("session: %d %v", r.status, r.body)
	}
}

// write sends a protected browser write with the right Origin and CSRF token.
func (b *browser) write(path string, body any, key string) response {
	b.t.Helper()
	h := map[string]string{"Origin": testOrigin, session.CSRFHeader: b.csrf, "Content-Type": "application/json"}
	if key != "" {
		h["Idempotency-Key"] = key
	}
	return b.do("POST", path, body, h)
}

func booking(qty int) map[string]any {
	return map[string]any{"event": "live", "quantity": qty, "name": "Nusrat Jahan",
		"email": "nusrat@example.com", "phone": "+8801711000000", "age": 28}
}

func TestBuyerReservesWithoutAnAccount(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live", Shards: []int32{5, 5}})
	srv, _ := newServer(t, pool, pool)
	b := newBrowser(t, srv)

	a := b.do("GET", "/api/v1/events/live/availability", nil, nil)
	if a.status != 200 || a.body["state"] != "on_sale" || a.body["remaining"] != float64(10) || a.body["server_time"] == "" {
		t.Fatalf("availability: %d %v", a.status, a.body)
	}
	q := b.do("GET", "/api/v1/events/live/quote?quantity=3", nil, nil)
	if q.status != 200 || q.body["total_minor"] != float64(465000) || q.body["currency"] != "BDT" {
		t.Fatalf("quote: %d %v", q.status, q.body)
	}

	b.startSession()
	created := b.write("/api/v1/orders", booking(3), "key-00000001")
	if created.status != 201 || created.body["status"] != "held" || created.body["total_minor"] != float64(465000) {
		t.Fatalf("create: %d %v", created.status, created.body)
	}
	ref := created.body["ref"].(string)

	// A duplicate submit (double click, retry after a lost response) reuses the hold.
	replay := b.write("/api/v1/orders", booking(3), "key-00000001")
	if replay.status != 200 || replay.body["ref"] != ref {
		t.Fatalf("replay: %d %v", replay.status, replay.body)
	}
	// Refreshing the order page shows the same order.
	got := b.do("GET", "/api/v1/orders/"+ref, nil, nil)
	if got.status != 200 || got.body["ref"] != ref || got.body["quantity"] != float64(3) {
		t.Fatalf("get: %d %v", got.status, got.body)
	}
	if after := b.do("GET", "/api/v1/events/live/availability", nil, nil); after.body["remaining"] != float64(7) {
		t.Fatalf("remaining after hold: %v", after.body)
	}

	// Another buyer cannot see this order.
	other := newBrowser(t, srv)
	if r := other.do("GET", "/api/v1/orders/"+ref, nil, nil); r.status != 404 || r.errCode() != "order_not_found" {
		t.Fatalf("other buyer: %d %v", r.status, r.body)
	}

	cancelled := b.write("/api/v1/orders/"+ref+"/cancel", nil, "")
	if cancelled.status != 200 || cancelled.body["status"] != "cancelled" {
		t.Fatalf("cancel: %d %v", cancelled.status, cancelled.body)
	}
	if again := b.write("/api/v1/orders/"+ref+"/cancel", nil, ""); again.status != 409 || again.errCode() != "not_held" {
		t.Fatalf("cancel again: %d %v", again.status, again.body)
	}
	if r := b.do("GET", "/api/v1/orders/"+ref, nil, nil); r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("order response cacheable: %v", r.header)
	}
}

func TestWritesRequireOriginAndSessionCSRF(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live"})
	srv, _ := newServer(t, pool, pool)
	b := newBrowser(t, srv)
	b.startSession()

	cases := []struct {
		name   string
		header map[string]string
		want   string
	}{
		{"no origin", map[string]string{session.CSRFHeader: b.csrf}, "bad_origin"},
		{"cross origin", map[string]string{"Origin": "https://evil.test", session.CSRFHeader: b.csrf}, "bad_origin"},
		{"no token", map[string]string{"Origin": testOrigin}, "bad_csrf"},
		{"wrong token", map[string]string{"Origin": testOrigin, session.CSRFHeader: "nope"}, "bad_csrf"},
	}
	for _, c := range cases {
		c.header["Idempotency-Key"] = "key-00000001"
		if r := b.do("POST", "/api/v1/orders", booking(1), c.header); r.status != 403 || r.errCode() != c.want {
			t.Errorf("%s: %d %v", c.name, r.status, r.body)
		}
	}
	// A buyer without a session has no token to send.
	fresh := newBrowser(t, srv)
	if r := fresh.write("/api/v1/orders", booking(1), "key-00000001"); r.status != 403 || r.errCode() != "bad_csrf" {
		t.Errorf("no session: %d %v", r.status, r.body)
	}
	// The session endpoint is stable for a session.
	before := b.csrf
	b.startSession()
	if b.csrf != before {
		t.Fatal("CSRF token changed within a session")
	}
}

func TestOrderErrorsMapToStableCodes(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live", Shards: []int32{2}, MaxPerOrder: 4})
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "soon", OpensIn: time.Hour, ClosesIn: 2 * time.Hour})
	srv, _ := newServer(t, pool, pool)
	b := newBrowser(t, srv)
	b.startSession()

	bad := booking(1)
	bad["email"] = "nope"
	soon := booking(1)
	soon["event"] = "soon"
	missing := booking(1)
	missing["event"] = "missing"
	checks := []struct {
		name   string
		body   any
		key    string
		status int
		code   string
	}{
		{"bad key", booking(1), "short", 400, "bad_idempotency_key"},
		{"bad json", "{not json", "key-00000001", 400, "bad_json"},
		{"unknown field", `{"event":"live","admin":true}`, "key-00000001", 400, "bad_json"},
		{"two objects", `{"event":"live"} {}`, "key-00000001", 400, "bad_json"},
		{"invalid", bad, "key-00000001", 422, "invalid"},
		{"missing event", missing, "key-00000001", 404, "event_not_found"},
		{"not on sale", soon, "key-00000001", 409, "not_on_sale"},
		{"insufficient", booking(3), "key-00000001", 409, "insufficient"},
	}
	for _, c := range checks {
		r := b.write("/api/v1/orders", c.body, c.key)
		if r.status != c.status || r.errCode() != c.code {
			t.Errorf("%s: %d %v", c.name, r.status, r.body)
		}
	}
	if r := b.write("/api/v1/orders", bad, "key-00000001"); r.errField("fields") == nil {
		t.Errorf("field errors missing: %v", r.body)
	}

	held := b.write("/api/v1/orders", booking(2), "key-00000002")
	if held.status != 201 {
		t.Fatalf("hold: %d %v", held.status, held.body)
	}
	if r := b.write("/api/v1/orders", booking(1), "key-00000002"); r.status != 422 || r.errCode() != "idempotency_key_reused" {
		t.Errorf("reused key: %d %v", r.status, r.body)
	}
	if r := b.write("/api/v1/orders", booking(1), "key-00000003"); r.status != 409 || r.errCode() != "active_hold" ||
		r.errField("order_ref") != held.body["ref"] {
		t.Errorf("active hold: %d %v", r.status, r.body)
	}
	other := newBrowser(t, srv)
	other.startSession()
	if r := other.write("/api/v1/orders", booking(1), "key-00000001"); r.status != 409 || r.errCode() != "sold_out" ||
		r.errField("remaining") != float64(0) {
		t.Errorf("sold out: %d %v", r.status, r.body)
	}
	if r := other.write("/api/v1/orders/NOPE0-NOPE0/cancel", nil, ""); r.status != 404 {
		t.Errorf("cancel unknown: %d %v", r.status, r.body)
	}
	if r := b.do("GET", "/api/v1/orders", nil, nil); r.status != 405 || r.header.Get("Allow") != "POST" {
		t.Errorf("GET orders: %d %v", r.status, r.header)
	}
}

func TestEventLookupsAndQuoteValidation(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live", MaxPerOrder: 4})
	srv, _ := newServer(t, pool, pool)
	b := newBrowser(t, srv)
	for _, path := range []string{"/api/v1/events/missing/availability", "/api/v1/events/missing/quote?quantity=1"} {
		if r := b.do("GET", path, nil, nil); r.status != 404 || r.errCode() != "event_not_found" {
			t.Errorf("%s: %d %v", path, r.status, r.body)
		}
	}
	for _, q := range []string{"", "0", "5", "two"} {
		if r := b.do("GET", "/api/v1/events/live/quote?quantity="+q, nil, nil); r.status != 422 || r.errField("fields") == nil {
			t.Errorf("quantity %q: %d %v", q, r.status, r.body)
		}
	}
	// An event with more than one tier is outside the launch scope.
	_, _ = pool.Exec(context.Background(), `INSERT INTO ticket_type (event_id, name, price_minor, fee_minor, capacity)
		SELECT id, 'VIP', 1, 0, 0 FROM event WHERE slug = 'live'`)
	if r := b.do("GET", "/api/v1/events/live/quote?quantity=1", nil, nil); r.status != 500 || r.errCode() != "internal" {
		t.Errorf("two tiers: %d %v", r.status, r.body)
	}
}

func TestDatabaseFailuresBecomeLoggedInternalErrors(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live"})
	// The session (and its CSRF token) lives in the real pool; only the
	// application queries go through the fault-injecting DB.
	sessions := session.NewBuyer(pool, 0, nil)
	var ref string
	step := 0
	requests := []func(b *browser) response{
		func(b *browser) response { return b.do("GET", "/api/v1/events/live/availability", nil, nil) },
		func(b *browser) response { return b.do("GET", "/api/v1/events/live/quote?quantity=1", nil, nil) },
		func(b *browser) response {
			step++
			return b.write("/api/v1/orders", booking(1), fmt.Sprintf("key-fault-%04d", step))
		},
		func(b *browser) response { return b.do("GET", "/api/v1/orders/"+ref, nil, nil) },
		func(b *browser) response { return b.write("/api/v1/orders/"+ref+"/cancel", nil, "") },
	}
	for i, req := range requests {
		pgtest.EachFault(t, pool, func(db dbx.DB) error {
			var logs bytes.Buffer
			srv := httptest.NewTLSServer(NewPublic(PublicDeps{DB: db, Sessions: sessions, Origin: testOrigin,
				Logger: slog.New(slog.NewJSONHandler(&logs, nil))}))
			defer srv.Close()
			b := newBrowser(t, srv)
			b.startSession()
			if i >= 3 {
				// Each attempt needs a fresh hold owned by this browser's session.
				r := b.write("/api/v1/orders", booking(1), "key-setup-0001")
				ref, _ = r.body["ref"].(string)
				if r.status != 201 {
					return errors.New("setup hold failed")
				}
			}
			r := req(b)
			if r.status == 500 {
				if r.errCode() != "internal" || !strings.Contains(logs.String(), "request failed") {
					t.Errorf("request %d: 500 without log: %v %s", i, r.body, logs.String())
				}
				return errors.New("internal")
			}
			return nil
		}, func(int, error) {})
	}
}

func TestSessionStoreFailureIsJSON500(t *testing.T) {
	pool := pgtest.Pool(t)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	broken, err := pgxpool.New(context.Background(), pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	broken.Close()
	srv := httptest.NewTLSServer(NewPublic(PublicDeps{DB: pool, Sessions: session.NewBuyer(broken, 0, SessionError(logger)),
		Origin: testOrigin, Logger: logger}))
	defer srv.Close()
	b := newBrowser(t, srv)
	if r := b.do("GET", "/api/v1/session", nil, nil); r.status != 500 || r.errCode() != "internal" ||
		!strings.Contains(logs.String(), "buyer session store") {
		t.Fatalf("%d %v %s", r.status, r.body, logs.String())
	}
}
