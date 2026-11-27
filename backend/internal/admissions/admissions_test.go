package admissions

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/orders"
	"kacheem/backend/internal/pgtest"
	"kacheem/backend/internal/postgres"
	"kacheem/backend/internal/sealbox"
	"kacheem/backend/internal/tickets"
)

var ctx = context.Background()

const gateOrigin = "https://gate.test"

type env struct {
	t       *testing.T
	pool    *pgxpool.Pool
	iss     *tickets.Issuer
	eventID string
	gate    string
	gate2   string
	staff   string
	n       int
}

func setup(t *testing.T) *env {
	t.Helper()
	pool := pgtest.Pool(t)
	ids := pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live", Shards: []int32{100, 100}, MaxPerOrder: 20})
	// The fixture event starts in 30 days; open the doors now for most tests.
	_, _ = pool.Exec(ctx, `UPDATE event SET admission_opens_at = now() - interval '1 hour', admission_closes_at = now() + interval '1 day'`)
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	box, _ := sealbox.New(k)
	e := &env{t: t, pool: pool, iss: &tickets.Issuer{Box: box}, eventID: ids.EventID}
	_ = pool.QueryRow(ctx, `INSERT INTO staff (email, name, role) VALUES ('gate@example.org', 'Gate One', 'gate') RETURNING id`).Scan(&e.staff)
	_ = pool.QueryRow(ctx, `INSERT INTO gate (event_id, name) VALUES ($1, 'North') RETURNING id`, ids.EventID).Scan(&e.gate)
	_ = pool.QueryRow(ctx, `INSERT INTO gate (event_id, name) VALUES ($1, 'South') RETURNING id`, ids.EventID).Scan(&e.gate2)
	return e
}

// ticket issues one ticket and returns its secrets and ID.
func (e *env) ticket() (tickets.Secrets, string) {
	e.t.Helper()
	e.n++
	o, _, err := orders.Reserve(ctx, e.pool, orders.Request{EventSlug: "live", BuyerID: fmt.Sprintf("b%d", e.n), IdempotencyKey: "key-00000001",
		Input: orders.Input{Quantity: 1, Name: "A Buyer", Email: "a@example.com", Phone: "+8801711000000", Age: 30}})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.iss.Issue(ctx, e.pool, o.ID); err != nil {
		e.t.Fatal(err)
	}
	var id string
	var sealed []byte
	_ = e.pool.QueryRow(ctx, `SELECT id, credential_sealed FROM ticket WHERE order_id = $1`, o.ID).Scan(&id, &sealed)
	s, _ := e.iss.Open(sealed)
	return s, id
}

func scan(device, request, gate, staff, cred string) Scan {
	return Scan{DeviceID: device, RequestID: request, GateID: gate, StaffID: staff, QR: gateOrigin + "/scan#" + cred}
}

