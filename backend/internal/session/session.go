// Package session configures opaque, PostgreSQL-backed buyer sessions and the
// CSRF and Origin checks every browser write must pass (plan section 7).
package session

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/scs/pgxstore"
	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// BuyerCookie is host-only (no Domain), so the staff host never sees it.
	BuyerCookie = "kc_buyer"
	keyBuyerID  = "buyer_id"
	keyCSRF     = "csrf"
	// CSRFHeader carries the session-bound token on every browser write.
	CSRFHeader = "X-CSRF-Token"
)

// NewBuyer returns the buyer session manager. cleanup is how often expired
// rows are deleted (0 disables the background job, for tests). onError
// answers session-store failures; nil keeps SCS's default plain-text 500.
func NewBuyer(pool *pgxpool.Pool, cleanup time.Duration, onError func(http.ResponseWriter, *http.Request, error)) *scs.SessionManager {
	m := scs.New()
	m.Store = pgxstore.NewWithConfig(pool, pgxstore.Config{TableName: "buyer_session", CleanUpInterval: cleanup})
	m.Lifetime = 24 * time.Hour
	m.IdleTimeout = 12 * time.Hour
	m.Cookie.Name = BuyerCookie
	m.Cookie.HttpOnly = true
	m.Cookie.Secure = true
	m.Cookie.SameSite = http.SameSiteLaxMode
	m.Cookie.Path = "/"
	m.Cookie.Persist = true
	if onError != nil {
		m.ErrorFunc = onError
	}
	return m
}

// Buyer is the identity of an anonymous buyer session.
type Buyer struct {
	ID        string
	CSRFToken string
}

// Ensure returns the session's buyer, creating one on first use.
func Ensure(m *scs.SessionManager, r *http.Request) Buyer {
	ctx := r.Context()
	b := Current(m, r)
	if b.ID == "" {
		b = Buyer{ID: random(), CSRFToken: random()}
		m.Put(ctx, keyBuyerID, b.ID)
		m.Put(ctx, keyCSRF, b.CSRFToken)
	}
	return b
}

// Current returns the session's buyer, or a zero Buyer if there is none.
func Current(m *scs.SessionManager, r *http.Request) Buyer {
	ctx := r.Context()
	return Buyer{ID: m.GetString(ctx, keyBuyerID), CSRFToken: m.GetString(ctx, keyCSRF)}
}

// CheckWrite reports why a browser write must be refused, or "" if it may
// proceed: the Origin must be exactly origin, and the CSRF header must match
// the token bound to this session.
func CheckWrite(m *scs.SessionManager, r *http.Request, origin string) string {
	if r.Header.Get("Origin") != origin {
		return "bad_origin"
	}
	want := Current(m, r).CSRFToken
	got := r.Header.Get(CSRFHeader)
	if want == "" || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
		return "bad_csrf"
	}
	return ""
}

func random() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

const keyPickups = "pickup_orders"

// maxPickups bounds how many redeemed pickup links one session remembers.
const maxPickups = 20

// AddPickup records that this session redeemed a pickup link for orderID.
// The session token is renewed, as on any privilege change.
func AddPickup(m *scs.SessionManager, r *http.Request, orderID string) error {
	ctx := r.Context()
	ids := Pickups(m, r)
	for _, id := range ids {
		if id == orderID {
			return nil
		}
	}
	ids = append(ids, orderID)
	if len(ids) > maxPickups {
		ids = ids[len(ids)-maxPickups:]
	}
	if err := m.RenewToken(ctx); err != nil {
		return err
	}
	m.Put(ctx, keyPickups, strings.Join(ids, ","))
	return nil
}

// Pickups lists the orders this session may download through pickup links.
func Pickups(m *scs.SessionManager, r *http.Request) []string {
	v := m.GetString(r.Context(), keyPickups)
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}

// CanSee reports whether this session may see orderID's tickets: as the buyer
// who placed it, or through a redeemed pickup link.
func CanSee(m *scs.SessionManager, r *http.Request, orderID, orderBuyerID string) bool {
	if b := Current(m, r).ID; b != "" && b == orderBuyerID {
		return true
	}
	for _, id := range Pickups(m, r) {
		if id == orderID {
			return true
		}
	}
	return false
}

// StaffCookie is the staff session cookie, host-only on the staff origin.
const StaffCookie = "kc_staff"

// NewStaff returns the staff session manager: stricter than the buyer's
// (SameSite=Strict, a 12-hour lifetime and a 2-hour idle timeout).
func NewStaff(pool *pgxpool.Pool, cleanup time.Duration, onError func(http.ResponseWriter, *http.Request, error)) *scs.SessionManager {
	m := NewBuyer(pool, cleanup, onError)
	m.Store = pgxstore.NewWithConfig(pool, pgxstore.Config{TableName: "staff_session", CleanUpInterval: cleanup})
	m.Lifetime = 12 * time.Hour
	m.IdleTimeout = 2 * time.Hour
	m.Cookie.Name = StaffCookie
	m.Cookie.SameSite = http.SameSiteStrictMode
	return m
}
