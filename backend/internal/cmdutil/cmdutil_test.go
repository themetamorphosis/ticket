package cmdutil

import (
	"bytes"
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestMainReturnsOneAndReportsError(t *testing.T) {
	var stderr bytes.Buffer
	code := Main("api", func(context.Context) error { return errors.New("bad config") }, &stderr)
	if code != 1 || stderr.String() != "api: bad config\n" {
		t.Fatalf("code %d, stderr %q", code, stderr.String())
	}
}

func TestMainCancelsOnSIGTERM(t *testing.T) {
	var stderr bytes.Buffer
	code := Main("worker", func(ctx context.Context) error {
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("context not cancelled by SIGTERM")
		}
	}, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code %d, stderr %q", code, stderr.String())
	}
}
