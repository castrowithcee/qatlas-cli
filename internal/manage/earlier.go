package manage

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/vault"
	"github.com/castrowithcee/qatlas-connect/internal/vaultproc"
)

// EarlierVaultDirs returns the vault directories a vault process of the release before qatlas-connect may
// hold v unlocked for: v's own, and, where the configuration is resolved from the default location
// (explicitConfig empty, see config.LegacyDefaultDir), the vault in that release's default location.
func EarlierVaultDirs(explicitConfig string, v *vault.Vault) []string {
	dirs := []string{v.Dir()}
	if home := config.LegacyDefaultDir(explicitConfig); home != "" {
		if legacy := vault.New(home).Dir(); filepath.Clean(legacy) != filepath.Clean(v.Dir()) {
			dirs = append(dirs, legacy)
		}
	}
	return dirs
}

// EarlierFound is the outcome of looking for a vault process of the earlier release in one directory: its
// pid (0 if none), or the error of a socket that could not be identified.
type EarlierFound struct {
	PID int
	Err error
}

// FindEarlierProcesses looks, in the order of EarlierVaultDirs, for a vault process of the release before
// qatlas-connect that holds v unlocked (see vaultproc.FindEarlier). It returns one outcome per directory.
func FindEarlierProcesses(explicitConfig string, v *vault.Vault) []EarlierFound {
	var found []EarlierFound
	for _, dir := range EarlierVaultDirs(explicitConfig, v) {
		pid, err := vaultproc.FindEarlier(dir)
		found = append(found, EarlierFound{PID: pid, Err: err})
	}
	return found
}

// EndEarlier ends every vault process of the release before qatlas-connect that holds v unlocked (see
// vaultproc.EndEarlier) and returns the ids of those it ended.
func EndEarlier(ctx context.Context, explicitConfig string, v *vault.Vault) ([]int, error) {
	var ended []int
	var errs []error
	for _, dir := range EarlierVaultDirs(explicitConfig, v) {
		pid, err := vaultproc.EndEarlier(ctx, dir)
		if err != nil {
			errs = append(errs, err)
		} else if pid > 0 {
			ended = append(ended, pid)
		}
	}
	return ended, errors.Join(errs...)
}
