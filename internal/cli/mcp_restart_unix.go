//go:build linux || darwin

package cli

import (
	"syscall"

	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// platformMCPRestart is how a server restarts itself here: it replaces its process image in place.
func platformMCPRestart() mcpRestart {
	return mcpRestart{replaced: vaultproc.ReplacedProgram, exec: syscall.Exec}
}
