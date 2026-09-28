//go:build linux || darwin

package cli

import (
	"context"
	"os"
	"syscall"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/vaultmigrate"
)

// socketCheckInterval is how often a vault process checks that its socket is still in place.
const socketCheckInterval = 10 * time.Second

// stopSignals are the signals that lock a vault process, as a lock request would.
var stopSignals = []os.Signal{syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT}

// serveStreams returns the handover and the report a vault process inherits from StartProcess, as the
// descriptors HandoverFD and ReportFD, or false when either is not a pipe: 'qatlas vault serve' run by hand.
func serveStreams() (handover, report *os.File, ok bool) {
	if !inheritedPipe(vaultmigrate.HandoverFD) || !inheritedPipe(vaultmigrate.ReportFD) {
		return nil, nil, false
	}
	return os.NewFile(vaultmigrate.HandoverFD, "vault-handover"),
		os.NewFile(vaultmigrate.ReportFD, "vault-report"), true
}

// inheritedPipe reports whether fd is open and a pipe, which is what 'qatlas vault unlock' hands a vault
// process. A descriptor that is anything else is left alone.
func inheritedPipe(fd int) bool {
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		return false
	}
	return stat.Mode&syscall.S_IFMT == syscall.S_IFIFO
}

// watchSocket calls gone once the socket at path is removed or replaced, which is what happens to a socket
// in the runtime directory when the system removes that directory at the end of the last session. A vault
// process nobody can reach any more locks itself rather than holding the secrets until its idle timeout.
func watchSocket(ctx context.Context, path string, gone func()) {
	own, err := os.Lstat(path)
	if err != nil {
		gone()
		return
	}
	ticker := time.NewTicker(socketCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if now, err := os.Lstat(path); err != nil || !os.SameFile(own, now) {
				gone()
				return
			}
		}
	}
}
