package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// logEntries returns every line of the invocation log below dir, the configuration directory, decoded.
func logEntries(t *testing.T, dir string) ([]invokelog.Entry, [][]byte) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, vault.DirName, "logs", "*.jsonl"))
	var entries []invokelog.Entry
	var lines [][]byte
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			if line == "" {
				continue
			}
			var entry invokelog.Entry
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatalf("log line %q: %v", line, err)
			}
			entries, lines = append(entries, entry), append(lines, []byte(line))
		}
	}
	return entries, lines
}

func logFields(operation string) invokelog.Fields {
	return invokelog.Fields{Path: "cli", Operation: operation, Effect: "read", Result: "success",
		Duration: time.Millisecond}
}

// verifyJSON runs 'vault logs verify --output json' in a run of its own and decodes its report.
func verifyJSON(t *testing.T, dir string) (int, logsVerifyDocument, string) {
	t.Helper()
	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "logs", "verify", "--config", configIn(dir),
		"--output", "json")
	var doc logsVerifyDocument
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("verify: exit %d, stdout %q, stderr %q: %v", code, stdout, stderr, err)
	}
	return code, doc, stderr
}

// A vault unlocked in this very process signs the entries this process writes; one locked without a vault
// process leaves them unverified, and the chain runs on through both. verify, which never asks for the
// passphrase, then checks the chain alone and reports the check values as not checked; with the vault
// unlocked in its own process it checks them too.
func TestInvokeLogSignsWithAVaultUnlockedHere(t *testing.T) {
	dir := vaultCredentialFixture(t)
	setter := vault.New(dir)
	if err := setter.Set("wiki-vault", "token-id", canaryVault, offeringPassphrase("s3cret-phrase")); err != nil {
		t.Fatal(err)
	}
	logger := invokelog.New(filepath.Join(dir, vault.DirName), 90)

	locked := invokeLogWriter{logger: logger, vault: vault.New(dir)}
	if err := locked.Append(logFields("op.locked")); err != nil {
		t.Fatalf("Append() while locked = %v, want no warning", err)
	}
	unlockedVault := vault.New(dir)
	if _, err := unlockedVault.Unlock("s3cret-phrase"); err != nil {
		t.Fatal(err)
	}
	unlocked := invokeLogWriter{logger: logger, vault: unlockedVault}
	for _, op := range []string{"op.one", "op.two"} {
		if err := unlocked.Append(logFields(op)); err != nil {
			t.Fatalf("Append() while unlocked = %v", err)
		}
	}
	if err := locked.Append(logFields("op.locked.again")); err != nil {
		t.Fatal(err)
	}

	entries, lines := logEntries(t, dir)
	if len(entries) != 4 || entries[0].MAC != "" || entries[1].MAC == "" || entries[2].MAC == "" || entries[3].MAC != "" {
		t.Fatalf("entries = %+v, want unsigned, signed, signed, unsigned", entries)
	}
	for _, line := range lines {
		if strings.Contains(string(line), "AGE-SECRET-KEY") || strings.Contains(string(line), canaryVault) {
			t.Fatalf("a log line holds key material or a secret: %s", line)
		}
	}

	code, doc, stderr := verifyJSON(t, dir)
	if code != exitOK || doc.Broken || doc.MACCheck != macVaultLocked || len(doc.Days) != 1 ||
		doc.Days[0].Unverified != 2 || doc.Days[0].Unchecked != 2 || stderr != "" {
		t.Fatalf("verify while locked: exit %d, report %+v, stderr %q", code, doc, stderr)
	}

	checker, macCheck, done := logChecker(context.Background(), unlockedVault, func(w string) { t.Errorf("warning %q", w) })
	defer done()
	report, err := invokelog.VerifyWith(unlockedVault.Dir(), checker)
	if err != nil || macCheck != macChecked || report.Broken || report.Days[0].Unchecked != 0 || report.Days[0].Changed != 0 {
		t.Fatalf("VerifyWith(unlocked) = %+v, %q, %v, want every check value checked", report, macCheck, err)
	}

	// A changed signed entry is found once the check values are checked, the last signed one included.
	files, _ := filepath.Glob(filepath.Join(dir, vault.DirName, "logs", "*.jsonl"))
	data, _ := os.ReadFile(files[0])
	tampered := strings.Replace(string(data), `"operation":"op.two"`, `"operation":"op.TWO"`, 1)
	if err := os.WriteFile(files[0], []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err = invokelog.VerifyWith(unlockedVault.Dir(), checker)
	if err != nil || !report.Broken || report.Days[0].Changed != 1 {
		t.Fatalf("VerifyWith(tampered) = %+v, %v, want one changed entry", report, err)
	}
}

// Without an encrypted vault there is no key to sign with, and nothing to check with either.
func TestInvokeLogStaysUnsignedWithoutAnEncryptedVault(t *testing.T) {
	dir := vaultCredentialFixture(t)
	code, stdout, stderr := runWithInput(t, &Options{}, "", "invoke", "bookstack.pages.nope", "--config", configIn(dir))
	if code == exitOK || strings.Contains(stderr, "warning") {
		t.Fatalf("invoke: exit %d, stdout %q, stderr %q, want the unknown operation and no warning", code, stdout, stderr)
	}
	entries, _ := logEntries(t, dir)
	if len(entries) != 1 || entries[0].MAC != "" {
		t.Fatalf("entries = %+v, want one unsigned entry", entries)
	}
	code, doc, _ := verifyJSON(t, dir)
	if code != exitOK || doc.MACCheck != macNoVaultKey || doc.Days[0].Unverified != 1 {
		t.Fatalf("verify: exit %d, report %+v", code, doc)
	}
}
