//go:build !linux && !darwin && !windows

package cli

import (
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// vaultProcessPlatform reports whether this platform runs a vault process. Here it does not yet: 'qatlas
// vault unlock', and the TUI's own 'ctrl+l', unlock the vault for their own process only (see
// vaultmigrate.StartProcess, which reports vaultproc.ErrUnsupported here too).
const vaultProcessPlatform = false

func runVaultServe(*Options, *capability.Registry) error { return vaultproc.ErrUnsupported }

func sessionWarnings(string, int) []string { return nil }
