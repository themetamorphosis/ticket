// Command worker runs background jobs under systemd, on hosts separate from
// buyer traffic: it releases expired holds, reconciles payments the buyer
// never returned from, sends refunds, and renders and delivers tickets.
package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"time"

	"kacheem/backend/internal/cmdutil"
	"kacheem/backend/internal/config"
	"kacheem/backend/internal/delivery"
	"kacheem/backend/internal/fulfil"
	"kacheem/backend/internal/orders"
	"kacheem/backend/internal/payments"
	"kacheem/backend/internal/payments/providers"
	"kacheem/backend/internal/postgres"
	"kacheem/backend/internal/sealbox"
	"kacheem/backend/internal/tickets"
)

var exit = os.Exit

const (
	// expiryBatch bounds how many holds one tick releases.
	expiryBatch = 500
	// reconcileIdle is how long an attempt sits unchanged before the worker
	// asks the provider about it (the buyer may still be on the payment page).
	reconcileIdle  = 2 * time.Minute
	reconcileBatch = 100
	refundBatch    = 50
	fulfilBatch    = 20
)

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
	box, _ := sealbox.New(cfg.Delivery.TicketKey) // validated by config
	issuer := &tickets.Issuer{Box: box}
	pay := &payments.Service{DB: pool, Gateway: providers.New(cfg.Payment, pool, ""),
		OnPaid: issuer.IssueInTx}
	ful := &fulfil.Fulfiller{DB: pool, Issuer: issuer, Mailer: delivery.NewMailer(cfg.Delivery, pool),
		SMS: delivery.NewSMS(cfg.Delivery, pool), PublicOrigin: cfg.PublicOrigin, GateOrigin: cfg.Delivery.GateOrigin}
	logger.Info("worker started", "expiry_interval", cfg.ExpiryInterval.String(), "payment_provider", pay.Gateway.Provider())

	tick := time.NewTicker(cfg.ExpiryInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("worker stopped")
			return nil
		case <-tick.C:
			// Each job keeps running on errors: the next tick retries, and the
			// alerts on the oldest open hold, payment or refund page if it persists.
			step(ctx, logger, "released expired holds", func() (int, error) { return orders.ExpireDue(ctx, pool, expiryBatch) })
			step(ctx, logger, "reconciled payments", func() (int, error) { return pay.Reconcile(ctx, reconcileIdle, reconcileBatch) })
			step(ctx, logger, "processed refunds", func() (int, error) { return pay.ProcessRefunds(ctx, refundBatch) })
			step(ctx, logger, "ran fulfilment jobs", func() (int, error) { return ful.Run(ctx, fulfilBatch) })
		}
	}
}

func step(ctx context.Context, logger *slog.Logger, msg string, job func() (int, error)) {
	n, err := job()
	if err != nil && ctx.Err() == nil {
		logger.Error(msg+" failed", "err", err, "count", n)
		return
	}
	if n > 0 {
		logger.Info(msg, "count", n)
	}
}
