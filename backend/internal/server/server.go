// Package server runs HTTP servers with bounded timeouts and graceful shutdown.
package server

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	"golang.org/x/sync/errgroup"
)

// New returns an http.Server with the plan's ingress limits (section 7).
func New(h http.Handler, logger *slog.Logger) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
}

// Endpoint pairs a server with the listener it serves.
type Endpoint struct {
	Server   *http.Server
	Listener net.Listener
}

// ServeAll serves every endpoint until ctx is cancelled or one fails, then
// shuts all of them down, allowing in-flight requests up to grace to finish.
func ServeAll(ctx context.Context, grace time.Duration, endpoints ...Endpoint) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, e := range endpoints {
		g.Go(func() error { return serve(gctx, grace, e) })
	}
	return g.Wait()
}

func serve(ctx context.Context, grace time.Duration, e Endpoint) error {
	errc := make(chan error, 1)
	go func() { errc <- e.Server.Serve(e.Listener) }()
	select {
	case err := <-errc:
		return err // Serve only returns early on a listener failure.
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if err := e.Server.Shutdown(sctx); err != nil {
		return err
	}
	<-errc // http.ErrServerClosed
	return nil
}
