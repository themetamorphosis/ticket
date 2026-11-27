package session_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"kacheem/backend/internal/pgtest"
	"kacheem/backend/internal/session"
)

const origin = "https://tickets.test"

func TestBuyerSessionLifecycleAndWriteChecks(t *testing.T) {
	pool := pgtest.Pool(t)
	m := session.NewBuyer(pool, 0, nil)
	if m.Cookie.Name != session.BuyerCookie || !m.Cookie.Secure || !m.Cookie.HttpOnly ||
		m.Cookie.SameSite != http.SameSiteLaxMode || m.Cookie.Domain != "" {
		t.Fatalf("cookie = %+v", m.Cookie)
	}

	var first, second session.Buyer
	var writeVerdicts []string
	h := m.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ensure":
			first = session.Ensure(m, r)
			second = session.Ensure(m, r)
		case "/write":
			writeVerdicts = append(writeVerdicts, session.CheckWrite(m, r, origin))
		}
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/ensure", nil))
	cookie := rec.Result().Cookies()[0]
	if first.ID == "" || first.CSRFToken == "" || first != second || cookie.Name != session.BuyerCookie {
		t.Fatalf("first %+v second %+v cookie %v", first, second, cookie)
	}

	write := func(withCookie bool, origin, token string) {
		req := httptest.NewRequest("POST", "/write", nil)
		if withCookie {
			req.AddCookie(cookie)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		req.Header.Set(session.CSRFHeader, token)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	write(true, origin, first.CSRFToken)
	write(true, "https://evil.test", first.CSRFToken)
	write(true, "", first.CSRFToken)
	write(true, origin, "wrong")
	write(false, origin, first.CSRFToken)
	want := []string{"", "bad_origin", "bad_origin", "bad_csrf", "bad_csrf"}
	for i := range want {
		if writeVerdicts[i] != want[i] {
			t.Fatalf("verdicts = %q, want %q", writeVerdicts, want)
		}
	}

	// The session persists in PostgreSQL across requests.
	var again session.Buyer
	h2 := m.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { again = session.Current(m, r) }))
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(cookie)
	h2.ServeHTTP(httptest.NewRecorder(), req)
	if again != first {
		t.Fatalf("reloaded %+v, want %+v", again, first)
	}
	var rows int
	_ = pool.QueryRow(t.Context(), `SELECT count(*) FROM buyer_session`).Scan(&rows)
	if rows != 1 {
		t.Fatalf("buyer_session rows = %d", rows)
	}
}

func TestCleanupIntervalStartsBackgroundJob(t *testing.T) {
	pool := pgtest.Pool(t)
	m := session.NewBuyer(pool, time.Hour, nil)
	if m.Store == nil {
		t.Fatal("no store")
	}
}

func TestPickupsAndCanSee(t *testing.T) {
	m := scs.New()
	var calls []func(r *http.Request)
	h := m.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[0](r)
		calls = calls[1:]
	}))
	var cookie *http.Cookie
	run := func(fn func(r *http.Request)) {
		calls = append(calls, fn)
		req := httptest.NewRequest("GET", "/", nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if c := rec.Result().Cookies(); len(c) > 0 {
			cookie = c[0]
		}
	}
	run(func(r *http.Request) {
		if session.Pickups(m, r) != nil || session.CanSee(m, r, "o1", "") {
			t.Error("fresh session sees something")
		}
		for i := range 25 {
			if err := session.AddPickup(m, r, fmt.Sprintf("o%d", i)); err != nil {
				t.Error(err)
			}
		}
		_ = session.AddPickup(m, r, "o24") // already present
	})
	run(func(r *http.Request) {
		p := session.Pickups(m, r)
		if len(p) != 20 || p[0] != "o5" || p[19] != "o24" {
			t.Errorf("pickups %v", p)
		}
		if !session.CanSee(m, r, "o24", "someone") || session.CanSee(m, r, "o1", "someone") {
			t.Error("pickup visibility")
		}
		b := session.Ensure(m, r)
		if !session.CanSee(m, r, "other", b.ID) || session.CanSee(m, r, "other", "") {
			t.Error("buyer visibility")
		}
	})
}

func TestAddPickupReportsRenewFailure(t *testing.T) {
	pool := pgtest.Pool(t)
	broken, err := pgxpool.New(t.Context(), pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	broken.Close()
	m := session.NewBuyer(pool, 0, func(http.ResponseWriter, *http.Request, error) {})
	good := m.Store
	var cookie *http.Cookie
	var addErr error
	h := m.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookie == nil {
			session.Ensure(m, r) // create a stored session first
			return
		}
		// The store fails while the existing token is being replaced.
		m.Store = session.NewBuyer(broken, 0, nil).Store
		addErr = session.AddPickup(m, r, "o1")
		m.Store = good
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	cookie = rec.Result().Cookies()[0]
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(cookie)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if addErr == nil {
		t.Fatal("renewal failure not reported")
	}
}
