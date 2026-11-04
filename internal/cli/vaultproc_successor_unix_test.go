//go:build linux || darwin

package cli

import (
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/castrowithcee/qatlas-connect/internal/vault"
	"github.com/castrowithcee/qatlas-connect/internal/vaultproc"
)

// successorHome is a home directory with an encrypted vault in the default location of the release before
// qatlas-connect, and a runtime directory of the test's own. It returns home and the vault's key.
func successorHome(t *testing.T) (string, *age.X25519Identity) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	t.Setenv("XDG_RUNTIME_DIR", shortRuntimeDir(t))
	return home, encryptedVault(t, filepath.Join(home, ".qatlas", "cli"))
}

// encryptedVault creates the configuration directory dir with an encrypted vault and returns the vault's key.
func encryptedVault(t *testing.T, dir string) *age.X25519Identity {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("version: 4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v := vault.New(dir)
	offer := func(string) (string, error) { return "successor-passphrase", nil }
	if err := v.Set("wiki-reader", "token", "successor-secret", offer); err != nil {
		t.Fatal(err)
	}
	snap, err := v.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	key, err := age.ParseX25519Identity(snap.Identity)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// serveEarlier listens at the socket a vault process of the release before qatlas-connect serves the vault in
// vaultDir at, until the test ends.
func serveEarlier(t *testing.T, vaultDir string) {
	t.Helper()
	current, err := vaultproc.SocketPath(vaultDir)
	if err != nil {
		t.Fatal(err)
	}
	l, err := vaultproc.Listen(filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "qatlas", filepath.Base(current)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
}

// A successor started with the default file of the release before qatlas-connect by a vault process of that
// release migrates and serves the current default file; the legacy directory stays as it was.
func TestSuccessorConfigPathMapsTheEarlierDefault(t *testing.T) {
	home, key := successorHome(t)
	legacy := filepath.Join(home, ".qatlas", "cli")
	vaultDir := vault.New(legacy).Dir()
	serveEarlier(t, vaultDir)
	before := treeOf(t, legacy)

	got, err := successorConfigPath(filepath.Join(legacy, "config.yaml"), vaultDir, key)

	if want := filepath.Join(home, ".qatlas", "connect", "config.yaml"); err != nil || got != want {
		t.Fatalf("successorConfigPath() = %q, %v, want %q", got, err, want)
	}
	if recipient, err := vault.New(filepath.Dir(got)).Recipient(); err != nil || recipient != key.Recipient().String() {
		t.Errorf("the migrated vault's recipient = %q, %v, want the key's", recipient, err)
	}
	if after := treeOf(t, legacy); after != before {
		t.Errorf("the legacy directory changed:\n%s\nwant\n%s", after, before)
	}
}

// Any other start keeps its path and migrates nothing: another explicit path, an explicit place in the
// environment, or no vault process of the earlier release serving the vault.
func TestSuccessorConfigPathKeepsEveryOtherPath(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T, home string) string{
		"another explicit path": func(t *testing.T, home string) string {
			dir := filepath.Join(home, "elsewhere")
			encryptedVault(t, dir)
			serveEarlier(t, vault.New(dir).Dir())
			return filepath.Join(dir, "config.yaml")
		},
		"QATLAS_CONFIG": func(t *testing.T, home string) string {
			path := filepath.Join(home, ".qatlas", "cli", "config.yaml")
			serveEarlier(t, vault.New(filepath.Dir(path)).Dir())
			t.Setenv("QATLAS_CONFIG", path)
			return path
		},
		"QATLAS_CLI_HOME": func(t *testing.T, home string) string {
			dir := filepath.Join(home, ".qatlas", "cli")
			serveEarlier(t, vault.New(dir).Dir())
			t.Setenv("QATLAS_CLI_HOME", dir)
			return filepath.Join(dir, "config.yaml")
		},
		"no earlier vault process": func(t *testing.T, home string) string {
			return filepath.Join(home, ".qatlas", "cli", "config.yaml")
		},
	} {
		t.Run(name, func(t *testing.T) {
			home, key := successorHome(t)
			path := setup(t, home)

			got, err := successorConfigPath(path, vault.New(filepath.Dir(path)).Dir(), key)

			if err != nil || got != path {
				t.Fatalf("successorConfigPath() = %q, %v, want %q unchanged", got, err, path)
			}
			if _, err := os.Lstat(filepath.Join(home, ".qatlas", "connect")); !os.IsNotExist(err) {
				t.Errorf("the configuration was migrated: %v", err)
			}
		})
	}
}

// A successor that cannot serve the current default location as the vault handed over does not start: when
// the migration fails, and when the current directory holds another vault.
func TestSuccessorConfigPathFailsRatherThanServeTheLegacyPlace(t *testing.T) {
	t.Run("migration fails", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root writes into a read-only directory")
		}
		home, key := successorHome(t)
		legacy := filepath.Join(home, ".qatlas", "cli")
		serveEarlier(t, vault.New(legacy).Dir())
		root := filepath.Dir(legacy)
		if err := os.Chmod(root, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(root, 0o700) })

		if got, err := successorConfigPath(filepath.Join(legacy, "config.yaml"), vault.New(legacy).Dir(), key); err == nil {
			t.Fatalf("successorConfigPath() = %q, want an error", got)
		}
	})
	t.Run("another vault in the current directory", func(t *testing.T) {
		home, key := successorHome(t)
		legacy := filepath.Join(home, ".qatlas", "cli")
		serveEarlier(t, vault.New(legacy).Dir())
		encryptedVault(t, filepath.Join(home, ".qatlas", "connect"))

		if got, err := successorConfigPath(filepath.Join(legacy, "config.yaml"), vault.New(legacy).Dir(), key); err == nil {
			t.Fatalf("successorConfigPath() = %q, want an error", got)
		}
	})
}

// treeOf describes every entry below dir with its mode and content, for comparing a directory over time.
func treeOf(t *testing.T, dir string) string {
	t.Helper()
	var out string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		out += rel + " " + info.Mode().String()
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out += " " + string(data)
		}
		out += "\n"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
