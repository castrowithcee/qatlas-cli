package vault

import (
	"errors"
	"testing"

	"filippo.io/age"
)

// A snapshot carries what the unlock opened, pending entries merged in, and a key that decrypts for the
// recipient a fresh, locked process reads without any passphrase.
func TestSnapshotAndRecipient(t *testing.T) {
	lowWorkFactor(t)
	dir := t.TempDir()

	plain := New(dir)
	if _, err := plain.Recipient(); !errors.Is(err, ErrNotEncrypted) {
		t.Fatalf("Recipient() of an absent vault error = %v, want ErrNotEncrypted", err)
	}
	if err := plain.Set("wiki-reader", "token-id", "synthetic-id", offering("synthetic-passphrase")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	locked := New(dir)
	if _, err := locked.Snapshot(); !errors.Is(err, ErrNotUnlocked) {
		t.Fatalf("Snapshot() of a locked vault error = %v, want ErrNotUnlocked", err)
	}
	recipient, err := locked.Recipient()
	if err != nil {
		t.Fatalf("Recipient() of a locked vault error = %v", err)
	}
	if err := locked.Set("wiki-reader", "token-secret", "synthetic-secret", nil); err != nil {
		t.Fatalf("pending Set() error = %v", err)
	}

	if merged, err := locked.Unlock("synthetic-passphrase"); err != nil || merged != 1 {
		t.Fatalf("Unlock() = %d, %v, want one merged entry", merged, err)
	}
	snap, err := locked.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	want := map[string]string{"token-id": "synthetic-id", "token-secret": "synthetic-secret"}
	if got := snap.Secrets["wiki-reader"]; len(got) != len(want) || got["token-id"] != want["token-id"] ||
		got["token-secret"] != want["token-secret"] {
		t.Fatalf("Snapshot().Secrets = %v, want %v", snap.Secrets, want)
	}
	snap.Secrets["wiki-reader"]["token-id"] = "changed"
	if value, _, _, _ := locked.Get("wiki-reader", "token-id", nil); value != "synthetic-id" {
		t.Fatalf("changing a snapshot changed the vault")
	}

	key, err := age.ParseX25519Identity(snap.Identity)
	if err != nil {
		t.Fatalf("Snapshot().Identity does not parse: %v", err)
	}
	if key.Recipient().String() != recipient {
		t.Fatalf("Snapshot().Identity does not belong to Recipient()")
	}
}
