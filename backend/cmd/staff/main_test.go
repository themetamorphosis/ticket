package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"kacheem/backend/internal/pgtest"
)

func getter(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestInvite(t *testing.T) {
	pool := pgtest.Pool(t)
	env := getter(map[string]string{"DATABASE_URL": pool.Config().ConnString(), "GATE_ORIGIN": "https://gate.test"})
	var out bytes.Buffer
	if err := run(context.Background(), []string{"invite", "org@example.org", "Org One", "organizer"}, env, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "https://gate.test/invite/#") {
		t.Fatalf("out %q", out.String())
	}
	if err := run(context.Background(), []string{"invite", "x", "X", "king"}, env, io.Discard); err == nil {
		t.Fatal("bad role accepted")
	}
}

func TestFailures(t *testing.T) {
	pool := pgtest.Pool(t)
	for name, c := range map[string]struct {
		args []string
		env  map[string]string
	}{
		"usage":         {[]string{"remove", "a", "b", "c"}, nil},
		"config":        {[]string{"invite", "a", "b", "c"}, nil},
		"database down": {[]string{"invite", "a", "b", "c"}, map[string]string{"DATABASE_URL": "postgres://app:x@127.0.0.1:1/x?connect_timeout=1"}},
		"bad origin":    {[]string{"invite", "a", "b", "c"}, map[string]string{"DATABASE_URL": pool.Config().ConnString(), "GATE_ORIGIN": "::x"}},
	} {
		if err := run(context.Background(), c.args, getter(c.env), io.Discard); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestMainExitsNonZeroOnBadUsage(t *testing.T) {
	origArgs, origExit := os.Args, exit
	defer func() { os.Args, exit = origArgs, origExit }()
	os.Args = []string{"staff"}
	code := -1
	exit = func(c int) { code = c }
	main()
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
}
