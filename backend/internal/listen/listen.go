// Package listen opens the listeners NGINX proxies to. In production these are
// Unix sockets in a systemd RuntimeDirectory; the unit's UMask and Group give
// NGINX access, so this package does not change socket permissions itself.
package listen

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strings"
)

// Listen opens "unix:/path/to.sock" or "tcp:host:port". A stale Unix socket
// left by a crashed process is removed first.
func Listen(spec string) (net.Listener, error) {
	scheme, addr, _ := strings.Cut(spec, ":")
	if addr == "" || (scheme != "unix" && scheme != "tcp") {
		return nil, fmt.Errorf("listen address %q must be unix:/path or tcp:host:port", spec)
	}
	if scheme == "unix" {
		if err := os.Remove(addr); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("remove stale socket: %w", err)
		}
	}
	return net.Listen(scheme, addr)
}
