package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("TEST_DATABASE_URL")
	if u == "" {
		t.Fatal("TEST_DATABASE_URL is not set; run tests with `make go-test`")
	}
	return u
}

func TestOpenConnectsWithPgBouncerSafeExecMode(t *testing.T) {
	pool, err := Open(context.Background(), testURL(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	if mode := pool.Config().ConnConfig.DefaultQueryExecMode; mode != pgx.QueryExecModeExec {
		t.Fatalf("exec mode = %v", mode)
	}
	var tz string
	if err := pool.QueryRow(context.Background(), "show timezone").Scan(&tz); err != nil || tz != "UTC" {
		t.Fatalf("timezone = %q, err %v", tz, err)
	}
}

func TestOpenRejectsBadURL(t *testing.T) {
	if _, err := Open(context.Background(), "postgres://%zz"); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenFailsWhenUnreachable(t *testing.T) {
	_, err := Open(context.Background(), "postgres://app:x@127.0.0.1:1/tickets?connect_timeout=1")
	if err == nil || !strings.Contains(err.Error(), "connect") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenReportsPoolConstructionFailure(t *testing.T) {
	orig := newPool
	defer func() { newPool = orig }()
	newPool = func(context.Context, *pgxpool.Config) (*pgxpool.Pool, error) { return nil, errors.New("boom") }
	if _, err := Open(context.Background(), testURL(t)); err == nil || !strings.Contains(err.Error(), "create database pool") {
		t.Fatalf("err = %v", err)
	}
}
