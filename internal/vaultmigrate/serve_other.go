//go:build !linux

package vaultmigrate

import (
	"context"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// StartProcess is unsupported here: this platform runs no vault process at all (see vaultproc.Supported).
func StartProcess(context.Context, string, vault.Snapshot, *vaultproc.Client) (vaultproc.Status, error) {
	return vaultproc.Status{}, vaultproc.ErrUnsupported
}
