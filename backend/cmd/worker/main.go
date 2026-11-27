// Command worker runs background jobs under systemd, on hosts separate from
// buyer traffic. Today it releases expired holds; later milestones add the
// outbox, fulfillment and reconciliation jobs.
package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"time"

	"kacheem/backend/internal/cmdutil"
	"kacheem/backend/internal/config"
	"kacheem/backend/internal/orders"
	"kacheem/backend/internal/postgres"
)

var exit = os.Exit

// expiryBatch bounds how many holds one tick releases.
const expiryBatch = 500

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
	logger.Info("worker started", "expiry_interval", cfg.ExpiryInterval.String())

	tick := time.NewTicker(cfg.ExpiryInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("worker stopped")
			return nil
		case <-tick.C:
			n, err := orders.ExpireDue(ctx, pool, expiryBatch)
			if err != nil && ctx.Err() == nil {
				// Keep running: the next tick retries, and the alert on the
				// oldest unreleased hold pages if this persists.
				logger.Error("release expired holds", "err", err, "released", n)
				continue
			}
			if n > 0 {
				logger.Info("released expired holds", "count", n)
			}
		}
	}
}
