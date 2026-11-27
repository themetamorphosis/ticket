package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"kacheem/backend/internal/orders"
	"kacheem/backend/internal/pgtest"
)

func getter(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// syncBuffer lets the test read logs while the worker goroutine writes them.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for range 200 {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

func TestWorkerReleasesExpiredHolds(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live"})
	o, _, err := orders.Reserve(ctx, pool, orders.Request{EventSlug: "live", BuyerID: "b", IdempotencyKey: "key-00000001",
		Input: orders.Input{Quantity: 2, Name: "A Buyer", Email: "a@example.com", Phone: "+8801711000000", Age: 30}})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = pool.Exec(ctx, `UPDATE ticket_order SET expires_at = now() - interval '1 second'`)

	runCtx, cancel := context.WithCancel(ctx)
	var logs syncBuffer
	done := make(chan error, 1)
	go func() {
		done <- run(runCtx, getter(map[string]string{"DATABASE_URL": pool.Config().ConnString(), "PAYMENT_PROVIDER": "fake", "EXPIRY_INTERVAL": "20ms",
			"PUBLIC_ORIGIN": "https://tickets.test", "TICKET_KEY": "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
			"GATE_ORIGIN": "https://gate.test", "EMAIL_PROVIDER": "fake", "SMS_PROVIDER": "fake"}), &logs)
	}()
	waitFor(t, func() bool { return strings.Contains(logs.String(), "released expired holds") })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
	got, _ := orders.Get(ctx, pool, "b", o.Ref)
	if got.Status != "expired" || !strings.Contains(logs.String(), "worker stopped") {
		t.Fatalf("status %s logs %s", got.Status, logs.String())
	}
}

func TestWorkerKeepsRunningWhenReleaseFails(t *testing.T) {
	// An unmigrated database makes every release attempt fail.
	url := pgtest.NewDatabase(t)
	ctx, cancel := context.WithCancel(context.Background())
	var logs syncBuffer
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, getter(map[string]string{"DATABASE_URL": url, "PAYMENT_PROVIDER": "fake", "EXPIRY_INTERVAL": "20ms",
			"PUBLIC_ORIGIN": "https://tickets.test", "TICKET_KEY": "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
			"GATE_ORIGIN": "https://gate.test", "EMAIL_PROVIDER": "fake", "SMS_PROVIDER": "fake"}), &logs)
	}()
	waitFor(t, func() bool {
		l := logs.String()
		return strings.Count(l, "released expired holds failed") >= 2 && strings.Contains(l, "reconciled payments failed") &&
			strings.Contains(l, "processed refunds failed") && strings.Contains(l, "ran fulfilment jobs failed")
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestRunFailures(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"missing config": {},
		"database down": {"DATABASE_URL": "postgres://app:x@127.0.0.1:1/x?connect_timeout=1", "PAYMENT_PROVIDER": "fake",
			"PUBLIC_ORIGIN": "https://tickets.test", "TICKET_KEY": "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
			"GATE_ORIGIN": "https://gate.test", "EMAIL_PROVIDER": "fake", "SMS_PROVIDER": "fake"},
	} {
		if err := run(context.Background(), getter(env), io.Discard); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestMainExitsNonZeroOnBadConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	code := -1
	orig := exit
	defer func() { exit = orig }()
	exit = func(c int) { code = c }
	main()
	if code != 1 {
		t.Fatalf("exit code %d", code)
	}
}
