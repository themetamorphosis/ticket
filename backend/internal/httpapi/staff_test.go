package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"kacheem/backend/internal/audit"
	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/orders"
	"kacheem/backend/internal/payments"
	"kacheem/backend/internal/payments/fakegw"
	"kacheem/backend/internal/pgtest"
	"kacheem/backend/internal/sealbox"
	"kacheem/backend/internal/session"
	"kacheem/backend/internal/staffauth"
	"kacheem/backend/internal/tickets"
)

const staffOrigin = "https://gate.test"

const staffPassword = "correct horse battery staple"

type staffEnv struct {
	t        *testing.T
	pool     *pgxpool.Pool
	auth     *staffauth.Service
	iss      *tickets.Issuer
	sessions *scs.SessionManager
	srv      *httptest.Server
	dir      string
	eventID  string
	n        int
}

func newStaffEnv(t *testing.T, publishDir bool) *staffEnv {
	t.Helper()
	pool := pgtest.Pool(t)
	ids := pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live", Shards: []int32{50, 50}})
	_, _ = pool.Exec(context.Background(), `UPDATE event SET admission_opens_at = now() - interval '1 hour'`)
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	box, _ := sealbox.New(k)
	auth := staffauth.New(pool)
	e := &staffEnv{t: t, pool: pool, auth: auth, iss: &tickets.Issuer{Box: box}, eventID: ids.EventID}
	if publishDir {
		e.dir = t.TempDir()
	}
	e.sessions = session.NewStaff(pool, 0, nil)
	e.srv = httptest.NewTLSServer(e.handler(pool))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *staffEnv) handler(db dbx.DB) http.Handler {
	auth := *e.auth
	auth.DB = db
	return NewStaff(StaffDeps{DB: db, Sessions: e.sessions, Origin: staffOrigin, Auth: &auth, Issuer: e.iss,
		PublishDir: e.dir, Logger: slog.New(slog.DiscardHandler)})
}

// enroll creates an active staff member, directly.
func (e *staffEnv) enroll(email, role string) string {
	e.t.Helper()
	token, err := e.auth.Invite(context.Background(), audit.Actor{Kind: "system"}, email, "Staff "+role, role)
	if err != nil {
		e.t.Fatal(err)
	}
	st, err := e.auth.AcceptInvite(context.Background(), token, staffPassword)
	if err != nil {
		e.t.Fatal(err)
	}
	return st.ID
}

func (e *staffEnv) browser() *browser {
	b := newBrowser(e.t, e.srv)
	b.origin = staffOrigin
	b.startSession()
	return b
}

// signIn signs a staff member in through the API.
func (e *staffEnv) signIn(email string) *browser {
	e.t.Helper()
	b := e.browser()
	r := b.write("/api/v1/staff/login", map[string]string{"email": email, "password": staffPassword}, "")
	if r.status != 200 {
		e.t.Fatalf("login %d %v", r.status, r.body)
	}
	b.startSession() // the token was renewed at sign-in
	return b
}

// paidOrder books and pays an order directly through the services.
func (e *staffEnv) paidOrder(qty int) (string, string) {
	e.t.Helper()
	ctx := context.Background()
	e.n++
	buyer := fmt.Sprintf("buyer-%d", e.n)
	o, _, err := orders.Reserve(ctx, e.pool, orders.Request{EventSlug: "live", BuyerID: buyer, IdempotencyKey: "key-00000001",
		Input: orders.Input{Quantity: qty, Name: "Nusrat Jahan", Email: "nusrat@example.com", Phone: "+8801711000000", Age: 28}})
	if err != nil {
		e.t.Fatal(err)
	}
	gw := &fakegw.Gateway{DB: e.pool, BaseURL: "https://tickets.test"}
	svc := &payments.Service{DB: e.pool, Gateway: gw, ReturnURL: "https://tickets.test/r", OnPaid: e.iss.IssueInTx}
	redirect, _ := svc.Checkout(ctx, buyer, o.Ref)
	u, _ := url.Parse(redirect)
	back, _ := gw.Decide(ctx, u.Path[len("/payments/fake/pay/"):], "approve")
	bu, _ := url.Parse(back)
	_, _ = svc.Return(ctx, bu.Query())
	return o.ID, o.Ref
}

