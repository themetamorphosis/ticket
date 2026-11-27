// Command publish writes an event's public snapshot into the buyer web root.
//
//	PUBLISH_DIR=/srv/kacheem/buyer publish <event-slug>
//
// Run it on each web node (or publish once and sync) after an organizer
// approves the event content. It never rebuilds the frontend.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"kacheem/backend/internal/cmdutil"
	"kacheem/backend/internal/config"
	"kacheem/backend/internal/postgres"
	"kacheem/backend/internal/publish"
)

var exit = os.Exit

var errUsage = errors.New("usage: publish <event-slug>")

func main() {
	exit(cmdutil.Main("publish", func(ctx context.Context) error {
		return run(ctx, os.Args[1:], os.Getenv, os.Stdout)
	}, os.Stderr))
}

func run(ctx context.Context, args []string, getenv func(string) string, out io.Writer) error {
	if len(args) != 1 {
		return errUsage
	}
	cfg, err := config.LoadPublish(getenv)
	if err != nil {
		return err
	}
	pool, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	snap, err := publish.Publish(ctx, pool, args[0], cfg.Dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "published %s revision %d to %s\n", snap.Event.Slug, snap.Revision, filepath.Join(cfg.Dir, "event.json"))
	return nil
}
