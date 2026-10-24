package cli

import (
	"path/filepath"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

func TestTUIPrefsFileFollowsTheConfigurationDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", home)
	path, err := config.Path("")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := tuiPrefsPath(path), filepath.Join(home, "tui.yaml"); got != want {
		t.Errorf("QATLAS_CLI_HOME: tuiPrefsPath = %q, want %q", got, want)
	}

	other := filepath.Join(t.TempDir(), "elsewhere", "my.yaml")
	t.Setenv("QATLAS_CONFIG", other)
	path, err = config.Path("")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := tuiPrefsPath(path), filepath.Join(filepath.Dir(other), "tui.yaml"); got != want {
		t.Errorf("QATLAS_CONFIG: tuiPrefsPath = %q, want %q", got, want)
	}
}