func (e *staffEnv) credential(orderID string, ordinal int) (string, string) {
	e.t.Helper()
	var id string
	var sealed []byte
	_ = e.pool.QueryRow(context.Background(), `SELECT id, credential_sealed FROM ticket WHERE order_id = $1 AND ordinal = $2`,
		orderID, ordinal).Scan(&id, &sealed)
	s, _ := e.iss.Open(sealed)
	return id, s.Credential
}

func TestStaffSignInAndInvitations(t *testing.T) {
	e := newStaffEnv(t, false)
	e.enroll("org@example.org", "organizer")
	org := e.signIn("org@example.org")
	me := org.do("GET", "/api/v1/staff/me", nil, nil)
	if me.status != 200 || me.body["staff"].(map[string]any)["role"] != "organizer" || len(me.body["events"].([]any)) != 1 {
		t.Fatalf("me %d %v", me.status, me.body)
	}

	// The organizer invites a gate person, who accepts on another device.
	inv := org.write("/api/v1/ops/staff/invite", map[string]string{"email": "gate@example.org", "name": "Gate One", "role": "gate"}, "")
	inviteURL, _ := inv.body["invite_url"].(string)
	if inv.status != 201 || !strings.HasPrefix(inviteURL, staffOrigin+"/invite/#") {
		t.Fatalf("invite %d %v", inv.status, inv.body)
	}
	token := strings.TrimPrefix(inviteURL, staffOrigin+"/invite/#")
	newcomer := e.browser()
	accept := func(token, password string) response {
		return newcomer.write("/api/v1/staff/invite/accept", map[string]string{"token": token, "password": password}, "")
	}
	if r := accept(token, "short"); r.status != 422 || r.errCode() != "weak_password" {
		t.Fatalf("weak %d %v", r.status, r.body)
	}
	if r := accept("nope", staffPassword); r.status != 404 || r.errCode() != "invite_invalid" {
		t.Fatalf("bad token %d %v", r.status, r.body)
	}
	if r := newcomer.write("/api/v1/staff/invite/accept", "not json", ""); r.status != 400 {
		t.Fatalf("bad invite json %d", r.status)
	}
	if r := accept(token, staffPassword); r.status != 200 || r.body["staff"].(map[string]any)["role"] != "gate" {
		t.Fatalf("accept %d %v", r.status, r.body)
	}
	newcomer.startSession()
	if r := newcomer.do("GET", "/api/v1/staff/me", nil, nil); r.status != 200 {
		t.Fatalf("newcomer me %d", r.status)
	}
	// Invitation errors.
	for body, code := range map[string]string{
		`{"email":"x","name":"X","role":"gate"}`:                "invalid",
		`{"email":"gate@example.org","name":"G","role":"gate"}`: "exists",
	} {
		if r := org.write("/api/v1/ops/staff/invite", body, ""); r.errCode() != code {
			t.Errorf("%s: %v", body, r.body)
		}
	}

	// Sign-in failures all look the same.
	failing := e.browser()
	if r := failing.write("/api/v1/staff/login", map[string]string{"email": "org@example.org", "password": "nope nope nope"}, ""); r.status != 401 ||
		r.errCode() != "sign_in_failed" {
		t.Fatalf("wrong password %d %v", r.status, r.body)
	}
	if r := failing.write("/api/v1/staff/login", "not json", ""); r.status != 400 {
		t.Fatalf("bad json %d", r.status)
	}
	// Writes need the staff origin and CSRF token.
	if r := failing.do("POST", "/api/v1/staff/login", `{}`, map[string]string{"Origin": "https://tickets.test", session.CSRFHeader: failing.csrf}); r.status != 403 {
		t.Fatalf("buyer origin %d", r.status)
	}

	if r := org.write("/api/v1/staff/logout", nil, ""); r.status != 200 {
		t.Fatalf("logout %d", r.status)
	}
	if r := org.do("GET", "/api/v1/staff/me", nil, nil); r.status != 401 || r.errCode() != "signed_out" {
		t.Fatalf("after logout %d", r.status)
	}
}

