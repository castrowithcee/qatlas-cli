package vault

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	mac, err := settingsMAC(v.identity.key, settingsDocument{Schema: 1, UpdateHandover: "handover"})
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
			m, _ := settingsMAC(v.identity.key, settingsDocument{Schema: 2, UpdateHandover: "handover"})
			writeSettings(t, v, recipient, `{"schema":3,"update_handover":"handover","mac":"`+m+`"}`)
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
	mac, err := settingsMAC(v.identity.key, settingsDocument{Schema: 1, UpdateHandover: "never"})
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

func wantSettings(t *testing.T, v *Vault, want Settings, wantErr error) {
	t.Helper()
	got, err := v.Settings()
	if got != want || !errors.Is(err, wantErr) {
		t.Fatalf("Settings() = %+v, %v, want %+v, %v", got, err, want, wantErr)
	}
	read, err := ReadSettings(v.Dir(), v.identity.key)
	if read != want || !errors.Is(err, wantErr) {
		t.Fatalf("ReadSettings() = %+v, %v, want %+v, %v", read, err, want, wantErr)
	}
}

func TestSettingsDefaultsAndSafeValues(t *testing.T) {
	v := tokenVault(t)
	wantSettings(t, v, Settings{UpdateHandover, 12 * time.Hour, 10 * time.Minute, 90, 0}, nil)
	if got, err := ReadSettings(v.Dir(), nil); got != SafeSettings() || !errors.Is(err, ErrNotUnlocked) {
		t.Errorf("ReadSettings() without a key = %+v, %v", got, err)
	}
	if safe := SafeSettings(); safe.Update != UpdateLock || safe.IdleTimeout != 12*time.Hour ||
		safe.AdminTimeout != 0 || safe.RetentionDays != 0 || safe.MaxUnlock != 0 {
		t.Errorf("SafeSettings() = %+v", safe)
	}
}

func TestSettingsRoundTripAndNoUpperBound(t *testing.T) {
	v := tokenVault(t)
	for _, want := range []Settings{
		{UpdateLock, time.Second, 0, 1, 0},
		{UpdateHandover, 10000 * time.Hour, 5000 * time.Hour, 100000, 0},
		{UpdateLock, 90 * time.Minute, 30 * time.Second, 7, 0},
		{UpdateHandover, time.Hour, time.Minute, 7, 8 * time.Hour},
	} {
		if err := v.SetSettings(want); err != nil {
			t.Fatalf("SetSettings(%+v) = %v", want, err)
		}
		wantSettings(t, v, want, nil)
	}
}

func TestSetSettingsRejectsInvalidValues(t *testing.T) {
	v := tokenVault(t)
	good := DefaultSettings()
	if err := v.SetSettings(good); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]Settings{
		"behaviour":      {"sometimes", time.Hour, time.Minute, 1, 0},
		"zero idle":      {UpdateLock, 0, time.Minute, 1, 0},
		"negative idle":  {UpdateLock, -time.Hour, time.Minute, 1, 0},
		"negative admin": {UpdateLock, time.Hour, -time.Second, 1, 0},
		"zero retention": {UpdateLock, time.Hour, time.Minute, 0, 0},
		"negative days":  {UpdateLock, time.Hour, time.Minute, -3, 0},
		"negative max":   {UpdateLock, time.Hour, time.Minute, 1, -time.Second},
	} {
		if err := v.SetSettings(bad); err == nil {
			t.Errorf("SetSettings accepted %s", name)
		}
	}
	wantSettings(t, v, good, nil)
}

func TestSetUpdateBehaviourKeepsOtherSettings(t *testing.T) {
	v := tokenVault(t)
	want := Settings{UpdateHandover, 3 * time.Hour, 0, 30, 0}
	if err := v.SetSettings(want); err != nil {
		t.Fatal(err)
	}
	if err := v.SetUpdateBehaviour(UpdateLock); err != nil {
		t.Fatal(err)
	}
	want.Update = UpdateLock
	wantSettings(t, v, want, nil)
}

