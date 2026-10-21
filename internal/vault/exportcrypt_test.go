package vault

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"filippo.io/age"
)

func TestExportEncryptionRoundTrip(t *testing.T) {
	lowWorkFactor(t)
	plain := []byte("format: x\nsecret: top-secret-9\n")
	data, err := EncryptExport(plain, "export-phrase")
	if err != nil {
		t.Fatal(err)
	}
	if !IsEncryptedExport(data) || bytes.Contains(data, []byte("top-secret-9")) {
		t.Fatal("the export is not an age file or leaks its plaintext")
	}
	// A standard age reader reads it, which is the emergency path.
	id, _ := age.NewScryptIdentity("export-phrase")
	r, err := age.Decrypt(bytes.NewReader(data), id)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(r); !bytes.Equal(got, plain) {
		t.Errorf("age read %q", got)
	}
	got, err := DecryptExport(data, "export-phrase", 1<<10)
	if err != nil || !bytes.Equal(got, plain) {
		t.Errorf("DecryptExport() = %q, %v", got, err)
	}
}

func TestExportEncryptionFailures(t *testing.T) {
	lowWorkFactor(t)
	if _, err := EncryptExport([]byte("x"), ""); !errors.Is(err, ErrEmptyExportPassphrase) {
		t.Errorf("empty passphrase = %v", err)
	}
	data, _ := EncryptExport([]byte("0123456789"), "right")
	if _, err := DecryptExport(data, "wrong", 100); !errors.Is(err, ErrWrongExportPassphrase) {
		t.Errorf("wrong passphrase = %v", err)
	}
	if _, err := DecryptExport(data, "right", 5); !errors.Is(err, ErrExportTooLarge) {
		t.Errorf("too large = %v", err)
	}
	if _, err := DecryptExport(data[:len(data)-4], "right", 100); err == nil {
		t.Error("a truncated file was decrypted")
	}
	if _, err := DecryptExport(data, "", 100); !errors.Is(err, ErrEmptyExportPassphrase) {
		t.Errorf("empty = %v", err)
	}
	if IsEncryptedExport([]byte("format: qatlas-config-export\n")) {
		t.Error("YAML taken for an age file")
	}
}
