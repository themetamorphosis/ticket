// Package cmdutil is the shared entry point for the binaries under cmd/.
package cmdutil

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// Main runs fn with a context cancelled on SIGINT or SIGTERM (systemd stop)
// and returns the process exit code.
func Main(name string, fn func(context.Context) error, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := fn(ctx); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	return 0
}
