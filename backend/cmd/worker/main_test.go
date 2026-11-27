package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func getter(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestRunUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var logs bytes.Buffer
	if err := run(ctx, getter(map[string]string{"DATABASE_URL": os.Getenv("TEST_DATABASE_URL")}), &logs); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, msg := range []string{"worker started", "worker stopped"} {
		if !strings.Contains(logs.String(), msg) {
			t.Errorf("logs missing %q: %s", msg, logs.String())
		}
	}
}

func TestRunFailures(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"missing config": {},
		"database down":  {"DATABASE_URL": "postgres://app:x@127.0.0.1:1/x?connect_timeout=1"},
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
