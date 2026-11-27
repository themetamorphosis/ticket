package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"kacheem/backend/internal/pgtest"
	"kacheem/backend/migrations"
)

func getter(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

const unreachable = "postgres://app:x@127.0.0.1:1/x?connect_timeout=1"

func TestUpStatusDown(t *testing.T) {
	ctx := context.Background()
	env := getter(map[string]string{"DATABASE_URL": pgtest.NewDatabase(t)})
	steps := []struct{ cmd, want string }{
		{"status", "pending  00001_audit_event.sql"},
		{"up", "OK    up 00001_audit_event.sql"},
		{"status", "applied  00001_audit_event.sql"},
		{"down", "OK    down 00001_audit_event.sql"},
	}
	for _, s := range steps {
		var out bytes.Buffer
		if err := run(ctx, []string{s.cmd}, env, &out, migrations.Files); err != nil {
			t.Fatalf("%s: %v", s.cmd, err)
		}
		if !strings.Contains(out.String(), s.want) {
			t.Fatalf("%s output %q, want %q", s.cmd, out.String(), s.want)
		}
	}
	if err := run(ctx, []string{"down"}, env, io.Discard, migrations.Files); err == nil {
		t.Fatal("down with nothing applied should fail")
	}
}

func TestRunFailures(t *testing.T) {
	ctx := context.Background()
	good := getter(map[string]string{"DATABASE_URL": pgtest.AdminURL(t)})
	broken := fstest.MapFS{
		"00001_a.sql": {Data: []byte("-- +goose Up\nselect 1;\n")},
		"00001_b.sql": {Data: []byte("-- +goose Up\nselect 1;\n")},
	}
	cases := []struct {
		name string
		args []string
		env  func(string) string
		fsys fstest.MapFS
	}{
		{"no args", nil, good, nil},
		{"unknown command", []string{"sideways"}, good, nil},
		{"missing config", []string{"up"}, getter(nil), nil},
		{"bad url", []string{"up"}, getter(map[string]string{"DATABASE_URL": "postgres://%zz"}), nil},
		{"broken migrations", []string{"up"}, good, broken},
		{"up unreachable", []string{"up"}, getter(map[string]string{"DATABASE_URL": unreachable}), nil},
		{"down unreachable", []string{"down"}, getter(map[string]string{"DATABASE_URL": unreachable}), nil},
		{"status unreachable", []string{"status"}, getter(map[string]string{"DATABASE_URL": unreachable}), nil},
	}
	for _, c := range cases {
		var fsys = migrations.Files
		if c.fsys != nil {
			fsys = c.fsys
		}
		if err := run(ctx, c.args, c.env, io.Discard, fsys); err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}
	if err := run(ctx, nil, good, io.Discard, migrations.Files); !errors.Is(err, errUsage) {
		t.Errorf("no args: err = %v, want usage", err)
	}
}

func TestMainExitsNonZeroOnBadUsage(t *testing.T) {
	origArgs, origExit := os.Args, exit
	defer func() { os.Args, exit = origArgs, origExit }()
	os.Args = []string{"migrate"}
	code := -1
	exit = func(c int) { code = c }
	main()
	if code != 1 {
		t.Fatalf("exit code %d", code)
	}
}
