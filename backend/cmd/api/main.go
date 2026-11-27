// Command api serves the buyer API on the public listener and the staff API on
// the VPN-only listener. NGINX proxies /api/v1/* to each over a Unix socket.
package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"kacheem/backend/internal/cmdutil"
	"kacheem/backend/internal/config"
	"kacheem/backend/internal/httpapi"
	"kacheem/backend/internal/listen"
	"kacheem/backend/internal/payments"
	"kacheem/backend/internal/payments/providers"
	"kacheem/backend/internal/postgres"
	"kacheem/backend/internal/redisx"
	"kacheem/backend/internal/server"
	"kacheem/backend/internal/session"
)

var exit = os.Exit

func main() {
	exit(cmdutil.Main("api", func(ctx context.Context) error {
		return run(ctx, os.Getenv, os.Stderr)
	}, os.Stderr))
}

func run(ctx context.Context, getenv func(string) string, logOut io.Writer) error {
	logger := slog.New(slog.NewJSONHandler(logOut, nil))
	cfg, err := config.LoadAPI(getenv)
	if err != nil {
		return err
	}
	pool, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	rdb, err := redisx.Open(cfg.RedisURL)
	if err != nil {
		return err
	}
	defer rdb.Close()

	checks := []httpapi.Check{
		{Name: "postgres", Fn: pool.Ping},
		{Name: "redis", Fn: redisx.Ping(rdb)},
	}
	public, err := listen.Listen(cfg.PublicListen)
	if err != nil {
		return err
	}
	staff, err := listen.Listen(cfg.StaffListen)
	if err != nil {
		public.Close()
		return err
	}
	gw := providers.New(cfg.Payment, pool, cfg.PublicOrigin)
	var fakePage http.Handler
	if h, ok := gw.(http.Handler); ok {
		fakePage = h
	}
	pay := &payments.Service{DB: pool, Gateway: gw, ReturnURL: cfg.PublicOrigin + "/payments/return/" + gw.Provider()}
	logger.Info("api listening", "public", cfg.PublicListen, "staff", cfg.StaffListen, "payment_provider", gw.Provider())
	err = server.ServeAll(ctx, cfg.ShutdownTimeout,
		server.Endpoint{Server: server.New(httpapi.NewPublic(httpapi.PublicDeps{
			DB:       pool,
			Sessions: session.NewBuyer(pool, 30*time.Minute, httpapi.SessionError(logger)),
			Origin:   cfg.PublicOrigin,
			Checks:   checks,
			Logger:   logger,
			Payments: pay,
			// Only the fake gateway serves a page of its own.
			FakeGatewayPage: fakePage,
		}), logger), Listener: public},
		server.Endpoint{Server: server.New(httpapi.NewStaff(checks), logger), Listener: staff},
	)
	logger.Info("api stopped")
	return err
}
