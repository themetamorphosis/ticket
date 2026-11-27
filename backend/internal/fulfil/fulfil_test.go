package fulfil

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/delivery"
	"kacheem/backend/internal/orders"
	"kacheem/backend/internal/outbox"
	"kacheem/backend/internal/payments"
	"kacheem/backend/internal/payments/fakegw"
	"kacheem/backend/internal/pgtest"
	"kacheem/backend/internal/sealbox"
	"kacheem/backend/internal/tickets"
)

var ctx = context.Background()

// sink records what was "sent" and can fail on demand.
type sink struct {
	mu     sync.Mutex
	emails []delivery.Email
	sms    []string
	fail   error
}

func (s *sink) Send(_ context.Context, e delivery.Email) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return "", s.fail
	}
	s.emails = append(s.emails, e)
	return e.MessageID, nil
}

type smsSink struct{ *sink }

func (s smsSink) Send(_ context.Context, to, text string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return "", s.fail
	}
	s.sms = append(s.sms, to+"|"+text)
	return "sms-1", nil
}

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	f    *Fulfiller
	out  *sink
}

func setup(t *testing.T) *env {
	t.Helper()
	pool := pgtest.Pool(t)
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live", Shards: []int32{50, 50}, MaxPerOrder: 20})
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	box, _ := sealbox.New(k)
	out := &sink{}
	return &env{t: t, pool: pool, out: out, f: &Fulfiller{DB: pool, Issuer: &tickets.Issuer{Box: box}, Mailer: out,
		SMS: smsSink{out}, PublicOrigin: "https://tickets.test", GateOrigin: "https://gate.test"}}
}

// pay reserves qty places and pays through the fake gateway, issuing tickets
// in the payment transaction exactly as cmd/api wires it.
func (e *env) pay(buyer string, qty int) string {
	e.t.Helper()
	o, _, err := orders.Reserve(ctx, e.pool, orders.Request{EventSlug: "live", BuyerID: buyer, IdempotencyKey: "key-00000001",
		Input: orders.Input{Quantity: qty, Name: "Nusrat Jahan", Email: "nusrat@example.com", Phone: "+8801711000000", Age: 28}})
	if err != nil {
		e.t.Fatal(err)
	}
	gw := &fakegw.Gateway{DB: e.pool, BaseURL: "https://tickets.test"}
	svc := &payments.Service{DB: e.pool, Gateway: gw, ReturnURL: "https://tickets.test/payments/return/fake",
		OnPaid: func(ctx context.Context, tx pgx.Tx, id string) error { return e.f.Issuer.Issue(ctx, tx, id) }}
	redirect, err := svc.Checkout(ctx, buyer, o.Ref)
	if err != nil {
		e.t.Fatal(err)
	}
	u, _ := url.Parse(redirect)
	back, _ := gw.Decide(ctx, u.Path[len("/payments/fake/pay/"):], "approve")
	bu, _ := url.Parse(back)
	if _, err := svc.Return(ctx, bu.Query()); err != nil {
		e.t.Fatal(err)
	}
	return o.ID
}

