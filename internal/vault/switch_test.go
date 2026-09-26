package vault

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptUnencryptedVault(t *testing.T) {
	lowWorkFactor(t)
	dir := t.TempDir()
	v := New(dir)
	if err := v.Set("wiki-reader", "token-id", "id-1", nil); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	if err := v.Encrypt("s3cret-phrase"); err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if state, err := v.State(); err != nil || state != StateUnlocked {
		t.Fatalf("State() after Encrypt() = %v, %v, want unlocked, nil", state, err)
	}
	if _, err := os.Stat(v.plainPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the plaintext document survives encryption: %v", err)
	}

	// A fresh Vault value stands in for the next process: it must open with the passphrase alone.
	reopened := New(dir)
	if state, err := reopened.State(); err != nil || state != StateLocked {
		t.Fatalf("State() of a fresh Vault = %v, %v, want locked, nil", state, err)
	}
	value, found, _, err := reopened.Get("wiki-reader", "token-id", offering("s3cret-phrase"))
	if err != nil || !found || value != "id-1" {
		t.Fatalf("Get() after Encrypt() = %q, %v, %v, want id-1, true, nil", value, found, err)
	}
}

func TestEncryptAbsentVault(t *testing.T) {
	lowWorkFactor(t)
	dir := t.TempDir()
	v := New(dir)

	if err := v.Encrypt("s3cret-phrase"); err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if state, err := v.State(); err != nil || state != StateUnlocked {
		t.Fatalf("State() = %v, %v, want unlocked, nil", state, err)
	}

	if err := v.Set("wiki-reader", "token-id", "id-1", nil); err != nil {
		t.Fatalf("Set() after Encrypt() error = %v", err)
	}
	reopened := New(dir)
	if _, _, _, err := reopened.Get("wiki-reader", "token-id", offering("wrong")); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("Get() with the wrong passphrase = %v, want ErrWrongPassphrase", err)
	}
}

func TestEncryptRefusals(t *testing.T) {
	lowWorkFactor(t)
	dir := t.TempDir()
	v := New(dir)
	if err := v.Set("wiki-reader", "token-id", "id-1", offering("s3cret-phrase")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	if err := v.Encrypt(""); !errors.Is(err, ErrEmptyPassphrase) {
		t.Errorf("Encrypt(\"\") = %v, want ErrEmptyPassphrase", err)
	}
	if err := v.Encrypt("another-phrase"); !errors.Is(err, ErrAlreadyEncrypted) {
		t.Errorf("Encrypt() of an already encrypted vault = %v, want ErrAlreadyEncrypted", err)
	}
	// Neither refusal touched the key: the original passphrase still opens it.
	reopened := New(dir)
	if _, found, _, err := reopened.Get("wiki-reader", "token-id", offering("s3cret-phrase")); err != nil || !found {
		t.Fatalf("Get() after the refusals = found %v, err %v, want true, nil", found, err)
	}
}

func TestChangePassphrase(t *testing.T) {
	lowWorkFactor(t)
	dir := t.TempDir()
	v := New(dir)
	if err := v.Set("wiki-reader", "token-id", "id-1", offering("old-phrase")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	if err := v.ChangePassphrase("old-phrase", "new-phrase"); err != nil {
		t.Fatalf("ChangePassphrase() error = %v", err)
	}

	reopened := New(dir)
	if _, _, _, err := reopened.Get("wiki-reader", "token-id", offering("old-phrase")); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("Get() with the old passphrase = %v, want ErrWrongPassphrase", err)
	}
	value, found, _, err := reopened.Get("wiki-reader", "token-id", offering("new-phrase"))
	if err != nil || !found || value != "id-1" {
		t.Fatalf("Get() with the new passphrase = %q, %v, %v, want id-1, true, nil", value, found, err)
	}
}

// A wrong current passphrase changes nothing: key.age stays byte for byte the file it was.
func TestChangePassphraseWrongOldLeavesTheKeyUntouched(t *testing.T) {
	lowWorkFactor(t)
	dir := t.TempDir()
	v := New(dir)
	if err := v.Set("wiki-reader", "token-id", "id-1", offering("old-phrase")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	before, err := os.ReadFile(v.keyPath())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if err := v.ChangePassphrase("wrong-phrase", "new-phrase"); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("ChangePassphrase() with the wrong old passphrase = %v, want ErrWrongPassphrase", err)
	}

	after, err := os.ReadFile(v.keyPath())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("key.age changed although the old passphrase was wrong")
	}
	reopened := New(dir)
	if _, found, _, err := reopened.Get("wiki-reader", "token-id", offering("old-phrase")); err != nil || !found {
		t.Fatalf("Get() with the original passphrase = found %v, err %v, want true, nil", found, err)
	}
}