func TestRolesAndRevocation(t *testing.T) {
	e := newStaffEnv(t, false)
	e.enroll("org@example.org", "organizer")
	gateID := e.enroll("gate@example.org", "gate")
	org := e.signIn("org@example.org")
	gate := e.signIn("gate@example.org")
	stranger := e.browser()
	if r := stranger.do("GET", "/api/v1/ops/events/live/summary", nil, nil); r.status != 401 {
		t.Fatalf("anonymous %d", r.status)
	}
	for _, path := range []string{"/api/v1/ops/events/live/summary", "/api/v1/ops/finance", "/api/v1/ops/staff", "/api/v1/ops/audit"} {
		if r := gate.do("GET", path, nil, nil); r.status != 403 || r.errCode() != "forbidden" {
			t.Errorf("gate %s: %d", path, r.status)
		}
	}
	if r := org.write("/api/v1/ops/staff/"+gateID+"/disable", map[string]string{"reason": ""}, ""); r.errCode() != "reason_required" {
		t.Fatalf("no reason %v", r.body)
	}
	if r := org.write("/api/v1/ops/staff/00000000-0000-0000-0000-000000000000/disable", map[string]string{"reason": "x"}, ""); r.status != 404 {
		t.Fatalf("unknown %d", r.status)
	}
	if r := org.write("/api/v1/ops/staff/"+gateID+"/disable", map[string]string{"reason": "lost phone"}, ""); r.status != 200 {
		t.Fatalf("disable %d %v", r.status, r.body)
	}
	// The gate person's live session ends at their next request.
	if r := gate.do("GET", "/api/v1/staff/me", nil, nil); r.status != 401 || r.errCode() != "signed_out" {
		t.Fatalf("revoked session %d", r.status)
	}
	list := org.do("GET", "/api/v1/ops/staff", nil, nil)
	if len(list.body["staff"].([]any)) != 2 {
		t.Fatalf("staff list %v", list.body)
	}
	if entries := org.do("GET", "/api/v1/ops/audit", nil, nil).body["entries"].([]any); len(entries) < 4 {
		t.Fatalf("audit %d entries", len(entries))
	}
}

func TestScannerThroughTheAPI(t *testing.T) {
	e := newStaffEnv(t, false)
	e.enroll("org@example.org", "organizer")
	gateStaff := e.enroll("gate@example.org", "gate")
	org := e.signIn("org@example.org")
	gate := e.signIn("gate@example.org")
	north := org.write("/api/v1/ops/events/live/gates", map[string]string{"name": "North"}, "").body["id"].(string)
	south := org.write("/api/v1/ops/events/live/gates", map[string]string{"name": "South"}, "").body["id"].(string)
	if r := org.write("/api/v1/ops/events/live/gates", map[string]string{"name": "North"}, ""); r.status != 409 {
		t.Fatalf("duplicate gate %d", r.status)
	}
	if r := gate.do("GET", "/api/v1/ops/events/live/my-gates", nil, nil); len(r.body["gates"].([]any)) != 0 {
		t.Fatalf("unassigned gates %v", r.body)
	}
	orderID, _ := e.paidOrder(1)
	_, cred := e.credential(orderID, 1)
	scan := func(b *browser, gateID, device, request string) response {
		return b.write("/api/v1/admissions", map[string]string{"gate_id": gateID, "device_id": device, "request_id": request,
			"qr": staffOrigin + "/scan#" + cred}, "")
	}
	if r := scan(gate, north, "device-north", "request-0001"); r.status != 403 || r.errCode() != "gate_not_assigned" {
		t.Fatalf("unassigned %d %v", r.status, r.body)
	}
	if r := org.write("/api/v1/ops/gates/"+north+"/assign", map[string]string{"staff_id": gateStaff}, ""); r.status != 200 {
		t.Fatalf("assign %d", r.status)
	}
	if r := org.write("/api/v1/ops/gates/"+north+"/assign", map[string]string{"staff_id": "00000000-0000-0000-0000-000000000000"}, ""); r.status != 404 {
		t.Fatalf("assign unknown %d", r.status)
	}
	if r := gate.do("GET", "/api/v1/ops/events/live/my-gates", nil, nil); len(r.body["gates"].([]any)) != 1 {
		t.Fatalf("my gates %v", r.body)
	}
	if r := gate.do("GET", "/api/v1/ops/events/missing/my-gates", nil, nil); r.status != 404 {
		t.Fatalf("missing event %d", r.status)
	}

	first := scan(gate, north, "device-north", "request-0001")
	if first.body["outcome"] != "admitted" || first.body["message"] != "Admit" || first.body["replay"] != false ||
		first.body["ticket"] != "General admission · ticket 1 of 1" {
		t.Fatalf("first %v", first.body)
	}
	// The organizer scans at another gate a moment later.
	second := scan(org, south, "device-south", "request-0002")
	if second.body["outcome"] != "already_used" || second.body["first_gate"] != "North" || second.body["message"] != "Already used" {
		t.Fatalf("second %v", second.body)
	}
	retry := scan(gate, north, "device-north", "request-0001")
	if retry.body["outcome"] != "admitted" || retry.body["replay"] != true || retry.body["first_gate"] != "North" {
		t.Fatalf("retry %v", retry.body)
	}
	bad := gate.write("/api/v1/admissions", map[string]string{"gate_id": north, "device_id": "device-north", "request_id": "request-0003",
		"code": "NOPE"}, "")
	if bad.status != 422 || bad.errCode() != "bad_scan" {
		t.Fatalf("bad scan %d %v", bad.status, bad.body)
	}
	if r := gate.write("/api/v1/admissions", "[]", ""); r.status != 400 {
		t.Fatalf("bad json %d", r.status)
	}
	summary := org.do("GET", "/api/v1/ops/events/live/summary", nil, nil)
	if summary.body["summary"].(map[string]any)["admitted"] != float64(1) || len(summary.body["gates"].([]any)) != 2 {
		t.Fatalf("summary %v", summary.body)
	}
	if r := org.do("GET", "/api/v1/ops/events/missing/summary", nil, nil); r.status != 404 {
		t.Fatalf("missing summary %d", r.status)
	}
	gates := org.do("GET", "/api/v1/ops/events/live/gates", nil, nil)
	if len(gates.body["gates"].([]any)) != 2 {
		t.Fatalf("gates %v", gates.body)
	}
}

