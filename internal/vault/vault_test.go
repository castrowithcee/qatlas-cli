package vault

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// lowWorkFactor keeps every encrypted-vault test fast: scrypt at the real work factor would make the whole
// suite take seconds per passphrase instead of milliseconds.
func lowWorkFactor(t *testing.T) {
	t.Helper()
	original := scryptLogN
	scryptLogN = 4
	t.Cleanup(func() { scryptLogN = original })
}

func offering(passphrase string) PassphraseFunc {
	return func(string) (string, error) { return passphrase, nil }
}

func TestUnencryptedRoundTrip(t *testing.T) {
	v := New(t.TempDir())

	if state, err := v.State(); err != nil || state != StateAbsent {
		t.Fatalf("State() = %v, %v, want absent, nil", state, err)
	}

	if err := v.Set("wiki-reader", "token-id", "synthetic-token", nil); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if state, err := v.State(); err != nil || state != StateUnencrypted {
		t.Fatalf("State() = %v, %v, want unencrypted, nil", state, err)
	}

	value, found, state, err := v.Get("wiki-reader", "token-id", nil)
	if err != nil || !found || value != "synthetic-token" || state != StateUnencrypted {
		t.Fatalf("Get() = %q, %v, %v, %v", value, found, state, err)
	}

	if _, found, _, err := v.Get("wiki-reader", "no-such-role", nil); err != nil || found {
		t.Fatalf("Get() of an unknown role = found %v, err %v, want false, nil", found, err)
	}

	status, err := v.Status()
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Entries != 1 || status.Warning == "" {
		t.Fatalf("Status() = %+v, want one entry and a warning", status)
	}

	removed, err := v.Delete("wiki-reader", "token-id", nil)
	if err != nil || !removed {
		t.Fatalf("Delete() = %v, %v, want true, nil", removed, err)
	}
	if _, found, _, err := v.Get("wiki-reader", "token-id", nil); err != nil || found {
		t.Fatalf("Get() after Delete() = found %v, err %v, want false, nil", found, err)
	}
}

