package manage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
)

// maxCheckedBackups bounds how many backups a rollback runs the older program against: the rotation keeps
// ten, and each check starts a process.
const maxCheckedBackups = 10

var (
	// ErrNoReadableBackup reports that the older program reads neither the configuration nor any backup.
	ErrNoReadableBackup = errors.New("the older version reads neither the configuration nor any of its backups")
	// ErrRestoreNotConfirmed reports that the older program does not read the configuration and the caller
	// did not confirm restoring a backup, so nothing may be installed.
	ErrRestoreNotConfirmed = errors.New("the older version does not read the configuration and no backup was confirmed")
)

// RollbackCheck is what running an older release's program against the configuration found. It carries the
// answer for the caller to ask its own questions, and what a later Rollback needs to act on it.
type RollbackCheck struct {
	// Reads reports that the older program reads the current configuration, or that there is none: the
	// installation needs nothing restored.
	Reads bool
	// Offer is the newest backup the older program reads, set only when Reads is false; nil when it reads
	// none. Offering is not restoring: nothing is changed until Rollback gets restore true.
	Offer *config.Backup

	offerData []byte
	base      config.Revision
}

// CheckRollback runs the program of the prepared older release against copies of the configuration and,
// when it does not read it, of the backups from the newest on, and returns what it found. The real file and
// the backups are only read. An error, including a program that cannot be run or does not finish in time,
// means nothing is known and nothing was changed.
func (s *Service) CheckRollback(ctx context.Context, p *selfupdate.Prepared) (*RollbackCheck, error) {
	data, rev, err := s.store.Snapshot()
	if err != nil {
		var nf *config.NotFoundError
		if errors.As(err, &nf) {
			return &RollbackCheck{Reads: true, base: config.RevisionAbsent}, nil
		}
		return nil, err
	}
	reads, err := p.ReadsConfig(ctx, data)
	if err != nil {
		return nil, err
	}
	check := &RollbackCheck{Reads: reads, base: rev}
	if reads {
		return check, nil
	}
	backups, err := s.store.Backups()
	if err != nil {
		return nil, err
	}
	if len(backups) > maxCheckedBackups {
		backups = backups[:maxCheckedBackups]
	}
	for _, b := range backups {
		backup, err := s.store.ReadBackup(b)
		if err != nil {
			continue
		}
		ok, err := p.ReadsConfig(ctx, backup)
		if err != nil {
			return nil, err
		}
		if ok {
			offer := b
			check.Offer, check.offerData = &offer, backup
			break
		}
	}
	return check, nil
}

// Rollback installs the prepared older release through client. With check.Reads it only installs. Without,
// it needs restore true and an offered backup: it saves the current configuration as a new backup, writes the
// offered backup in its place, and then installs, which runs client's hooks (the vault process is locked
// there). Should the installation fail, the configuration it replaced is put back from that new backup. The
// vault's files are never touched. Before any change, a configuration that changed since CheckRollback
// ended the rollback with a *config.ConflictError.
func (s *Service) Rollback(ctx context.Context, client *selfupdate.Client, p *selfupdate.Prepared,
	check *RollbackCheck, restore bool) (selfupdate.Result, error) {
	if check.Reads {
		return client.InstallPrepared(ctx, p)
	}
	if check.Offer == nil {
		return selfupdate.Result{}, ErrNoReadableBackup
	}
	if !restore {
		return selfupdate.Result{}, ErrRestoreNotConfirmed
	}
	restored, err := s.store.RestoreBackup(check.offerData, check.base)
	if err != nil {
		return selfupdate.Result{}, err
	}
	result, err := client.InstallPrepared(ctx, p)
	if err != nil {
		if undoErr := s.store.UndoRestore(restored); undoErr != nil {
			return selfupdate.Result{}, fmt.Errorf("%w; the previous configuration could not be put back: %v; it is saved as %s",
				err, undoErr, filepath.Base(restored.Previous))
		}
		return selfupdate.Result{}, err
	}
	return result, nil
}
