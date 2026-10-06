//go:build windows

package cli

import (
	"context"
	"os"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// vaultHandoverPlatform reports whether a vault process here hands the vault over to a successor when an
// update asks it to. On Windows it never does: an update locks it instead.
const vaultHandoverPlatform = false

// stopSignals are the signals that lock a vault process, as a lock request would. A vault process has no
// console, so Windows sends it none of them in practice; it ends with the system at logout or restart.
var stopSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

// serveStreams returns the handover and the report a vault process inherits from StartProcess, its
// standard input and output, or false when either is not a pipe: 'qatlas vault serve' run by hand at a
// console.
func serveStreams() (handover, report *os.File, ok bool) {
	if !isPipe(os.Stdin) || !isPipe(os.Stdout) {
		return nil, nil, false
	}
	return os.Stdin, os.Stdout, true
}

// isPipe reports whether f is open and a pipe.
func isPipe(f *os.File) bool {
	if f == nil {
		return false
	}
	kind, err := windows.GetFileType(windows.Handle(f.Fd()))
	return err == nil && kind == windows.FILE_TYPE_PIPE
}

// allowHandover leaves server without a handover: a vault process on Windows answers an update's handover
// by locking itself, and reports no update behaviour in its status, since none applies here.
func allowHandover(*vaultproc.Server, string, string) {}

// watchSocket has nothing to watch on Windows: nobody can remove the vault pipe while this process holds
// it, and the system removes it with the process at logout or restart.
func watchSocket(context.Context, string, func()) {}

// sessionWarnings has nothing to read on Windows: no setting of the session ends a vault process that left
// the console it was started from.
func sessionWarnings(string) []string { return nil }