func TestStatusOfAnAbsentAndALockedVault(t *testing.T) {
	dir := t.TempDir()
	v := New(dir)
	st, err := v.Status()
	if err != nil || st.State != StateAbsent || st.Entries != 0 || st.Pending != 0 || st.Warning != "" {
		t.Fatalf("Status() of an absent vault = %+v, %v", st, err)
	}

	lowWorkFactor(t)
	if err := v.Set("wiki-reader", "token-id", "id-1", offering("s3cret-phrase")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	locked := New(dir)
	st, err = locked.Status()
	if err != nil || st.State != StateLocked || st.Entries != -1 {
		t.Fatalf("Status() of a locked vault = %+v, %v, want state locked and entries -1", st, err)
	}
}

func TestEncryptedRoundTrip(t *testing.T) {
	lowWorkFactor(t)
	dir := t.TempDir()

	created := New(dir)
	if err := created.Set("wiki-reader", "token-id", "synthetic-token", offering("s3cret-phrase")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	// The process that created the vault has it unlocked already.
	if state, err := created.State(); err != nil || state != StateUnlocked {
		t.Fatalf("State() right after creation = %v, %v, want unlocked, nil", state, err)
	}

	// A fresh Vault value stands in for the next process: nothing is cached.
	reopened := New(dir)
	if state, err := reopened.State(); err != nil || state != StateLocked {
		t.Fatalf("State() of a fresh Vault = %v, %v, want locked, nil", state, err)
	}
	if _, _, _, err := reopened.Get("wiki-reader", "token-id", nil); !errors.Is(err, ErrNoTerminal) {
		t.Fatalf("Get() without a passphrase source = %v, want ErrNoTerminal", err)
	}
	if _, _, _, err := reopened.Get("wiki-reader", "token-id", offering("wrong-phrase")); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("Get() with the wrong passphrase = %v, want ErrWrongPassphrase", err)
	}

	value, found, state, err := reopened.Get("wiki-reader", "token-id", offering("s3cret-phrase"))
	if err != nil || !found || value != "synthetic-token" || state != StateUnlocked {
		t.Fatalf("Get() = %q, %v, %v, %v", value, found, state, err)
	}
	// The passphrase is asked once per process: a second read uses the cached identity.
	if _, _, _, err := reopened.Get("wiki-reader", "token-id", nil); err != nil {
		t.Fatalf("Get() after unlock, without a passphrase source, error = %v", err)
	}
}

func TestPendingMerge(t *testing.T) {
	lowWorkFactor(t)
	dir := t.TempDir()

	created := New(dir)
	if err := created.Set("wiki-reader", "token-id", "id-1", offering("s3cret-phrase")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Two locked writes: one adds a role to the existing credential, one creates a new one. Neither needs
	// the passphrase.
	locked := New(dir)
	if err := locked.Set("wiki-reader", "token-secret", "secret-1", nil); err != nil {
		t.Fatalf("locked Set() of an existing credential error = %v", err)
	}
	if err := locked.Set("telegram-notifier", "bot-token", "bot-1", nil); err != nil {
		t.Fatalf("locked Set() of a new credential error = %v", err)
	}

	status, err := locked.Status()
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Pending != 2 {
		t.Fatalf("Status().Pending = %d, want 2", status.Pending)
	}

	merged, err := locked.Unlock("s3cret-phrase")
	if err != nil {
		t.Fatalf("Unlock() error = %v", err)
	}
	if merged != 2 {
		t.Fatalf("Unlock() merged = %d, want 2", merged)
	}

	if entries, err := os.ReadDir(filepath.Join(dir, DirName, pendingDir)); err != nil || len(entries) != 0 {
		t.Fatalf("pending directory after Unlock() = %v, %v, want empty", entries, err)
	}

	for _, want := range []struct{ name, role, value string }{
		{"wiki-reader", "token-id", "id-1"},
		{"wiki-reader", "token-secret", "secret-1"},
		{"telegram-notifier", "bot-token", "bot-1"},
	} {
		value, found, _, err := locked.Get(want.name, want.role, nil)
		if err != nil || !found || value != want.value {
			t.Fatalf("Get(%q, %q) = %q, %v, %v, want %q, true, nil", want.name, want.role, value, found, err, want.value)
		}
	}

	status, err = locked.Status()
	if err != nil || status.Entries != 2 || status.Pending != 0 {
		t.Fatalf("Status() after merge = %+v, %v, want 2 entries, 0 pending", status, err)
	}
}

func TestDeleteNeedsUnlock(t *testing.T) {
	lowWorkFactor(t)
	dir := t.TempDir()

	created := New(dir)
	if err := created.Set("wiki-reader", "token-id", "id-1", offering("s3cret-phrase")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	locked := New(dir)
	if _, err := locked.Delete("wiki-reader", "token-id", nil); !errors.Is(err, ErrNoTerminal) {
		t.Fatalf("Delete() without a passphrase source = %v, want ErrNoTerminal", err)
	}

	removed, err := locked.Delete("wiki-reader", "token-id", offering("s3cret-phrase"))
	if err != nil || !removed {
		t.Fatalf("Delete() = %v, %v, want true, nil", removed, err)
	}
	if _, found, _, err := locked.Get("wiki-reader", "token-id", nil); err != nil || found {
		t.Fatalf("Get() after Delete() = found %v, err %v, want false, nil", found, err)
	}
}

func TestPermissionRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode is not meaningful on Windows")
	}
	dir := t.TempDir()
	v := New(dir)
	if err := v.Set("wiki-reader", "token-id", "id-1", nil); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	path := filepath.Join(dir, DirName, plainFile)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}

	_, _, _, err := v.Get("wiki-reader", "token-id", nil)
	var perm *PermissionError
	if !errors.As(err, &perm) {
		t.Fatalf("Get() of a widened file error = %v, want *PermissionError", err)
	}
}

func TestDirAndFileModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode is not meaningful on Windows")
	}
	lowWorkFactor(t)
	dir := t.TempDir()
	v := New(dir)
	if err := v.Set("wiki-reader", "token-id", "id-1", offering("s3cret-phrase")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	info, err := os.Stat(v.Dir())
	if err != nil || info.Mode().Perm() != dirMode {
		t.Fatalf("vault directory mode = %v, %v, want %o", info, err, dirMode)
	}
	for _, name := range []string{keyFile, recipientFile, secretsFile} {
		info, err := os.Stat(filepath.Join(v.Dir(), name))
		if err != nil || info.Mode().Perm() != fileMode {
			t.Fatalf("%s mode = %v, %v, want %o", name, info, err, fileMode)
		}
	}
}

// A document of a schema version this build does not know is refused rather than read as if it were
// current, and never silently rewritten to the current schema by a later write.
func TestUnsupportedSchemaVersionIsRejected(t *testing.T) {
	dir := t.TempDir()
	v := New(dir)
	if err := v.Set("wiki-reader", "token-id", "id-1", nil); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	path := filepath.Join(dir, DirName, plainFile)
	future := `{"schema":2,"entries":{}}`
	if err := os.WriteFile(path, []byte(future), fileMode); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, _, _, err := v.Get("wiki-reader", "token-id", nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported vault schema version 2") {
		t.Fatalf("Get() of a future schema = %v, want an error naming the unsupported version", err)
	}

	// Set must refuse it too, rather than silently overwriting a document it cannot read.
	if err := v.Set("wiki-reader", "token-secret", "id-2", nil); err == nil ||
		!strings.Contains(err.Error(), "unsupported vault schema version 2") {
		t.Fatalf("Set() against a future schema = %v, want the same refusal", err)
	}
}
