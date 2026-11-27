package redisx

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestOpenAndPing(t *testing.T) {
	u := os.Getenv("TEST_REDIS_URL")
	if u == "" {
		t.Fatal("TEST_REDIS_URL is not set; run tests with `make go-test`")
	}
	c, err := Open(u)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()
	if err := Ping(c)(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestPingFailsWhenUnreachable(t *testing.T) {
	c, err := Open("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := Ping(c)(ctx); err == nil {
		t.Fatal("expected ping error")
	}
}

func TestOpenRejectsBadURL(t *testing.T) {
	if _, err := Open("http://not-redis"); err == nil {
		t.Fatal("expected error")
	}
}
