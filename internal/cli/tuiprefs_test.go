package cli

import (
	"path/filepath"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/config"
)

func TestTUIPrefsFileFollowsTheConfigurationDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("QCONNECT_CONFIG", "")
	t.Setenv("QCONNECT_HOME", home)
	path, err := config.Path("")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := tuiPrefsPath(path), filepath.Join(home, "tui.yaml"); got != want {
		t.Errorf("QCONNECT_HOME: tuiPrefsPath = %q, want %q", got, want)
	}

	other := filepath.Join(t.TempDir(), "elsewhere", "my.yaml")
	t.Setenv("QCONNECT_CONFIG", other)
	path, err = config.Path("")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := tuiPrefsPath(path), filepath.Join(filepath.Dir(other), "tui.yaml"); got != want {
		t.Errorf("QCONNECT_CONFIG: tuiPrefsPath = %q, want %q", got, want)
	}
}
