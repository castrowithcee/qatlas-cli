package manage

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/vault"
)

func TestEarlierVaultDirs(t *testing.T) {
	t.Setenv("QCONNECT_CONFIG", "")
	t.Setenv("QCONNECT_HOME", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	legacyHome := config.LegacyDefaultDir("")
	if legacyHome == "" {
		t.Skip("no legacy default directory")
	}
	legacy := vault.New(legacyHome).Dir()
	own := vault.New(t.TempDir())

	if got, want := EarlierVaultDirs("", own), []string{own.Dir(), legacy}; !reflect.DeepEqual(got, want) {
		t.Errorf("default location: got %v, want %v", got, want)
	}
	if got, want := EarlierVaultDirs(filepath.Join(t.TempDir(), "config.yaml"), own), []string{own.Dir()}; !reflect.DeepEqual(got, want) {
		t.Errorf("explicit config: got %v, want %v", got, want)
	}
	if got, want := EarlierVaultDirs("", vault.New(legacyHome)), []string{legacy}; !reflect.DeepEqual(got, want) {
		t.Errorf("vault in legacy dir: got %v, want %v", got, want)
	}
	t.Setenv("QCONNECT_HOME", t.TempDir())
	if got, want := EarlierVaultDirs("", own), []string{own.Dir()}; !reflect.DeepEqual(got, want) {
		t.Errorf("QCONNECT_HOME set: got %v, want %v", got, want)
	}
}