func TestSupportAndFinanceActions(t *testing.T) {
	e := newStaffEnv(t, false)
	e.enroll("org@example.org", "organizer")
	e.enroll("support@example.org", "support")
	e.enroll("finance@example.org", "finance")
	support := e.signIn("support@example.org")
	finance := e.signIn("finance@example.org")
	orderID, ref := e.paidOrder(2)
	ticket1, _ := e.credential(orderID, 1)

	search := support.do("GET", "/api/v1/ops/orders?q="+url.QueryEscape("nusrat@example.com"), nil, nil)
	row := search.body["orders"].([]any)[0].(map[string]any)
	if row["email"] != "n*****@example.com" {
		t.Fatalf("support sees %v", row)
	}
	if full := finance.do("GET", "/api/v1/ops/orders?q="+ref, nil, nil).body["orders"].([]any)[0].(map[string]any); full["email"] != "nusrat@example.com" {
		t.Fatalf("finance sees %v", full)
	}
	detail := support.do("GET", "/api/v1/ops/orders/"+ref, nil, nil)
	if detail.status != 200 || len(detail.body["tickets"].([]any)) != 2 || len(detail.body["attempts"].([]any)) != 1 {
		t.Fatalf("detail %d %v", detail.status, detail.body)
	}
	if r := support.do("GET", "/api/v1/ops/orders/NOPE0-NOPE0", nil, nil); r.status != 404 {
		t.Fatalf("unknown detail %d", r.status)
	}

	if r := support.write("/api/v1/ops/orders/"+ref+"/resend", nil, ""); r.status != 200 {
		t.Fatalf("resend %d %v", r.status, r.body)
	}
	if r := support.write("/api/v1/ops/orders/"+ref+"/resend", nil, ""); r.status != 429 {
		t.Fatalf("resend again %d", r.status)
	}
	if r := support.write("/api/v1/ops/orders/NOPE0-NOPE0/resend", nil, ""); r.status != 404 {
		t.Fatalf("resend unknown %d", r.status)
	}
	if r := support.write("/api/v1/ops/tickets/"+ticket1+"/reissue", map[string]string{"reason": ""}, ""); r.errCode() != "reason_required" {
		t.Fatalf("reissue no reason %v", r.body)
	}
	if r := support.write("/api/v1/ops/tickets/"+ticket1+"/reissue", map[string]string{"reason": "lost phone"}, ""); r.status != 200 {
		t.Fatalf("reissue %d %v", r.status, r.body)
	}
	if r := support.write("/api/v1/ops/tickets/nope/reissue", map[string]string{"reason": "x"}, ""); r.status != 404 {
		t.Fatalf("reissue unknown %d", r.status)
	}
	if r := support.write("/api/v1/ops/tickets/"+ticket1+"/void", map[string]string{"reason": "duplicate"}, ""); r.status != 200 {
		t.Fatalf("void %d", r.status)
	}
	if r := support.write("/api/v1/ops/tickets/"+ticket1+"/void", map[string]string{"reason": "again"}, ""); r.status != 409 || r.errCode() != "not_valid" {
		t.Fatalf("void again %d %v", r.status, r.body)
	}
	if r := support.write("/api/v1/ops/orders/"+ref+"/refund", map[string]string{"reason": "x"}, ""); r.status != 403 {
		t.Fatalf("support refund %d", r.status)
	}
	if r := finance.write("/api/v1/ops/orders/"+ref+"/refund", map[string]string{"reason": "x"}, ""); r.status != 409 || r.errCode() != "not_valid" {
		t.Fatalf("refund with a void ticket %d %v", r.status, r.body)
	}
	orderID2, ref2 := e.paidOrder(1)
	if r := finance.write("/api/v1/ops/orders/"+ref2+"/refund", map[string]string{"reason": "event clash"}, ""); r.status != 200 {
		t.Fatalf("refund %d %v", r.status, r.body)
	}
	if r := finance.write("/api/v1/ops/orders/NOPE0-NOPE0/refund", map[string]string{"reason": "x"}, ""); r.status != 404 {
		t.Fatalf("refund unknown %d", r.status)
	}
	if r := finance.write("/api/v1/ops/orders/"+ref2+"/refund", "nope", ""); r.status != 400 {
		t.Fatalf("refund bad json %d", r.status)
	}
	fin := finance.do("GET", "/api/v1/ops/finance", nil, nil)
	if fin.body["balanced"] != true || len(fin.body["queue"].([]any)) != 1 {
		t.Fatalf("finance %v", fin.body)
	}
	// An admitted order goes to a supervisor instead of being refunded.
	orderID3, ref3 := e.paidOrder(1)
	_, cred3 := e.credential(orderID3, 1)
	org := e.signIn("org@example.org")
	north := org.write("/api/v1/ops/events/live/gates", map[string]string{"name": "North"}, "").body["id"].(string)
	org.write("/api/v1/admissions", map[string]string{"gate_id": north, "device_id": "device-north", "request_id": "request-0001",
		"qr": staffOrigin + "/scan#" + cred3}, "")
	if r := finance.write("/api/v1/ops/orders/"+ref3+"/refund", map[string]string{"reason": "x"}, ""); r.status != 409 || r.errCode() != "admitted" {
		t.Fatalf("admitted refund %d %v", r.status, r.body)
	}
	// An order whose payment was refunded already has nothing left to refund.
	_, _ = e.pool.Exec(context.Background(), `UPDATE ticket SET status = 'valid' WHERE order_id = $1`, orderID2)
	if r := finance.write("/api/v1/ops/orders/"+ref2+"/refund", map[string]string{"reason": "again"}, ""); r.status != 409 || r.errCode() != "no_payment" {
		t.Fatalf("no payment %d %v", r.status, r.body)
	}
}

