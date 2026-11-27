package listen

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTCP(t *testing.T) {
	ln, err := Listen("tcp:127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	if ln.Addr().Network() != "tcp" {
		t.Fatalf("network = %s", ln.Addr().Network())
	}
}

func TestUnixReplacesStaleSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.sock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := Listen("unix:" + path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Close()
}

func TestUnixFreshPath(t *testing.T) {
	ln, err := Listen("unix:" + filepath.Join(t.TempDir(), "api.sock"))
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ln.Close()
}

func TestUnixCannotRemoveExistingPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "busy")
	if err := os.MkdirAll(filepath.Join(dir, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen("unix:" + dir); err == nil {
		t.Fatal("expected error for non-empty directory in socket path")
	}
}

func TestUnixMissingDirectory(t *testing.T) {
	if _, err := Listen("unix:" + filepath.Join(t.TempDir(), "missing", "api.sock")); err == nil {
		t.Fatal("expected error")
	}
}

func TestRejectsMalformedSpecs(t *testing.T) {
	for _, spec := range []string{"", "tcp", "tcp:", "udp:127.0.0.1:1", "/run/api.sock"} {
		_, err := Listen(spec)
		if err == nil || !strings.Contains(err.Error(), "listen address") {
			t.Errorf("Listen(%q) err = %v", spec, err)
		}
	}
}
