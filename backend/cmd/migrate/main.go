// Command migrate applies the embedded Goose migrations. Production runs it
// once per release with the migration-only database role.
//
//	migrate up | down | status
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"kacheem/backend/internal/cmdutil"
	"kacheem/backend/internal/config"
	"kacheem/backend/migrations"
)

var exit = os.Exit

var errUsage = errors.New("usage: migrate up | down | status")

func main() {
	exit(cmdutil.Main("migrate", func(ctx context.Context) error {
		return run(ctx, os.Args[1:], os.Getenv, os.Stdout, migrations.Files)
	}, os.Stderr))
}

func run(ctx context.Context, args []string, getenv func(string) string, out io.Writer, fsys fs.FS) error {
	if len(args) != 1 {
		return errUsage
	}
	cfg, err := config.LoadWorker(getenv)
	if err != nil {
		return err
	}
	connCfg, err := pgx.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("parse database url: %w", err)
	}
	db := stdlib.OpenDB(*connCfg)
	defer db.Close()
	p, err := migrations.NewProvider(db, fsys)
	if err != nil {
		return err
	}

	switch args[0] {
	case "up":
		results, err := p.Up(ctx)
		for _, r := range results {
			fmt.Fprintln(out, r)
		}
		return err
	case "down":
		r, err := p.Down(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, r)
		return nil
	case "status":
		statuses, err := p.Status(ctx)
		for _, s := range statuses {
			fmt.Fprintf(out, "%-8s %s\n", s.State, s.Source.Path)
		}
		return err
	default:
		return errUsage
	}
}