func TestOrganizerManagesTheEvent(t *testing.T) {
	e := newStaffEnv(t, true)
	e.enroll("org@example.org", "organizer")
	org := e.signIn("org@example.org")
	ev := org.do("GET", "/api/v1/ops/events/live", nil, nil)
	if ev.status != 200 || ev.body["event"].(map[string]any)["title"] != "Test Concert" {
		t.Fatalf("event %d %v", ev.status, ev.body)
	}
	if r := org.do("GET", "/api/v1/ops/events/missing", nil, nil); r.status != 404 {
		t.Fatalf("missing %d", r.status)
	}
	details := map[string]any{"title": "Kacheem Live", "organizer": "Kacheem Comms", "venue_name": "Hall", "venue_address": "Dhaka",
		"description": "", "lineup": []string{"A"}, "starts_at": "2027-02-14T13:30:00Z", "doors_at": nil, "time_zone": "Asia/Dhaka",
		"sale_opens_at": "2026-11-01T00:00:00Z", "sale_closes_at": "2027-02-14T12:00:00Z", "max_per_order": 4, "min_age": 0,
		"price_minor": 200000, "fee_minor": 6000}
	if r := org.write("/api/v1/ops/events/live", details, ""); r.status != 200 {
		t.Fatalf("update %d %v", r.status, r.body)
	}
	details["time_zone"] = "Mars/Olympus"
	if r := org.write("/api/v1/ops/events/live", details, ""); r.status != 422 {
		t.Fatalf("invalid %d", r.status)
	}
	if r := org.write("/api/v1/ops/events/missing", details, ""); r.status != 404 {
		t.Fatalf("update missing %d", r.status)
	}
	if r := org.write("/api/v1/ops/events/live", "[]", ""); r.status != 400 {
		t.Fatalf("bad json %d", r.status)
	}

	e.paidOrder(3)
	if r := org.write("/api/v1/ops/events/live/capacity", map[string]int{"capacity": 2}, ""); r.status != 409 ||
		r.errField("min") != float64(3) {
		t.Fatalf("below %d %v", r.status, r.body)
	}
	if r := org.write("/api/v1/ops/events/live/capacity", map[string]int{"capacity": 150}, ""); r.status != 200 {
		t.Fatalf("capacity %d %v", r.status, r.body)
	}
	if r := org.write("/api/v1/ops/events/missing/capacity", map[string]int{"capacity": 1}, ""); r.status != 404 {
		t.Fatalf("capacity missing %d", r.status)
	}
	if r := org.write("/api/v1/ops/events/live/capacity", "x", ""); r.status != 400 {
		t.Fatalf("capacity bad json %d", r.status)
	}

	pub := org.write("/api/v1/ops/events/live/publish", nil, "")
	if pub.status != 200 || pub.body["revision"] != float64(2) {
		t.Fatalf("publish %d %v", pub.status, pub.body)
	}
	snap, _ := os.ReadFile(filepath.Join(e.dir, "event.json"))
	if !strings.Contains(string(snap), "Kacheem Live") {
		t.Fatal("snapshot not written")
	}
	if r := org.write("/api/v1/ops/events/missing/publish", nil, ""); r.status != 404 {
		t.Fatalf("publish missing %d", r.status)
	}
	_, _ = e.pool.Exec(context.Background(), `UPDATE event SET time_zone = 'Mars/Olympus'`)
	if r := org.write("/api/v1/ops/events/live/publish", nil, ""); r.status != 422 {
		t.Fatalf("publish invalid %d", r.status)
	}

	// Without a publish directory the dashboard says so.
	e.dir = ""
	srv := httptest.NewTLSServer(e.handler(e.pool))
	defer srv.Close()
	e.srv = srv
	org2 := e.signIn("org@example.org")
	if r := org2.write("/api/v1/ops/events/live/publish", nil, ""); r.status != 503 {
		t.Fatalf("unconfigured %d", r.status)
	}
	for _, path := range []string{"/api/v1/ops/events/live/gates", "/api/v1/ops/gates/x/assign", "/api/v1/ops/staff/invite", "/api/v1/ops/staff/x/disable",
		"/api/v1/ops/tickets/x/reissue", "/api/v1/ops/tickets/x/void"} {
		if r := org2.write(path, "{bad", ""); r.status != 400 {
			t.Errorf("%s bad json %d", path, r.status)
		}
	}
	if r := org2.do("PUT", "/api/v1/ops/events/live", nil, nil); r.status != 405 || r.header.Get("Allow") != "GET, HEAD, POST" {
		t.Fatalf("405 %d %v", r.status, r.header)
	}
}

