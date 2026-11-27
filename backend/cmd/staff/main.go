// Command staff bootstraps staff accounts from the command line, for the
// first organizer (everyone else is invited from the dashboard).
//
//	staff invite <email> <name> <role>
//
// It prints a one-time invitation link (valid 72 hours) to give the person.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"kacheem/backend/internal/audit"
	"kacheem/backend/internal/cmdutil"
	"kacheem/backend/internal/config"
	"kacheem/backend/internal/postgres"
	"kacheem/backend/internal/staffauth"
)

var exit = os.Exit

var errUsage = errors.New("usage: staff invite <email> <name> <organizer|finance|support|gate>")

func main() {
	exit(cmdutil.Main("staff", func(ctx context.Context) error {
		return run(ctx, os.Args[1:], os.Getenv, os.Stdout)
	}, os.Stderr))
}

func run(ctx context.Context, args []string, getenv func(string) string, out io.Writer) error {
	if len(args) != 4 || args[0] != "invite" {
		return errUsage
	}
	dbURL, err := config.LoadDatabase(getenv)
	if err != nil {
		return err
	}
	origin := getenv("GATE_ORIGIN")
	pool, err := postgres.Open(ctx, dbURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	auth, err := staffauth.New(pool, origin)
	if err != nil {
		return err
	}
	token, err := auth.Invite(ctx, audit.Actor{Kind: "system"}, args[1], args[2], args[3])
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Invitation for %s (%s), valid 72 hours:\n%s/invite/#%s\n", args[1], args[3], origin, token)
	return nil
}