func TestFirstScanAdmitsAndLaterScansSeeIt(t *testing.T) {
	e := setup(t)
	s, _ := e.ticket()
	first, err := Admit(ctx, e.pool, gateOrigin, scan("device-north", "request-0001", e.gate, e.staff, s.Credential))
	if err != nil || first.Outcome != Admitted || first.Replay || first.TicketLabel != "General admission · ticket 1 of 1" ||
		first.OrderRef == "" || first.DecidedAt.IsZero() {
		t.Fatalf("first %+v %v", first, err)
	}
	second, _ := Admit(ctx, e.pool, gateOrigin, scan("device-south", "request-0002", e.gate2, e.staff, s.Credential))
	if second.Outcome != AlreadyUsed || second.FirstGate != "North" || !second.FirstAt.Equal(first.DecidedAt) {
		t.Fatalf("second %+v", second)
	}
	// A device retrying a request whose answer was lost gets the same answer.
	retry, _ := Admit(ctx, e.pool, gateOrigin, scan("device-north", "request-0001", e.gate, e.staff, s.Credential))
	if retry.Outcome != Admitted || !retry.Replay || !retry.DecidedAt.Equal(first.DecidedAt) || retry.FirstGate != "North" {
		t.Fatalf("retry %+v", retry)
	}
	var admissions, attempts int
	_ = e.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM admission), (SELECT count(*) FROM scan_attempt)`).Scan(&admissions, &attempts)
	if admissions != 1 || attempts != 2 {
		t.Fatalf("%d admissions, %d attempts", admissions, attempts)
	}
	counts, err := GateCounts(ctx, e.pool, e.eventID)
	if err != nil || len(counts) != 2 || counts[0] != (Counts{"North", 1, 0}) || counts[1] != (Counts{"South", 0, 1}) {
		t.Fatalf("counts %+v %v", counts, err)
	}
}

func TestTypedFallbackCode(t *testing.T) {
	e := setup(t)
	s, _ := e.ticket()
	typed := strings.ToLower(strings.ReplaceAll(s.Fallback, "-", " "))
	res, err := Admit(ctx, e.pool, gateOrigin, Scan{DeviceID: "device-north", RequestID: "request-0001", GateID: e.gate, StaffID: e.staff, Code: typed})
	if err != nil || res.Outcome != Admitted {
		t.Fatalf("%+v %v", res, err)
	}
	var method string
	_ = e.pool.QueryRow(ctx, `SELECT method FROM scan_attempt`).Scan(&method)
	if method != "code" {
		t.Fatal(method)
	}
}

func TestMalformedScansAreRejectedWithoutRecording(t *testing.T) {
	e := setup(t)
	s, _ := e.ticket()
	bad := []Scan{
		{QR: "https://evil.test/scan#" + s.Credential},
		{QR: gateOrigin + "/other#" + s.Credential},
		{QR: gateOrigin + "/scan#short"},
		{QR: ""},
		{Code: "ABCD-EFGH"},
		{Code: s.Fallback[:len(s.Fallback)-1] + map[bool]string{true: "0", false: "1"}[!strings.HasSuffix(s.Fallback, "0")]},
	}
	for _, b := range bad {
		b.DeviceID, b.RequestID, b.GateID, b.StaffID = "device-north", "request-0001", e.gate, e.staff
		if _, err := Admit(ctx, e.pool, gateOrigin, b); !errors.Is(err, ErrBadScan) {
			t.Errorf("%+v: err = %v", b, err)
		}
	}
	var attempts int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM scan_attempt`).Scan(&attempts)
	if attempts != 0 {
		t.Fatal("malformed scans were recorded")
	}
}

func TestRefusals(t *testing.T) {
	e := setup(t)
	var otherGate string
	ids := pgtest.CreateEvent(t, e.pool, pgtest.Event{Slug: "other"})
	_ = e.pool.QueryRow(ctx, `INSERT INTO gate (event_id, name) VALUES ($1, 'Other') RETURNING id`, ids.EventID).Scan(&otherGate)

	valid, _ := e.ticket()
	voided, voidID := e.ticket()
	refunding, refundID := e.ticket()
	_, _ = e.pool.Exec(ctx, `UPDATE ticket SET status = 'void' WHERE id = $1`, voidID)
	_, _ = e.pool.Exec(ctx, `UPDATE ticket SET status = 'refund_pending' WHERE id = $1`, refundID)
	unknown := tickets.NewCredential()

	cases := []struct {
		name, gate, cred, want string
	}{
		{"unknown", e.gate, unknown, Unknown},
		{"wrong event", otherGate, valid.Credential, WrongEvent},
		{"void", e.gate, voided.Credential, Void},
		{"refund pending", e.gate, refunding.Credential, Void},
	}
	for i, c := range cases {
		res, err := Admit(ctx, e.pool, gateOrigin, scan("device-north", fmt.Sprintf("request-%04d", i), c.gate, e.staff, c.cred))
		if err != nil || res.Outcome != c.want {
			t.Errorf("%s: %+v %v", c.name, res, err)
		}
	}
	// A retry of the unknown scan is answered from the record.
	res, _ := Admit(ctx, e.pool, gateOrigin, scan("device-north", "request-0000", e.gate, e.staff, unknown))
	if res.Outcome != Unknown || !res.Replay || res.TicketLabel != "" {
		t.Fatalf("unknown replay %+v", res)
	}

	_, _ = e.pool.Exec(ctx, `UPDATE event SET admission_opens_at = now() + interval '1 hour'`)
	if res, _ := Admit(ctx, e.pool, gateOrigin, scan("device-north", "request-1001", e.gate, e.staff, valid.Credential)); res.Outcome != NotOpen {
		t.Fatalf("not open: %+v", res)
	}
	_, _ = e.pool.Exec(ctx, `UPDATE event SET admission_opens_at = now() - interval '2 hours', admission_closes_at = now() - interval '1 hour'`)
	if res, _ := Admit(ctx, e.pool, gateOrigin, scan("device-north", "request-1002", e.gate, e.staff, valid.Credential)); res.Outcome != Closed {
		t.Fatalf("closed: %+v", res)
	}
	if _, err := Admit(ctx, e.pool, gateOrigin, scan("device-north", "request-1003", "00000000-0000-0000-0000-000000000000", e.staff, valid.Credential)); !errors.Is(err, ErrGate) {
		t.Fatalf("unknown gate: %v", err)
	}
}