func TestStaffRoutesUnderDatabaseFailures(t *testing.T) {
	e := newStaffEnv(t, true)
	e.enroll("org@example.org", "organizer")
	orderID, ref := e.paidOrder(1)
	ticketID, cred := e.credential(orderID, 1)
	org := e.signIn("org@example.org")
	north := org.write("/api/v1/ops/events/live/gates", map[string]string{"name": "North"}, "").body["id"].(string)
	cookies := org.client.Jar.Cookies(mustURL(e.srv.URL))
	n := 0
	requests := []func(b *browser) int{
		func(b *browser) int { return b.do("GET", "/api/v1/staff/me", nil, nil).status },
		func(b *browser) int { return b.do("GET", "/api/v1/ops/events/live/my-gates", nil, nil).status },
		func(b *browser) int {
			n++
			return b.write("/api/v1/admissions", map[string]string{"gate_id": north, "device_id": "device-north",
				"request_id": fmt.Sprintf("request-%04d", n), "qr": staffOrigin + "/scan#" + cred}, "").status
		},
		func(b *browser) int { return b.do("GET", "/api/v1/ops/events/live/summary", nil, nil).status },
		func(b *browser) int { return b.do("GET", "/api/v1/ops/orders?q="+ref, nil, nil).status },
		func(b *browser) int { return b.do("GET", "/api/v1/ops/orders/"+ref, nil, nil).status },
		func(b *browser) int {
			_, _ = e.pool.Exec(context.Background(), `DELETE FROM outbox_job WHERE dedupe_key LIKE '%:resend:%'`)
			return b.write("/api/v1/ops/orders/"+ref+"/resend", nil, "").status
		},
		func(b *browser) int {
			return b.write("/api/v1/ops/tickets/"+ticketID+"/reissue", map[string]string{"reason": "x"}, "").status
		},
		func(b *browser) int { return b.do("GET", "/api/v1/ops/finance", nil, nil).status },
		func(b *browser) int { return b.do("GET", "/api/v1/ops/events/live", nil, nil).status },
		func(b *browser) int {
			return b.write("/api/v1/ops/events/live/capacity", map[string]int{"capacity": 120}, "").status
		},
		func(b *browser) int { return b.write("/api/v1/ops/events/live/publish", nil, "").status },
		func(b *browser) int { return b.do("GET", "/api/v1/ops/events/live/gates", nil, nil).status },
		func(b *browser) int {
			n++
			return b.write("/api/v1/ops/events/live/gates", map[string]string{"name": fmt.Sprintf("G%d", n)}, "").status
		},
		func(b *browser) int { return b.do("GET", "/api/v1/ops/staff", nil, nil).status },
		func(b *browser) int {
			n++
			return b.write("/api/v1/ops/staff/invite", map[string]string{"email": fmt.Sprintf("p%d@example.org", n), "name": "P", "role": "gate"}, "").status
		},
		func(b *browser) int { return b.do("GET", "/api/v1/ops/audit", nil, nil).status },
		func(b *browser) int {
			return b.write("/api/v1/staff/login", map[string]string{"email": "org@example.org", "password": staffPassword}, "").status
		},
	}
	for i, req := range requests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			pgtest.EachFault(t, e.pool, func(db dbx.DB) error {
				srv := httptest.NewTLSServer(e.handler(db))
				defer srv.Close()
				b := newBrowser(t, srv)
				b.origin = staffOrigin
				b.client.Jar.SetCookies(mustURL(srv.URL), cookies)
				b.startSession()
				if status := req(b); status == 500 {
					return fmt.Errorf("request %d: 500", i)
				}
				return nil
			}, func(int, error) {})
		})
	}
	// Disabling a staff member under failures.
	victim := e.enroll("victim@example.org", "gate")
	pgtest.EachFault(t, e.pool, func(db dbx.DB) error {
		_, _ = e.pool.Exec(context.Background(), `UPDATE staff SET status = 'active' WHERE id = $1`, victim)
		srv := httptest.NewTLSServer(e.handler(db))
		defer srv.Close()
		b := newBrowser(t, srv)
		b.origin = staffOrigin
		b.client.Jar.SetCookies(mustURL(srv.URL), cookies)
		b.startSession()
		if status := b.write("/api/v1/ops/staff/"+victim+"/disable", map[string]string{"reason": "x"}, "").status; status == 500 {
			return fmt.Errorf("500")
		}
		return nil
	}, func(int, error) {})
}

