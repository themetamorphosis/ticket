package pgtest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// fakeTB records failures instead of stopping the test.
type fakeTB struct {
	failed   []string
	cleanups []func()
}

func (f *fakeTB) Helper()                           {}
func (f *fakeTB) Fatalf(format string, args ...any) { f.failed = append(f.failed, fmt.Sprintf(format, args...)) }
func (f *fakeTB) Cleanup(fn func())                 { f.cleanups = append(f.cleanups, fn) }
func (f *fakeTB) runCleanups() {
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
}

func exists(t *testing.T, name string) bool {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), AdminURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	var n int
	if err := conn.QueryRow(context.Background(), "select count(*) from pg_database where datname = $1", name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestNewDatabaseIsUsableAndDroppedOnCleanup(t *testing.T) {
	f := &fakeTB{}
	u := NewDatabase(f)
	if len(f.failed) > 0 {
		t.Fatalf("failed: %v", f.failed)
	}
	conn, err := pgx.Connect(context.Background(), u)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	var name string
	if err := conn.QueryRow(context.Background(), "select current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	conn.Close(context.Background())
	if !strings.HasPrefix(name, "test_") || !exists(t, name) {
		t.Fatalf("database %q not created", name)
	}
	f.runCleanups()
	if exists(t, name) {
		t.Fatalf("database %q not dropped", name)
	}
}

func TestAdminURLRequiresVariable(t *testing.T) {
	t.Setenv("TEST_DATABASE_URL", "")
	f := &fakeTB{}
	if AdminURL(f) != "" || len(f.failed) != 1 {
		t.Fatalf("failed = %v", f.failed)
	}
}

func TestNewDatabaseReportsConnectFailure(t *testing.T) {
	t.Setenv("TEST_DATABASE_URL", "postgres://app:x@127.0.0.1:1/tickets?connect_timeout=1")
	f := &fakeTB{}
	if NewDatabase(f) != "" || len(f.failed) != 1 || !strings.Contains(f.failed[0], "connect") {
		t.Fatalf("failed = %v", f.failed)
	}
}

func TestNewDatabaseReportsCreateFailure(t *testing.T) {
	// A read-only session default makes the server reject CREATE DATABASE.
	t.Setenv("TEST_DATABASE_URL", AdminURL(t)+"&options=-c%20default_transaction_read_only%3Don")
	f := &fakeTB{}
	if NewDatabase(f) != "" || len(f.failed) != 1 || !strings.Contains(f.failed[0], "create database") {
		t.Fatalf("failed = %v", f.failed)
	}
	f.runCleanups()
}