// Exit gate (plan section 9): devices scanning one QR at the same moment.
// Exactly one admission survives, every time.
func TestSimultaneousScansAdmitExactlyOnce(t *testing.T) {
	e := setup(t)
	wide, err := postgres.Open(ctx, e.pool.Config().ConnString()+"&pool_max_conns=20")
	if err != nil {
		t.Fatal(err)
	}
	defer wide.Close()
	for round := range 20 {
		s, _ := e.ticket()
		var mu sync.Mutex
		outcomes := map[string]int{}
		var wg sync.WaitGroup
		start := make(chan struct{})
		for d := range 6 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				gate := e.gate
				if d%2 == 1 {
					gate = e.gate2
				}
				res, err := Admit(ctx, wide, gateOrigin, scan(fmt.Sprintf("device-%04d", d), fmt.Sprintf("round-%04d", round), gate, e.staff, s.Credential))
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					outcomes["error: "+err.Error()]++
					return
				}
				outcomes[res.Outcome]++
			}()
		}
		close(start)
		wg.Wait()
		if outcomes[Admitted] != 1 || outcomes[AlreadyUsed] != 5 {
			t.Fatalf("round %d: %v", round, outcomes)
		}
	}
}

// The same request sent twice at once (a double tap) is decided once.
func TestDuplicateRequestInFlightIsDecidedOnce(t *testing.T) {
	e := setup(t)
	s, _ := e.ticket()
	var wg sync.WaitGroup
	results := make([]Result, 4)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], _ = Admit(ctx, e.pool, gateOrigin, scan("device-north", "request-0001", e.gate, e.staff, s.Credential))
		}()
	}
	wg.Wait()
	replays := 0
	for _, r := range results {
		if r.Outcome != Admitted {
			t.Fatalf("results %+v", results)
		}
		if r.Replay {
			replays++
		}
	}
	if replays != 3 {
		t.Fatalf("%d replays", replays)
	}
}

func TestEveryFailurePointLeavesNoHalfAdmission(t *testing.T) {
	e := setup(t)
	n := 0
	pgtest.EachFault(t, e.pool, func(db dbx.DB) error {
		n++
		s, _ := e.ticket()
		_, err := Admit(ctx, db, gateOrigin, scan("device-north", fmt.Sprintf("request-%04d", n), e.gate, e.staff, s.Credential))
		return err
	}, func(int, error) {
		var bad int
		_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM ticket t WHERE (t.status = 'used') <> EXISTS (SELECT 1 FROM admission a WHERE a.ticket_id = t.id)`).Scan(&bad)
		if bad != 0 {
			t.Fatalf("%d tickets with a half-recorded admission", bad)
		}
	})
	// Replays of used tickets under faults.
	s, _ := e.ticket()
	_, _ = Admit(ctx, e.pool, gateOrigin, scan("device-north", "request-9999", e.gate, e.staff, s.Credential))
	pgtest.EachFault(t, e.pool, func(db dbx.DB) error {
		_, err := Admit(ctx, db, gateOrigin, scan("device-north", "request-9999", e.gate, e.staff, s.Credential))
		return err
	}, func(int, error) {})
	pgtest.EachFault(t, e.pool, func(db dbx.DB) error { _, err := GateCounts(ctx, db, e.eventID); return err }, func(int, error) {})
}
