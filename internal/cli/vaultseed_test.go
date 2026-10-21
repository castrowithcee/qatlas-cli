package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"filippo.io/age"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// seedWorkFactor is the scrypt work factor of a seeded vault's key. Creating an encrypted vault and
// unlocking it costs seconds at the real work factor, which a test of anything but the key's own strength
// does not need to pay again for every fixture.
const seedWorkFactor = 2

type vaultSeed struct {
	once sync.Once
	dir  string
	err  error
}

var (
	seedMu   sync.Mutex
	seedRoot string
	seeds    = map[string]*vaultSeed{}
)

// removeSeeds deletes the seed vaults of this test binary; TestMain calls it before exiting.
func removeSeeds() {
	if seedRoot != "" {
		_ = os.RemoveAll(seedRoot)
	}
}

// seedEncryptedVault gives dir an encrypted vault holding one secret and returns it unlocked, as
// vault.New(dir).Set with the passphrase leaves it. The vault is created through the real code once per
// binary and distinct arguments, its key is re-wrapped with a low scrypt work factor, and every call
// copies the files, so no two tests share one. A test of creation, rekeying or the key's strength
// creates its vault itself.
func seedEncryptedVault(t *testing.T, dir, name, role, value, passphrase string) *vault.Vault {
	t.Helper()
	if err := copySeed(t, dir, name, role, value, passphrase); err != nil {
		// A copy the vault refuses (file permissions) is replaced by a vault created in place.
		_ = os.RemoveAll(filepath.Join(dir, vault.DirName))
		if err := vault.New(dir).Set(name, role, value, offeringPassphrase(passphrase)); err != nil {
			t.Fatalf("vault Set() = %v", err)
		}
	}
	v := vault.New(dir)
	if _, err := v.Unlock(passphrase); err != nil {
		t.Fatalf("vault Unlock() = %v", err)
	}
	return v
}

func copySeed(t *testing.T, dir, name, role, value, passphrase string) error {
	key := strings.Join([]string{name, role, value, passphrase}, "\x00")
	seedMu.Lock()
	if seedRoot == "" {
		root, err := os.MkdirTemp("", "qatlas-cli-test-seed-")
		if err != nil {
			seedMu.Unlock()
			return err
		}
		seedRoot = root
	}
	seed := seeds[key]
	if seed == nil {
		seed = &vaultSeed{}
		seeds[key] = seed
	}
	root := seedRoot
	seedMu.Unlock()

	seed.once.Do(func() {
		seed.dir, seed.err = os.MkdirTemp(root, "v")
		if seed.err != nil {
			return
		}
		if seed.err = vault.New(seed.dir).Set(name, role, value, offeringPassphrase(passphrase)); seed.err != nil {
			return
		}
		seed.err = rewrapKey(filepath.Join(seed.dir, vault.DirName, "key.age"), passphrase)
	})
	if seed.err != nil {
		return seed.err
	}
	if err := copyTree(filepath.Join(seed.dir, vault.DirName), filepath.Join(dir, vault.DirName)); err != nil {
		return err
	}
	// Windows has no file modes: the copy inherits its ACL, which the vault checks on every read.
	_, err := vault.New(dir).Recipient()
	return err
}

// rewrapKey encrypts the key file's content again to the passphrase at seedWorkFactor.
func rewrapKey(path, passphrase string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	identity, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return err
	}
	r, err := age.Decrypt(bytes.NewReader(data), identity)
	if err != nil {
		return err
	}
	plain, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return err
	}
	recipient.SetWorkFactor(seedWorkFactor)
	var out bytes.Buffer
	w, err := age.Encrypt(&out, recipient)
	if err != nil {
		return err
	}
	if _, err := w.Write(plain); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, out.Bytes(), 0o600)
}

// copyTree copies the files below src into dst, creating dst at 0700 and the files at 0600.
func copyTree(src, dst string) error {
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}
