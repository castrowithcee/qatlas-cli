package secret

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// Without a session bus to reach, the store is unavailable at once: the check never starts a bus of its
// own, and it never reaches the library that would.
func TestReadyWithoutASessionBusIsUnavailable(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "bus"))

	start := time.Now()
	err := ready(context.Background())

	if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrLocked) {
		t.Fatalf("ready() = %v, want unavailable", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("ready() took %s, want an answer at once", elapsed)
	}
}
