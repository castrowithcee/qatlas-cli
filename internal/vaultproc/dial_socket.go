//go:build !windows

package vaultproc

import (
	"context"
	"net"
	"time"
)

// dialSocket connects to the vault socket at path as a client, until deadline or the end of ctx.
func dialSocket(ctx context.Context, path string, deadline time.Time) (net.Conn, error) {
	dialer := net.Dialer{Deadline: deadline}
	return dialer.DialContext(ctx, "unix", path)
}

// pipePath reports that the vault process listens on a socket here, not on a named pipe.
func pipePath(string) (string, bool, error) { return "", false, nil }
