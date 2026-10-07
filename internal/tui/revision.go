package tui

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// staleRevision is the base of a loaded state whose revision is not known, for instance because the file
// could not be read back after a conflict. It never equals the revision of a file, so the next write is
// reported as a conflict and asks for a reload instead of overwriting whatever the file holds.
const staleRevision config.Revision = "stale"

// adoptSaved makes saved, which the store just wrote, the editor's loaded state, and takes the revision of
// exactly the bytes it wrote as the base of the next change. The file is not read again: a read could
// return a change another writer made right after this save, which this state does not contain.
func (m *Model) adoptSaved(saved *config.Config) {
	m.cfg, m.configExists = saved, true
	rev, err := m.svc.RevisionOf(saved)
	if err != nil {
		rev = staleRevision
	}
	m.rev = rev
}

// reloadConfig replaces the loaded state, and the revision it is based on, with what the file holds now. A
// file that is gone is an empty configuration, like at start. Forms, typed input and the selection stay as
// they are, so the change can be repeated against the new state.
func (m *Model) reloadConfig() error {
	cfg, rev, err := m.svc.Load()
	if err != nil {
		var notFound *config.NotFoundError
		if !asNotFound(err, &notFound) {
			return err
		}
		cfg, rev = m.svc.NewConfig(), config.RevisionAbsent
		m.configExists = false
	} else {
		m.configExists = true
	}
	m.cfg, m.rev = cfg, rev
	m.list.setItems(m.entryNames(m.section))
	return nil
}

// conflicted reports whether err says the file changed since the editor loaded it. If so it reloads the
// state, so a repeated attempt is based on the current file, and sets the message for the user: nothing was
// written, the other change is kept, and the input is still there to repeat.
func (m *Model) conflicted(err error) bool {
	if !errors.Is(err, config.ErrConflict) {
		return false
	}
	name := filepath.Base(m.svc.Path())
	if rerr := m.reloadConfig(); rerr != nil {
		m.rev = staleRevision
		m.fail = m.redactor.Apply(fmt.Sprintf("%s changed outside this editor and could not be reloaded: %v; "+
			"nothing was written", name, rerr))
		return true
	}
	m.status = ""
	m.fail = name + " changed outside this editor; it was reloaded and nothing was written. " +
		"Check your input against the current state and repeat the change"
	return true
}
