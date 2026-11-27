package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func baseEnv(t *testing.T) map[string]string {
	dir := t.TempDir()
	return map[string]string{
		"DATABASE_URL":      os.Getenv("TEST_DATABASE_URL"),
		"REDIS_URL":         os.Getenv("TEST_REDIS_URL"),
		"API_PUBLIC_LISTEN": "unix:" + filepath.Join(dir, "public.sock"),
		"API_STAFF_LISTEN":  "unix:" + filepath.Join(dir, "staff.sock"),
		"PUBLIC_ORIGIN":     "https://tickets.test",
		"PAYMENT_PROVIDER":  "fake",
		"TICKET_KEY":        "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
		"GATE_ORIGIN":       "https://gate.test",
		"EMAIL_PROVIDER":    "fake",
		"SMS_PROVIDER":      "fake",
		"SHUTDOWN_TIMEOUT":  "2s",
	}
}

func getter(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func unixClient(sock string) *http.Client {
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("socket %s never appeared", path)
}

func TestRunServesBothListenersUntilCancelled(t *testing.T) {
	env := baseEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	var logs bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- run(ctx, getter(env), &logs) }()

	for _, key := range []string{"API_PUBLIC_LISTEN", "API_STAFF_LISTEN"} {
		sock := strings.TrimPrefix(env[key], "unix:")
		waitForSocket(t, sock)
		resp, err := unixClient(sock).Get("http://api/api/v1/ready")
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || body["status"] != "ready" {
			t.Fatalf("%s: %d %v", key, resp.StatusCode, body)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(logs.String(), `"msg":"api listening"`) || !strings.Contains(logs.String(), `"msg":"api stopped"`) {
		t.Fatalf("logs = %s", logs.String())
	}
}

func TestRunFailures(t *testing.T) {
	cases := map[string]func(map[string]string){
		"missing config":  func(m map[string]string) { delete(m, "DATABASE_URL") },
		"database down":   func(m map[string]string) { m["DATABASE_URL"] = "postgres://app:x@127.0.0.1:1/x?connect_timeout=1" },
		"bad redis url":   func(m map[string]string) { m["REDIS_URL"] = "http://nope" },
		"bad public addr": func(m map[string]string) { m["API_PUBLIC_LISTEN"] = "udp:1" },
		"bad staff addr":  func(m map[string]string) { m["API_STAFF_LISTEN"] = "udp:1" },
	}
	for name, mutate := range cases {
		env := baseEnv(t)
		mutate(env)
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
