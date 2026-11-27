package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"kacheem/backend/internal/pgtest"
)

func getter(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestPublishCommand(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.CreateEvent(t, pool, pgtest.Event{Slug: "live", Status: "draft"})
	dir := t.TempDir()
	env := getter(map[string]string{"DATABASE_URL": pool.Config().ConnString(), "PUBLISH_DIR": dir})
	var out bytes.Buffer
	if err := run(context.Background(), []string{"live"}, env, &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "published live revision 1") {
		t.Fatalf("out = %q", out.String())
	}
	if _, err := os.Stat(dir + "/event.json"); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"missing"}, env, io.Discard); err == nil {
		t.Fatal("missing event published")
	}
}

func TestPublishCommandFailures(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		env  map[string]string
	}{
		"usage":         {nil, nil},
		"config":        {[]string{"live"}, nil},
		"database down": {[]string{"live"}, map[string]string{"DATABASE_URL": "postgres://app:x@127.0.0.1:1/x?connect_timeout=1", "PUBLISH_DIR": "/tmp"}},
	} {
		if err := run(context.Background(), c.args, getter(c.env), io.Discard); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if err := run(context.Background(), nil, getter(nil), io.Discard); !errors.Is(err, errUsage) {
		t.Fatalf("usage err = %v", err)
	}
}

func TestMainExitsNonZeroOnBadUsage(t *testing.T) {
	origArgs, origExit := os.Args, exit
	defer func() { os.Args, exit = origArgs, origExit }()
	os.Args = []string{"publish"}
	code := -1
	exit = func(c int) { code = c }
	main()
	if code != 1 {
		t.Fatalf("exit code %d", code)
	}
}