func TestTamperedSettingsAreUntrustedAsAWhole(t *testing.T) {
	v := tokenVault(t)
	want := Settings{UpdateHandover, 3 * time.Hour, 5 * time.Minute, 30, 2 * time.Hour}
	if err := v.SetSettings(want); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(v.Dir(), settingsFile)
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	recipient, _ := v.Recipient()
	foreign, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	doc := func(mutate func(d *settingsDocument)) string {
		d := settingsDocument{Schema: 2, UpdateHandover: "handover",
			IdleTimeoutNS: int64(3 * time.Hour), AdminTimeoutNS: int64(5 * time.Minute), RetentionDays: 30,
			MaxUnlockNS: int64(2 * time.Hour)}
		d.MAC, _ = settingsMAC(v.identity.key, d)
		mutate(&d)
		b, _ := json.Marshal(d)
		return string(b)
	}
	// Each field changed after the check value was computed, plus invalid values under a valid check value.
	cases := map[string]string{
		"behaviour":  doc(func(d *settingsDocument) { d.UpdateHandover = "lock" }),
		"idle":       doc(func(d *settingsDocument) { d.IdleTimeoutNS++ }),
		"admin":      doc(func(d *settingsDocument) { d.AdminTimeoutNS++ }),
		"retention":  doc(func(d *settingsDocument) { d.RetentionDays++ }),
		"max unlock": doc(func(d *settingsDocument) { d.MaxUnlockNS++ }),
		"schema":     doc(func(d *settingsDocument) { d.Schema = 1 }),
		"unknown":    doc(func(d *settingsDocument) { d.Schema = 3 }),
		"garbage":    `handover`,
		"no mac":     doc(func(d *settingsDocument) { d.MAC = "" }),
		"neg idle":   signed(v, settingsDocument{Schema: 2, UpdateHandover: "lock", IdleTimeoutNS: -1, RetentionDays: 1}),
		"neg admin":  signed(v, settingsDocument{Schema: 2, UpdateHandover: "lock", IdleTimeoutNS: 1, AdminTimeoutNS: -1, RetentionDays: 1}),
		"neg max":    signed(v, settingsDocument{Schema: 2, UpdateHandover: "lock", IdleTimeoutNS: 1, RetentionDays: 1, MaxUnlockNS: -1}),
		"zero days":  signed(v, settingsDocument{Schema: 2, UpdateHandover: "lock", IdleTimeoutNS: 1}),
		"bad update": signed(v, settingsDocument{Schema: 2, UpdateHandover: "never", IdleTimeoutNS: 1, RetentionDays: 1}),
	}
	for name, plain := range cases {
		t.Run(name, func(t *testing.T) {
			writeSettings(t, v, recipient, plain)
			wantSettings(t, v, SafeSettings(), ErrSettingsUntrusted)
		})
	}
	t.Run("foreign recipient", func(t *testing.T) {
		writeSettings(t, v, foreign.Recipient().String(), doc(func(*settingsDocument) {}))
		wantSettings(t, v, SafeSettings(), ErrSettingsUntrusted)
	})
	t.Run("changed bytes", func(t *testing.T) {
		changed := append([]byte(nil), good...)
		changed[len(changed)/2] ^= 0xff
		if err := os.WriteFile(path, changed, 0o600); err != nil {
			t.Fatal(err)
		}
		wantSettings(t, v, SafeSettings(), ErrSettingsUntrusted)
	})
	if err := os.WriteFile(path, good, 0o600); err != nil {
		t.Fatal(err)
	}
	wantSettings(t, v, want, nil)
}

func signed(v *Vault, d settingsDocument) string {
	d.MAC, _ = settingsMAC(v.identity.key, d)
	b, _ := json.Marshal(d)
	return string(b)
}

func TestSchemaOneFileReadsTrustedWithDefaults(t *testing.T) {
	v := tokenVault(t)
	recipient, _ := v.Recipient()
	for _, behaviour := range []UpdateBehaviour{UpdateLock, UpdateHandover} {
		mac, err := settingsMAC(v.identity.key, settingsDocument{Schema: 1, UpdateHandover: string(behaviour)})
		if err != nil {
			t.Fatal(err)
		}
		writeSettings(t, v, recipient, `{"schema":1,"update_handover":"`+string(behaviour)+`","mac":"`+mac+`"}`)
		want := DefaultSettings()
		want.Update = behaviour
		wantSettings(t, v, want, nil)
	}
	// Changing another setting rewrites the file as schema 2.
	if err := v.SetUpdateBehaviour(UpdateHandover); err != nil {
		t.Fatal(err)
	}
	wantSettings(t, v, DefaultSettings(), nil)
}

func TestEffectiveSettingsFollowTheVaultState(t *testing.T) {
	if got, err := EffectiveSettings(nil); got != DefaultSettings() || err != nil {
		t.Errorf("EffectiveSettings(nil) = %+v, %v, want the defaults", got, err)
	}
	if got, err := EffectiveSettings(New(filepath.Join(t.TempDir(), "absent"))); got != DefaultSettings() || err != nil {
		t.Errorf("EffectiveSettings(absent) = %+v, %v, want the defaults", got, err)
	}

	lowWorkFactor(t)
	plain := New(t.TempDir())
	if err := plain.Set("gh", "token", "synthetic", nil); err != nil {
		t.Fatal(err)
	}
	if got, err := EffectiveSettings(plain); got != DefaultSettings() || err != nil {
		t.Errorf("EffectiveSettings(unencrypted) = %+v, %v, want the defaults", got, err)
	}

	v := tokenVault(t)
	want := Settings{UpdateLock, time.Hour, time.Minute, 7, 0}
	if err := v.SetSettings(want); err != nil {
		t.Fatal(err)
	}
	if got, err := EffectiveSettings(v); got != want || err != nil {
		t.Errorf("EffectiveSettings(unlocked) = %+v, %v, want %+v", got, err, want)
	}
	locked := New(filepath.Dir(v.Dir()))
	if got, err := EffectiveSettings(locked); got != SafeSettings() || err != nil {
		t.Errorf("EffectiveSettings(locked) = %+v, %v, want the safe settings", got, err)
	}
	if err := os.WriteFile(filepath.Join(v.Dir(), settingsFile), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := EffectiveSettings(v); got != SafeSettings() || !errors.Is(err, ErrSettingsUntrusted) {
		t.Errorf("EffectiveSettings(untrusted) = %+v, %v, want the safe settings and ErrSettingsUntrusted", got, err)
	}
}