func TestChangePassphraseRefusals(t *testing.T) {
	lowWorkFactor(t)
	dir := t.TempDir()

	absent := New(dir)
	if err := absent.ChangePassphrase("old", "new"); !errors.Is(err, ErrNotEncrypted) {
		t.Errorf("ChangePassphrase() of an absent vault = %v, want ErrNotEncrypted", err)
	}

	unencrypted := New(dir)
	if err := unencrypted.Set("wiki-reader", "token-id", "id-1", nil); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := unencrypted.ChangePassphrase("old", "new"); !errors.Is(err, ErrNotEncrypted) {
		t.Errorf("ChangePassphrase() of an unencrypted vault = %v, want ErrNotEncrypted", err)
	}

	encryptedDir := t.TempDir()
	encrypted := New(encryptedDir)
	if err := encrypted.Set("wiki-reader", "token-id", "id-1", offering("s3cret-phrase")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := encrypted.ChangePassphrase("s3cret-phrase", ""); !errors.Is(err, ErrEmptyPassphrase) {
		t.Errorf("ChangePassphrase() to an empty passphrase = %v, want ErrEmptyPassphrase", err)
	}
}

func TestDecrypt(t *testing.T) {
	lowWorkFactor(t)
	dir := t.TempDir()
	v := New(dir)
	if err := v.Set("wiki-reader", "token-id", "id-1", offering("s3cret-phrase")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	// Queue a pending entry while locked, the way another process would leave one behind.
	locked := New(dir)
	if err := locked.Set("wiki-reader", "token-secret", "secret-1", nil); err != nil {
		t.Fatalf("locked Set() error = %v", err)
	}

	toDecrypt := New(dir)
	if err := toDecrypt.Decrypt("s3cret-phrase"); err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}

	if state, err := toDecrypt.State(); err != nil || state != StateUnencrypted {
		t.Fatalf("State() after Decrypt() = %v, %v, want unencrypted, nil", state, err)
	}
	for _, name := range []string{keyFile, recipientFile, secretsFile} {
		if _, err := os.Stat(filepath.Join(v.Dir(), name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survives Decrypt(): %v", name, err)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(v.Dir(), pendingDir)); err == nil && len(entries) != 0 {
		t.Errorf("pending entries survive Decrypt(): %v", entries)
	}

	// The pending entry was merged before the switch, so both roles are there, and no passphrase is
	// needed to read them any more.
	fresh := New(dir)
	for _, want := range []struct{ role, value string }{
		{"token-id", "id-1"}, {"token-secret", "secret-1"},
	} {
		value, found, state, err := fresh.Get("wiki-reader", want.role, nil)
		if err != nil || !found || value != want.value || state != StateUnencrypted {
			t.Errorf("Get(%q) = %q, %v, %v, %v, want %q, true, unencrypted, nil",
				want.role, value, found, state, err, want.value)
		}
	}
}

// A wrong passphrase changes nothing: every encrypted file stays exactly as it was.
func TestDecryptWrongPassphraseLeavesTheVaultUntouched(t *testing.T) {
	lowWorkFactor(t)
	dir := t.TempDir()
	v := New(dir)
	if err := v.Set("wiki-reader", "token-id", "id-1", offering("s3cret-phrase")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	before := filesOf(t, v.Dir())

	fresh := New(dir)
	if err := fresh.Decrypt("wrong-phrase"); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("Decrypt() with the wrong passphrase = %v, want ErrWrongPassphrase", err)
	}

	after := filesOf(t, v.Dir())
	if len(before) != len(after) {
		t.Fatalf("the vault directory changed shape: had %v, has %v", before, after)
	}
	for name, data := range before {
		if !bytes.Equal(data, after[name]) {
			t.Errorf("%s changed although the passphrase was wrong", name)
		}
	}
	if state, err := New(dir).State(); err != nil || state != StateLocked {
		t.Fatalf("State() after the refused Decrypt() = %v, %v, want locked, nil", state, err)
	}
}

func TestDecryptRefusals(t *testing.T) {
	dir := t.TempDir()
	if err := New(dir).Decrypt("anything"); !errors.Is(err, ErrNotEncrypted) {
		t.Errorf("Decrypt() of an absent vault = %v, want ErrNotEncrypted", err)
	}

	unencryptedDir := t.TempDir()
	v := New(unencryptedDir)
	if err := v.Set("wiki-reader", "token-id", "id-1", nil); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := v.Decrypt("anything"); !errors.Is(err, ErrNotEncrypted) {
		t.Errorf("Decrypt() of an unencrypted vault = %v, want ErrNotEncrypted", err)
	}
}

// filesOf reads every regular file directly under dir, keyed by name, so a test can prove a refused write
// changed nothing byte for byte.
func filesOf(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("ReadFile() error = %v", err)
		}
		out[e.Name()] = data
	}
	return out
}
