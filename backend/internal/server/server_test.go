package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestNewSetsTimeoutsAndLimits(t *testing.T) {
	h := http.NotFoundHandler()
	srv := New(h, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if srv.ReadHeaderTimeout != 5*time.Second || srv.ReadTimeout != 15*time.Second ||
		srv.WriteTimeout != 30*time.Second || srv.IdleTimeout != 60*time.Second {
		t.Fatalf("timeouts not set: %+v", srv)
	}
	if srv.MaxHeaderBytes != 16<<10 || srv.ErrorLog == nil || srv.Handler == nil {
		t.Fatalf("limits/logging not set: %+v", srv)
	}
}

func listener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

func TestServeAllServesUntilCancelled(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	a, b := listener(t), listener(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeAll(ctx, time.Second, Endpoint{&http.Server{Handler: h}, a}, Endpoint{&http.Server{Handler: h}, b})
	}()
	for _, ln := range []net.Listener{a, b} {
		resp, err := http.Get("http://" + ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusTeapot {
			t.Fatalf("status %d", resp.StatusCode)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("ServeAll: %v", err)
	}
}

func TestServeAllReturnsListenerFailure(t *testing.T) {
	ln := listener(t)
	ln.Close()
	err := ServeAll(context.Background(), time.Second, Endpoint{&http.Server{}, ln})
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("err = %v, want accept error", err)
	}
}

func TestServeAllReportsShutdownTimeout(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	})
	ln := listener(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeAll(ctx, 10*time.Millisecond, Endpoint{&http.Server{Handler: h}, ln}) }()
	go func() {
		if resp, err := http.Get("http://" + ln.Addr().String()); err == nil {
			resp.Body.Close()
		}
	}()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}
