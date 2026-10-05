package vault

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
)

func wantBehaviour(t *testing.T, v *Vault, want UpdateBehaviour, wantErr error) {
	t.Helper()
	got, err := v.UpdateBehaviour()
	if got != want || !errors.Is(err, wantErr) {
		t.Fatalf("UpdateBehaviour() = %q, %v, want %q, %v", got, err, want, wantErr)
	}
	read, err := ReadUpdateBehaviour(v.Dir(), v.identity.key)
	if read != want || !errors.Is(err, wantErr) {
		t.Fatalf("ReadUpdateBehaviour() = %q, %v, want %q, %v", read, err, want, wantErr)
	}
}

func writeSettings(t *testing.T, v *Vault, recipient string, plain string) {
	t.Helper()
	data, err := encryptToRecipient(plain, []byte(recipient))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.Dir(), settingsFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateBehaviourRoundTrip(t *testing.T) {
	v := tokenVault(t)
	wantBehaviour(t, v, UpdateHandover, nil)
	if _, err := os.Stat(filepath.Join(v.Dir(), settingsFile)); !os.IsNotExist(err) {
		t.Fatalf("settings.age exists before a setting was changed: %v", err)
	}
	for _, want := range []UpdateBehaviour{UpdateLock, UpdateHandover, UpdateLock} {
		if err := v.SetUpdateBehaviour(want); err != nil {
			t.Fatalf("SetUpdateBehaviour(%q) = %v", want, err)
		}
		wantBehaviour(t, v, want, nil)
	}
	if err := v.SetUpdateBehaviour("sometimes"); err == nil {
		t.Error("SetUpdateBehaviour() accepted an unknown value")
	}
	wantBehaviour(t, v, UpdateLock, nil)
}

func TestUpdateBehaviourNeedsAnEncryptedUnlockedVault(t *testing.T) {
	lowWorkFactor(t)
	plain := New(t.TempDir())
	if err := plain.Set("gh", "token", "synthetic", nil); err != nil {
		t.Fatal(err)
	}
	if err := plain.SetUpdateBehaviour(UpdateLock); !errors.Is(err, ErrNotEncrypted) {
		t.Errorf("SetUpdateBehaviour() on an unencrypted vault = %v, want ErrNotEncrypted", err)
	}
	v := tokenVault(t)
	locked := New(filepath.Dir(v.Dir()))
	if err := locked.SetUpdateBehaviour(UpdateLock); !errors.Is(err, ErrNotUnlocked) {
		t.Errorf("SetUpdateBehaviour() on a locked vault = %v, want ErrNotUnlocked", err)
	}
}

func TestTamperedUpdateBehaviourLocks(t *testing.T) {
	v := tokenVault(t)
	if err := v.SetUpdateBehaviour(UpdateHandover); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(v.Dir(), settingsFile)
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := v.Recipient()
	if err != nil {
		t.Fatal(err)
	}
	mac, err := settingsMAC(v.identity.key, "handover")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	changed := append([]byte(nil), good...)
	changed[len(changed)/2] ^= 0xff

	cases := map[string]func(){
		"changed bytes": func() { _ = os.WriteFile(path, changed, 0o600) },
		"foreign recipient": func() {
			writeSettings(t, v, foreign.Recipient().String(),
				`{"schema":1,"update_handover":"handover","mac":"`+mac+`"}`)
		},
		"wrong check value": func() {
			writeSettings(t, v, recipient, `{"schema":1,"update_handover":"handover","mac":"00"}`)
		},
		"forged value under a valid check value of another": func() {
			writeSettings(t, v, recipient, `{"schema":1,"update_handover":"lock","mac":"`+mac+`"}`)
		},
		"unknown schema": func() {
			m, _ := settingsMAC(v.identity.key, "handover")
			writeSettings(t, v, recipient, `{"schema":2,"update_handover":"handover","mac":"`+m+`"}`)
		},
		"not json": func() { writeSettings(t, v, recipient, `handover`) },
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			tamper()
			wantBehaviour(t, v, UpdateLock, ErrSettingsUntrusted)
			if err := os.WriteFile(path, good, 0o600); err != nil {
				t.Fatal(err)
			}
			wantBehaviour(t, v, UpdateHandover, nil)
		})
	}
}

func TestUnknownUpdateBehaviourWithValidCheckValueLocks(t *testing.T) {
	v := tokenVault(t)
	recipient, _ := v.Recipient()
	mac, err := settingsMAC(v.identity.key, "never")
	if err != nil {
		t.Fatal(err)
	}
	writeSettings(t, v, recipient, `{"schema":1,"update_handover":"never","mac":"`+mac+`"}`)
	wantBehaviour(t, v, UpdateLock, ErrSettingsUntrusted)
}

func TestUpdateBehaviourSurvivesPassphraseChangeAndDecryptRemovesIt(t *testing.T) {
	v := tokenVault(t)
	if err := v.SetUpdateBehaviour(UpdateLock); err != nil {
		t.Fatal(err)
	}
	if err := v.ChangePassphrase("s3cret-phrase", "other-phrase"); err != nil {
		t.Fatal(err)
	}
	reopened := New(filepath.Dir(v.Dir()))
	if _, err := reopened.Unlock("other-phrase"); err != nil {
		t.Fatal(err)
	}
	wantBehaviour(t, reopened, UpdateLock, nil)

	if err := reopened.Decrypt("other-phrase"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(v.Dir(), settingsFile)); !os.IsNotExist(err) {
		t.Errorf("settings.age survived Decrypt: %v", err)
	}
}

func TestEncryptCreatesNoSettingsFile(t *testing.T) {
	lowWorkFactor(t)
	v := New(t.TempDir())
	if err := v.Encrypt("s3cret-phrase"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(v.Dir(), settingsFile)); !os.IsNotExist(err) {
		t.Errorf("Encrypt created settings.age: %v", err)
	}
	wantBehaviour(t, v, UpdateHandover, nil)
}
