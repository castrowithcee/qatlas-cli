//go:build !linux && !darwin && !windows

package vaultproc

import (
	"context"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// StartProcess is unsupported here: this platform runs no vault process at all (see Supported).
func StartProcess(context.Context, string, vault.Snapshot, *Client) (Status, error) {
	return Status{}, ErrUnsupported
}
