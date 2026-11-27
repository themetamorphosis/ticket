// Package pgtest gives integration tests a fresh, throwaway PostgreSQL
// database on the real server (never SQLite or mocks), so concurrency and
// constraint tests exercise the production engine.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"

	"github.com/jackc/pgx/v5"
)

// TB is the subset of testing.TB that pgtest needs.
type TB interface {
	Helper()
	Fatalf(format string, args ...any)
	Cleanup(func())
}

// AdminURL returns TEST_DATABASE_URL or fails the test.
func AdminURL(t TB) string {
	t.Helper()
	u := os.Getenv("TEST_DATABASE_URL")
	if u == "" {
		t.Fatalf("TEST_DATABASE_URL is not set; run tests with `make go-test`")
	}
	return u
}

// NewDatabase creates an empty database, drops it when the test ends and
// returns its URL.
func NewDatabase(t TB) string {
	t.Helper()
	admin := AdminURL(t)
	ctx := context.Background()
	// The admin connection stays open until cleanup, so dropping the
	// database never depends on reconnecting.
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("pgtest: connect: %v", err)
		return ""
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	name := "test_" + hex.EncodeToString(b)
	t.Cleanup(func() {
		_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		_ = conn.Close(ctx)
	})
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("pgtest: create database: %v", err)
		return ""
	}

	u, _ := url.Parse(admin)
	u.Path = "/" + name
	return u.String()
}
