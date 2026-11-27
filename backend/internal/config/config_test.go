package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func fullAPIEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":       "postgres://db/tickets",
		"REDIS_URL":          "redis://redis:6379/0",
		"API_PUBLIC_LISTEN":  "unix:/run/kacheem/public.sock",
		"API_STAFF_LISTEN":   "unix:/run/kacheem/staff.sock",
		"PUBLIC_ORIGIN":      "https://tickets.example.org",
		"SHUTDOWN_TIMEOUT":   "7s",
		"UNRELATED_VARIABLE": "ignored",
	}
}

func TestLoadAPIReadsEveryField(t *testing.T) {
	cfg, err := LoadAPI(env(fullAPIEnv()))
	if err != nil {
		t.Fatalf("LoadAPI: %v", err)
	}
	want := API{
		DatabaseURL:     "postgres://db/tickets",
		RedisURL:        "redis://redis:6379/0",
		PublicListen:    "unix:/run/kacheem/public.sock",
		StaffListen:     "unix:/run/kacheem/staff.sock",
		PublicOrigin:    "https://tickets.example.org",
		ShutdownTimeout: 7 * time.Second,
	}
	if cfg != want {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadAPIDefaultsShutdownTimeout(t *testing.T) {
	m := fullAPIEnv()
	delete(m, "SHUTDOWN_TIMEOUT")
	cfg, err := LoadAPI(env(m))
	if err != nil {
		t.Fatalf("LoadAPI: %v", err)
	}
	if cfg.ShutdownTimeout != DefaultShutdownTimeout {
		t.Fatalf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, DefaultShutdownTimeout)
	}
}

func TestLoadAPIReportsAllMissingVariables(t *testing.T) {
	_, err := LoadAPI(env(nil))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, name := range []string{"DATABASE_URL", "REDIS_URL", "API_PUBLIC_LISTEN", "API_STAFF_LISTEN", "PUBLIC_ORIGIN"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %s", err, name)
		}
	}
}

func TestLoadAPIRejectsBadShutdownTimeout(t *testing.T) {
	for _, v := range []string{"soon", "0s", "-1s"} {
		m := fullAPIEnv()
		m["SHUTDOWN_TIMEOUT"] = v
		_, err := LoadAPI(env(m))
		if err == nil || !strings.Contains(err.Error(), "SHUTDOWN_TIMEOUT") {
			t.Errorf("SHUTDOWN_TIMEOUT=%q: got err %v", v, err)
		}
	}
}

func TestLoadWorker(t *testing.T) {
	cfg, err := LoadWorker(env(map[string]string{"DATABASE_URL": "postgres://db/tickets"}))
	if err != nil {
		t.Fatalf("LoadWorker: %v", err)
	}
	if cfg.DatabaseURL != "postgres://db/tickets" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
}

func TestLoadWorkerRequiresDatabaseURL(t *testing.T) {
	if _, err := LoadWorker(env(nil)); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("got err %v", err)
	}
}