func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// failingStore is a session store whose Delete can fail, which is what
// renewing the token (at sign-in) and destroying the session (sign-out) use.
type failingStore struct {
	scs.Store
	fail *bool
}

func (f failingStore) Delete(token string) error {
	if *f.fail {
		return fmt.Errorf("store unavailable")
	}
	return f.Store.Delete(token)
}

func TestSessionStoreFailuresAtSignInAndOut(t *testing.T) {
	e := newStaffEnv(t, false)
	e.enroll("org@example.org", "organizer")
	fail := false
	e.sessions = scs.New()
	e.sessions.Store = failingStore{Store: e.sessions.Store, fail: &fail}
	e.sessions.Cookie.Name = session.StaffCookie
	srv := httptest.NewTLSServer(e.handler(e.pool))
	defer srv.Close()
	e.srv = srv
	org := e.signIn("org@example.org")
	fail = true
	if r := org.write("/api/v1/staff/logout", nil, ""); r.status != 500 {
		t.Fatalf("logout with a failing store %d", r.status)
	}
	fail = false
	b := e.browser()
	fail = true
	if r := b.write("/api/v1/staff/login", map[string]string{"email": "org@example.org", "password": staffPassword}, ""); r.status != 500 {
		t.Fatalf("renew failure %d %v", r.status, r.body)
	}
}