// drain runs jobs until none are due.
func (e *env) drain() {
	e.t.Helper()
	for range 10 {
		n, err := e.f.Run(ctx, 10)
		if err != nil {
			e.t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
	e.t.Fatal("jobs never drained")
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// Exit gate (plan section 9): a paid order for three produces exactly three
// PDFs, one email carrying all three, and a pickup link by SMS.
func TestPaidOrderForThreeDeliversThreePDFs(t *testing.T) {
	e := setup(t)
	oid := e.pay("buyer-1", 3)
	if n := e.count(`SELECT count(*) FROM ticket WHERE order_id = $1`, oid); n != 3 {
		t.Fatalf("%d tickets", n)
	}
	e.drain()
	if n := e.count(`SELECT count(DISTINCT sha256) FROM ticket_document`); n != 3 {
		t.Fatalf("%d distinct PDFs", n)
	}
	if len(e.out.emails) != 1 {
		t.Fatalf("%d emails", len(e.out.emails))
	}
	m := e.out.emails[0]
	var names []string
	for _, a := range m.Attachments {
		names = append(names, a.Name)
		if !strings.HasPrefix(string(a.Data), "%PDF-") || a.ContentType != "application/pdf" {
			t.Fatalf("attachment %s", a.Name)
		}
	}
	if strings.Join(names, ",") != "ticket-1-of-3.pdf,ticket-2-of-3.pdf,ticket-3-of-3.pdf" ||
		m.To != "nusrat@example.com" || m.Subject != "Your 3 tickets for Test Concert" ||
		!regexp.MustCompile(`^<[0-9A-Z]{5}-[0-9A-Z]{5}\.1\.1@tickets\.test>$`).MatchString(m.MessageID) {
		t.Fatalf("email %v %+v", names, m)
	}

	if len(e.out.sms) != 1 {
		t.Fatalf("%d sms", len(e.out.sms))
	}
	to, text, _ := strings.Cut(e.out.sms[0], "|")
	secret := regexp.MustCompile(`https://tickets\.test/pickup/#([A-Za-z0-9_-]{32})$`).FindStringSubmatch(text)
	if to != "+8801711000000" || secret == nil {
		t.Fatalf("sms %q", e.out.sms[0])
	}
	sum := sha256.Sum256([]byte(secret[1]))
	if e.count(`SELECT count(*) FROM download_grant WHERE order_id = $1 AND secret_digest = $2 AND expires_at > now()`, oid, sum[:]) != 1 {
		t.Fatal("pickup grant not stored by digest")
	}
	if e.count(`SELECT count(*) FROM delivery_attempt WHERE order_id = $1 AND status = 'sent'`, oid) != 2 ||
		e.count(`SELECT count(*) FROM outbox_job WHERE status <> 'done'`) != 0 {
		t.Fatal("attempts or jobs not settled")
	}
	// The QR on each ticket points at the staff scanner with that ticket's credential.
	var sealedRow []byte
	_ = e.pool.QueryRow(ctx, `SELECT credential_sealed FROM ticket WHERE order_id = $1 AND ordinal = 1`, oid).Scan(&sealedRow)
	s, _ := e.f.Issuer.Open(sealedRow)
	if !strings.Contains(string(m.Attachments[0].Data), "/Subtype /Image") || s.Credential == "" {
		t.Fatal("ticket PDF without QR")
	}
}

func TestLargeOrdersAreSplitIntoNumberedEmails(t *testing.T) {
	e := setup(t)
	e.pay("buyer-1", 12)
	e.drain()
	if len(e.out.emails) != 2 || len(e.out.emails[0].Attachments) != 10 || len(e.out.emails[1].Attachments) != 2 ||
		!strings.HasSuffix(e.out.emails[1].Subject, "(email 2 of 2)") || e.out.emails[1].Attachments[1].Name != "ticket-12-of-12.pdf" {
		t.Fatalf("emails %d", len(e.out.emails))
	}
}

func TestFailedSendsAreRecordedAndRetried(t *testing.T) {
	e := setup(t)
	oid := e.pay("buyer-1", 1)
	e.out.fail = errors.New("relay unavailable")
	_, _ = e.f.Run(ctx, 10) // render
	_, _ = e.f.Run(ctx, 10) // both sends fail
	if e.count(`SELECT count(*) FROM delivery_attempt WHERE order_id = $1 AND status = 'failed' AND error = 'relay unavailable'`, oid) != 2 ||
		e.count(`SELECT count(*) FROM outbox_job WHERE status = 'ready' AND attempts = 1 AND run_at > now()`) != 2 {
		t.Fatal("failure not recorded or not rescheduled")
	}
	e.out.fail = nil
	_, _ = e.pool.Exec(ctx, `UPDATE outbox_job SET run_at = now()`)
	e.drain()
	if len(e.out.emails) != 1 || len(e.out.sms) != 1 {
		t.Fatalf("after retry: %d emails %d sms", len(e.out.emails), len(e.out.sms))
	}
}

func TestEmailWaitsForEveryPDF(t *testing.T) {
	e := setup(t)
	oid := e.pay("buyer-1", 2)
	if err := e.f.email(ctx, oid); !errors.Is(err, ErrNotReady) {
		t.Fatalf("err = %v", err)
	}
}

func TestJobErrors(t *testing.T) {
	e := setup(t)
	oid := e.pay("buyer-1", 1)
	if err := e.f.handle(ctx, outbox.Job{Kind: "teleport", OrderID: oid}); err == nil {
		t.Fatal("unknown kind accepted")
	}
	// Tickets sealed with another key cannot be rendered.
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	other, _ := sealbox.New(k)
	e.f.Issuer = &tickets.Issuer{Box: other}
	if err := e.f.render(ctx, oid); !errors.Is(err, tickets.ErrSealed) {
		t.Fatalf("wrong key: %v", err)
	}
	// A bad event time zone still delivers, in UTC.
	if got := when(time.Date(2027, 2, 14, 13, 30, 0, 0, time.UTC), "Mars/Olympus"); got != "Sun 14 Feb 2027, 13:30 UTC" {
		t.Fatalf("when %q", got)
	}
}

func TestRenderErrorIsRetried(t *testing.T) {
	e := setup(t)
	oid := e.pay("buyer-1", 1)
	_, _ = e.pool.Exec(ctx, `UPDATE event SET time_zone = 'Mars/Olympus'`)
	if err := e.f.render(ctx, oid); err == nil {
		t.Fatal("render with unknown zone succeeded")
	}
}

func TestResendIsRateLimited(t *testing.T) {
	e := setup(t)
	oid := e.pay("buyer-1", 1)
	e.drain()
	if err := Resend(ctx, e.pool, oid); err != nil {
		t.Fatal(err)
	}
	if err := Resend(ctx, e.pool, oid); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("second resend: %v", err)
	}
	e.drain()
	if len(e.out.emails) != 2 || len(e.out.sms) != 2 {
		t.Fatalf("%d emails %d sms", len(e.out.emails), len(e.out.sms))
	}
	if e.out.emails[0].MessageID == e.out.emails[1].MessageID {
		t.Fatal("resend reused the Message-ID")
	}
}

func TestEveryFailurePointIsRetried(t *testing.T) {
	e := setup(t)
	oid := e.pay("buyer-1", 2)
	with := func(db dbx.DB) *Fulfiller { f := *e.f; f.DB = db; return &f }

	// Each job handler reports every database failure so the job is retried.
	pgtest.EachFault(t, e.pool, func(db dbx.DB) error { return with(db).render(ctx, oid) }, func(int, error) {})
	pgtest.EachFault(t, e.pool, func(db dbx.DB) error { return with(db).email(ctx, oid) }, func(int, error) {})
	pgtest.EachFault(t, e.pool, func(db dbx.DB) error { return with(db).sms(ctx, oid) }, func(int, error) {})
	pgtest.EachFault(t, e.pool, func(db dbx.DB) error { return Resend(ctx, db, oid) }, func(int, error) {})

	// Run absorbs a job's failure by rescheduling it, but its own queue
	// operations (claim, retry, done) surface errors. Fail each call in turn.
	e.out.fail = errors.New("relay down") // so some jobs take the Retry path
	for failAt := 1; ; failAt++ {
		_, _ = e.pool.Exec(ctx, `UPDATE outbox_job SET status = 'ready', run_at = now(), lease_until = NULL`)
		f := pgtest.NewFaulty(e.pool, failAt)
		_, _ = with(f).Run(ctx, 10)
		if !f.Injected() {
			break
		}
	}
	e.out.fail = nil
	_, _ = e.pool.Exec(ctx, `UPDATE outbox_job SET status = 'ready', run_at = now(), lease_until = NULL WHERE status <> 'done'`)
	e.drain()
	if e.count(`SELECT count(*) FROM outbox_job WHERE status <> 'done'`) != 0 {
		t.Fatal("jobs left over")
	}
}

func TestDeliveriesShowsLatestPerChannel(t *testing.T) {
	e := setup(t)
	oid := e.pay("buyer-1", 1)
	if d, err := Deliveries(ctx, e.pool, oid); err != nil || len(d) != 0 {
		t.Fatalf("%v %v", d, err)
	}
	e.out.fail = errors.New("down")
	_, _ = e.f.Run(ctx, 10)
	_, _ = e.f.Run(ctx, 10)
	e.out.fail = nil
	_, _ = e.pool.Exec(ctx, `UPDATE outbox_job SET run_at = now()`)
	e.drain()
	d, err := Deliveries(ctx, e.pool, oid)
	if err != nil || len(d) != 2 || d[0].Channel != "email" || d[0].Status != "sent" || d[1].Channel != "sms" || d[1].Status != "sent" {
		t.Fatalf("%+v %v", d, err)
	}
	pgtest.EachFault(t, e.pool, func(db dbx.DB) error { _, err := Deliveries(ctx, db, oid); return err }, func(int, error) {})
}
