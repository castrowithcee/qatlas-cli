//go:build !linux && !darwin && !windows

package cli

import (
	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/vaultproc"
)

// vaultProcessPlatform reports whether this platform runs a vault process. Here it does not yet: 'qconnect
// vault unlock', and the TUI's own 'ctrl+l', unlock the vault for their own process only (see
// vaultproc.StartProcess, which reports vaultproc.ErrUnsupported here too).
const vaultProcessPlatform = false

func runVaultServe(*Options, *capability.Registry, bool) error { return vaultproc.ErrUnsupported }

func sessionWarnings(string) []string { return nil }
