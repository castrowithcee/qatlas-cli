package vault

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// legacyFixture is a vault written by the build that still used the legacy labels. Its secrets are synthetic
// canaries.
type legacyFixture struct {
	Passphrase string `json:"passphrase"`
	Token      string `json:"agent_token"`
	Scope      Scope  `json:"scope"`
}

// copyLegacyVault copies testdata/legacy-v1/<mode> into a fresh temp home and returns the vault on it.
func copyLegacyVault(t *testing.T, mode string) (*Vault, legacyFixture) {
	t.Helper()
	lowWorkFactor(t)
	var meta legacyFixture
	raw, err := os.ReadFile(filepath.Join("testdata", "legacy-v1", "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	v := New(t.TempDir())
	if err := os.MkdirAll(v.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join("testdata", "legacy-v1", mode)
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(v.Dir(), e.Name()), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return v, meta
}

func unlockLegacy(t *testing.T) (*Vault, legacyFixture) {
	t.Helper()
	v, meta := copyLegacyVault(t, "encrypted")
	if _, err := v.Unlock(meta.Passphrase); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	return v, meta
}

func rawSettings(t *testing.T, v *Vault) settingsDocument {
	t.Helper()
	data, err := readFile(v.settingsPath())
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decryptFrom(data, v.identity.key)
	if err != nil {
		t.Fatal(err)
	}
	var doc settingsDocument
	if err := json.Unmarshal(plain, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func rawTokens(t *testing.T, v *Vault) tokenDocument {
	t.Helper()
	data, err := readFile(v.tokensPath())
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decryptFrom(data, v.identity.key)
	if err != nil {
		t.Fatal(err)
	}
	var doc tokenDocument
	if err := json.Unmarshal(plain, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func settingsMACIs(t *testing.T, v *Vault, doc settingsDocument, label string) bool {
	t.Helper()
	mac, err := settingsMACWith(v.identity.key, doc, label)
	if err != nil {
		t.Fatal(err)
	}
	return mac == doc.MAC
}

func tokensMACIs(t *testing.T, v *Vault, doc tokenDocument, label string) bool {
	t.Helper()
	mac, err := tokenMACWith(v.identity, doc.Tokens, label)
	if err != nil {
		t.Fatal(err)
	}
	return mac == doc.MAC
}

func writeSettingsDoc(t *testing.T, v *Vault, doc settingsDocument) {
	t.Helper()
	plain, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	data, err := encryptTo(string(plain), v.identity.recipient)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFile(v.settingsPath(), data); err != nil {
		t.Fatal(err)
	}
}

func writeTokenDoc(t *testing.T, v *Vault, doc tokenDocument) {
	t.Helper()
	plain, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	data, err := encryptTo(string(plain), v.identity.recipient)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFile(v.tokensPath(), data); err != nil {
		t.Fatal(err)
	}
}

// A vault written under the legacy labels keeps working without any action: the secret is released without
// a new approval, the token works and the settings read.
func TestLegacyVaultReadsWithoutAction(t *testing.T) {
	v, meta := unlockLegacy(t)

	value, found, _, err := v.Get("gh-a", "token", nil)
	if err != nil || !found || value != "canary-secret-a" {
		t.Fatalf("Get() = found %v, err %v", found, err)
	}
	if err := v.CheckApproval(meta.Scope); err != nil {
		t.Fatalf("CheckApproval() = %v, want the legacy approval to hold", err)
	}
	bindings, err := v.Bindings()
	if err != nil || !bindings.Allows(meta.Scope, "gh-a") {
		t.Fatalf("Bindings().Allows() = false, err %v", err)
	}
	tokens, err := v.Tokens()
	if err != nil || len(tokens) != 1 || tokens[0].Value != meta.Token {
		t.Fatalf("Tokens() = %d tokens, err %v", len(tokens), err)
	}
	result, err := v.ApproveWithToken(meta.Token, []Scope{meta.Scope}, nil, time.Now())
	if err != nil || len(result.Changes) != 0 {
		t.Fatalf("ApproveWithToken() = %d open changes, err %v; the legacy approval must not read as open",
			len(result.Changes), err)
	}
	settings, err := v.Settings()
	if err != nil || settings.IdleTimeout != 7*time.Hour || settings.RetentionDays != 33 || settings.Update != UpdateLock {
		t.Fatalf("Settings() = %+v, err %v", settings, err)
	}

	// Reading wrote nothing: the files still carry the legacy labels.
	if doc := rawSettings(t, v); !settingsMACIs(t, v, doc, legacySettingsMACLabel) || settingsMACIs(t, v, doc, settingsMACLabel) {
		t.Fatal("settings.age changed on a read")
	}
	if doc := rawTokens(t, v); !tokensMACIs(t, v, doc, legacyTokenMACLabel) || tokensMACIs(t, v, doc, tokenMACLabel) {
		t.Fatal("tokens.age changed on a read")
	}
}

// The next write re-labels what it writes and nothing is ever written under a legacy label.
func TestLegacyVaultRewritesUnderNewLabels(t *testing.T) {
	v, meta := unlockLegacy(t)

	settings, _ := v.Settings()
	settings.RetentionDays = 44
	if err := v.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	doc := rawSettings(t, v)
	if !settingsMACIs(t, v, doc, settingsMACLabel) || settingsMACIs(t, v, doc, legacySettingsMACLabel) {
		t.Fatal("settings.age was not written under the new label only")
	}

	created, err := v.CreateToken("agent-b", []string{"conn-a"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tdoc := rawTokens(t, v)
	if !tokensMACIs(t, v, tdoc, tokenMACLabel) || tokensMACIs(t, v, tdoc, legacyTokenMACLabel) {
		t.Fatal("tokens.age was not written under the new label only")
	}

	// Any regular write of secrets.age re-labels the legacy approval.
	if err := v.Set("gh-b", "token", "canary-secret-b", nil); err != nil {
		t.Fatal(err)
	}
	approvals, err := v.Approvals()
	if err != nil {
		t.Fatal(err)
	}
	got := approvals["conn-a"].Fingerprint
	if got != Fingerprint(meta.Scope, approvals["conn-a"].CredentialID) ||
		got == fingerprintWith(legacyFingerprintLabel, meta.Scope, approvals["conn-a"].CredentialID) {
		t.Fatal("the approval was not re-labelled to the new fingerprint")
	}

	// A fresh process reads all of it back.
	again := New(filepath.Dir(v.Dir()))
	if _, err := again.Unlock(meta.Passphrase); err != nil {
		t.Fatal(err)
	}
	if err := again.CheckApproval(meta.Scope); err != nil {
		t.Fatalf("CheckApproval() after rewrite = %v", err)
	}
	if s, err := again.Settings(); err != nil || s.RetentionDays != 44 {
		t.Fatalf("Settings() = %+v, %v", s, err)
	}
	tokens, err := again.Tokens()
	if err != nil || len(tokens) != 2 {
		t.Fatalf("Tokens() = %d, %v", len(tokens), err)
	}
	if tokens[0].Value != meta.Token && tokens[1].Value != meta.Token {
		t.Fatal("the legacy token was lost by the rewrite")
	}
	_ = created
}

// A rename through the tokens revoke path also writes under the new label.
func TestLegacyRevokeWritesNewLabel(t *testing.T) {
	v, _ := unlockLegacy(t)
	if err := v.RevokeToken("agent-a"); err != nil {
		t.Fatal(err)
	}
	doc := rawTokens(t, v)
	if !tokensMACIs(t, v, doc, tokenMACLabel) || tokensMACIs(t, v, doc, legacyTokenMACLabel) {
		t.Fatal("tokens.age was not written under the new label only")
	}
}

// The unencrypted vault carries no label and reads as before.
func TestLegacyPlainVaultReads(t *testing.T) {
	v, _ := copyLegacyVault(t, "plain")
	value, found, state, err := v.Get("gh-a", "token", nil)
	if err != nil || !found || value != "canary-pt" || state != StateUnencrypted {
		t.Fatalf("Get() = found %v, state %v, err %v", found, state, err)
	}
}

// A value that is valid under neither label is refused, and so is one authenticated with the label of another
// kind of data.
func TestSettingsRejectForeignAndTamperedMAC(t *testing.T) {
	v, _ := unlockLegacy(t)
	base := rawSettings(t, v)

	for name, label := range map[string]string{
		"token label":   tokenMACLabel,
		"legacy token":  legacyTokenMACLabel,
		"fingerprint":   fingerprintLabel,
		"legacy finger": legacyFingerprintLabel,
		"unknown label": "qatlas-connect vault settings v0",
	} {
		doc := base
		mac, err := settingsMACWith(v.identity.key, doc, label)
		if err != nil {
			t.Fatal(err)
		}
		doc.MAC = mac
		writeSettingsDoc(t, v, doc)
		if _, err := ReadSettings(v.Dir(), v.identity.key); !errors.Is(err, ErrSettingsUntrusted) {
			t.Errorf("%s: ReadSettings() = %v, want ErrSettingsUntrusted", name, err)
		}
	}

	for _, label := range []string{settingsMACLabel, legacySettingsMACLabel} {
		doc := base
		mac, _ := settingsMACWith(v.identity.key, doc, label)
		doc.MAC = mac
		doc.RetentionDays++ // changed after the MAC was computed
		writeSettingsDoc(t, v, doc)
		if _, err := ReadSettings(v.Dir(), v.identity.key); !errors.Is(err, ErrSettingsUntrusted) {
			t.Errorf("tampered under %q: ReadSettings() = %v, want ErrSettingsUntrusted", label, err)
		}
	}

	// Both labels, when correct, are accepted.
	for _, label := range []string{settingsMACLabel, legacySettingsMACLabel} {
		doc := base
		mac, _ := settingsMACWith(v.identity.key, doc, label)
		doc.MAC = mac
		writeSettingsDoc(t, v, doc)
		if _, err := ReadSettings(v.Dir(), v.identity.key); err != nil {
			t.Errorf("label %q: ReadSettings() = %v", label, err)
		}
	}
}

func TestTokensRejectForeignAndTamperedMAC(t *testing.T) {
	v, _ := unlockLegacy(t)
	base := rawTokens(t, v)

	for name, label := range map[string]string{
		"settings label":  settingsMACLabel,
		"legacy settings": legacySettingsMACLabel,
		"fingerprint":     fingerprintLabel,
		"unknown label":   "qatlas-connect agent tokens v0",
	} {
		doc := base
		mac, err := tokenMACWith(v.identity, doc.Tokens, label)
		if err != nil {
			t.Fatal(err)
		}
		doc.MAC = mac
		writeTokenDoc(t, v, doc)
		if _, err := v.Tokens(); !errors.Is(err, ErrTokensTampered) {
			t.Errorf("%s: Tokens() = %v, want ErrTokensTampered", name, err)
		}
	}

	for _, label := range []string{tokenMACLabel, legacyTokenMACLabel} {
		doc := base
		mac, _ := tokenMACWith(v.identity, doc.Tokens, label)
		doc.MAC = mac
		doc.Tokens = append([]Token(nil), doc.Tokens...)
		doc.Tokens[0].Models = []string{"other"} // changed after the MAC was computed
		writeTokenDoc(t, v, doc)
		if _, err := v.Tokens(); !errors.Is(err, ErrTokensTampered) {
			t.Errorf("tampered under %q: Tokens() = %v, want ErrTokensTampered", label, err)
		}
	}
}

// A fingerprint is accepted under the current or the legacy label only, never for a changed scope or under a
// foreign label, and Fingerprint itself only ever yields the current one.
func TestFingerprintMatches(t *testing.T) {
	scope := vorbildScope("")
	current := Fingerprint(scope, "id1")
	legacy := fingerprintWith(legacyFingerprintLabel, scope, "id1")
	if current == legacy {
		t.Fatal("the labels do not separate the fingerprints")
	}
	if !FingerprintMatches(current, scope, "id1") || !FingerprintMatches(legacy, scope, "id1") {
		t.Fatal("a fingerprint of either label must match")
	}
	changed := scope
	changed.Targets = []string{"repos/o/other"}
	foreign := fingerprintWith("qatlas-connect agent tokens v1", scope, "id1")
	for name, stored := range map[string]string{"current": current, "legacy": legacy, "foreign": foreign, "empty": ""} {
		if name == "foreign" || name == "empty" {
			if FingerprintMatches(stored, scope, "id1") {
				t.Errorf("%s fingerprint matched", name)
			}
			continue
		}
		if FingerprintMatches(stored, changed, "id1") || FingerprintMatches(stored, scope, "id2") {
			t.Errorf("%s fingerprint matched a changed scope or entry", name)
		}
	}
}
