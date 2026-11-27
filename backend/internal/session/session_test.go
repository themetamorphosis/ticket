package session_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
