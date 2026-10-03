package tui

import (
	"errors"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// syncRevision makes the model's revision the one of the file as a test just wrote it directly, the way the
// model itself adopts what it saved. Tests that seed the file behind the model's back call it; a test about
// a conflict deliberately does not.
func syncRevision(t *testing.T, m *Model) {
	t.Helper()
	_, rev, err := m.store.LoadVersioned()
	var notFound *config.NotFoundError
	if errors.As(err, &notFound) {
		err = nil // no file yet: the base of the first write
	}
	if err != nil {
		t.Fatalf("LoadVersioned() = %v", err)
	}
	m.rev = rev
}
