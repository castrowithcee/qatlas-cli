//go:build !linux

package cli

import (
	"context"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// vaultProcessPlatform reports whether this platform runs a vault process. Here it does not yet: 'qatlas
// vault unlock' unlocks the vault for its own process only.
const vaultProcessPlatform = false

func startVaultProcess(context.Context, string, vault.Snapshot, *vaultproc.Client) (vaultproc.Status, error) {
	return vaultproc.Status{}, vaultproc.ErrUnsupported
}

func runVaultServe(*Options, *capability.Registry) error { return vaultproc.ErrUnsupported }

func sessionWarnings(string) []string { return nil }
