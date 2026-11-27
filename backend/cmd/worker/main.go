// Command worker runs background jobs (outbox, expiry, fulfillment and
// reconciliation as later milestones add them) under systemd, on hosts
// separate from buyer traffic.
package main

import (
	"context"
	"io"
	"log/slog"
	"os"

	"kacheem/backend/internal/cmdutil"
	"kacheem/backend/internal/config"
	"kacheem/backend/internal/postgres"
)

var exit = os.Exit

func main() {
	exit(cmdutil.Main("worker", func(ctx context.Context) error {
		return run(ctx, os.Getenv, os.Stderr)
	}, os.Stderr))
}

func run(ctx context.Context, getenv func(string) string, logOut io.Writer) error {
	logger := slog.New(slog.NewJSONHandler(logOut, nil))
	cfg, err := config.LoadWorker(getenv)
	if err != nil {
		return err
	}
	pool, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	logger.Info("worker started")
	<-ctx.Done()
	logger.Info("worker stopped")
	return nil
}
