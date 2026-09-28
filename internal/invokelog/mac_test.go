package invokelog

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
)

func testKey(t *testing.T) (*Key, *age.X25519Identity) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	key, err := DeriveKey(id)
	if err != nil {
		t.Fatalf("DeriveKey() error = %v", err)
	}
	return key, id
}

// signedLogger returns a logger signing with key, its clock fixed at t.
func signedLogger(t *testing.T, dir string, key *Key, at time.Time) (*Logger, func(time.Time)) {
	t.Helper()
	logger, moveTo := clockAt(at)
	logger.vaultDir, logger.retentionDays = dir, 90
	return logger.WithKey(key), moveTo
}

func verifyWith(t *testing.T, dir string, checker Checker) Report {
	t.Helper()
	report, err := VerifyWith(dir, checker)
	if err != nil {
		t.Fatalf("VerifyWith() error = %v", err)
	}
	return report
}

// TestSignedEntriesFollowTheDefinition pins the check value to its documented definition: the hex
// HMAC-SHA256, under the key HKDF derives from the vault key, of the line with an empty mac, while the next
// entry's prev_hash is still the SHA-256 of the whole stored line, check value included.
func TestSignedEntriesFollowTheDefinition(t *testing.T) {
	dir := t.TempDir()
	key, id := testKey(t)
	logger, _ := signedLogger(t, dir, key, time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC))
	mustAppend(t, logger, "op.one")
	mustAppend(t, logger, "op.two")

	lines := readLines(t, filepath.Join(logger.logsDir(), "2026-03-01.jsonl"))
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	derived := hkdfKey(t, id)
	for i, line := range lines {
		var entry Entry
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatal(err)
		}
		entry.MAC = ""
		unsigned, _ := json.Marshal(entry)
		h := hmac.New(sha256.New, derived)
		h.Write(unsigned)
		want := hex.EncodeToString(h.Sum(nil))
		if !bytes.HasSuffix(line, []byte(`"mac":"`+want+`"}`)) {
			t.Fatalf("line %d = %s, want the check value %s as its last field", i, line, want)
		}
		if bytes.Contains(line, []byte(hex.EncodeToString(derived))) {
			t.Fatalf("line %d holds the log key", i)
		}
	}
	var second Entry
	_ = json.Unmarshal(lines[1], &second)
	if second.PrevHash != hashHex(lines[0]) {
		t.Fatalf("prev_hash = %s, want the SHA-256 of the whole signed line", second.PrevHash)
	}

	report := verifyWith(t, dir, key)
	if report.Broken || !report.Checked || report.Days[0].Unverified != 0 || report.Days[0].Changed != 0 ||
		report.Days[0].Unchecked != 0 {
		t.Fatalf("report = %+v, want an intact, checked chain", report)
	}
}

func hkdfKey(t *testing.T, id *age.X25519Identity) []byte {
	t.Helper()
	key, err := DeriveKey(id)
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), key.key...)
}

func TestDeriveKeyIsStablePerVaultKey(t *testing.T) {
	key, id := testKey(t)
	parsed, err := age.ParseX25519Identity(id.String())
	if err != nil {
		t.Fatal(err)
	}
	again, _ := DeriveKey(parsed)
	if !bytes.Equal(key.key, again.key) {
		t.Fatal("the same vault key derived two log keys")
	}
	other, _ := testKey(t)
	if bytes.Equal(key.key, other.key) {
		t.Fatal("two vault keys derived the same log key")
	}
	if bytes.Contains([]byte(id.String()), []byte(hex.EncodeToString(key.key))) ||
		strings.Contains(hex.EncodeToString(key.key), strings.ToLower(id.String())) {
		t.Fatal("the log key is the vault key")
	}

	dir := t.TempDir()
	logger, _ := signedLogger(t, dir, key, time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC))
	mustAppend(t, logger, "op.one")
	if report := verifyWith(t, dir, other); !report.Broken || report.Days[0].Changed != 1 {
		t.Fatalf("report under another vault's key = %+v, want the entry changed", report)
	}

	key.Clear()
	if _, err := key.Check([][]byte{[]byte("{}")}); err == nil {
		t.Fatal("a cleared key still checks")
	}
	if _, err := DeriveKey(nil); err == nil {
		t.Fatal("DeriveKey(nil) succeeded")
	}
}

func TestVerifyDetectsATamperedCheckValue(t *testing.T) {
	dir := t.TempDir()
	key, _ := testKey(t)
	logger, _ := signedLogger(t, dir, key, time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC))
	for _, op := range []string{"op.one", "op.two", "op.three"} {
		mustAppend(t, logger, op)
	}
	path := filepath.Join(logger.logsDir(), "2026-03-01.jsonl")
	lines := readLines(t, path)

	// A check value changed to another well-formed one: the chain after it breaks as well.
	tampered := append([][]byte(nil), lines...)
	tampered[1] = flipLastMACDigit(lines[1])
	writeLines(t, path, tampered)
	if report := verifyWith(t, dir, key); !report.Broken || report.Days[0].Changed != 1 {
		t.Fatalf("report = %+v, want one changed entry", report)
	}

	// A malformed check value is changed even when nobody can check it.
	tampered[1] = bytes.Replace(lines[1], []byte(`"mac":"`), []byte(`"mac":"zz`), 1)
	writeLines(t, path, tampered)
	if report := verifyWith(t, dir, nil); !report.Broken || report.Days[0].Changed != 1 {
		t.Fatalf("report without a checker = %+v, want the malformed check value changed", report)
	}
}

func flipLastMACDigit(line []byte) []byte {
	out := append([]byte(nil), line...)
	at := len(out) - 3 // the last hex digit, before `"}`
	if out[at] == '0' {
		out[at] = '1'
	} else {
		out[at] = '0'
	}
	return out
}

// TestVerifyDetectsAChangedLastLine is what the check value adds: the last line has no successor whose
// prev_hash would catch a change to it, so only its check value can.
func TestVerifyDetectsAChangedLastLine(t *testing.T) {
	dir := t.TempDir()
	key, _ := testKey(t)
	logger, _ := signedLogger(t, dir, key, time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC))
	mustAppend(t, logger, "op.one")
	mustAppend(t, logger, "op.two")
	path := filepath.Join(logger.logsDir(), "2026-03-01.jsonl")
	lines := readLines(t, path)
	lines[1] = bytes.Replace(lines[1], []byte(`"result":"success"`), []byte(`"result":"permission"`), 1)
	writeLines(t, path, lines)

	if report := verifyWith(t, dir, nil); report.Broken || report.Days[0].Unchecked != 2 {
		t.Fatalf("report without a checker = %+v, want an intact chain with 2 unchecked entries", report)
	}
	report := verifyWith(t, dir, key)
	if !report.Broken || report.Days[0].Changed != 1 ||
		!strings.Contains(report.Days[0].Problem, "sequence 2 does not match its check value") {
		t.Fatalf("report = %+v, want the last entry changed", report)
	}
}

// TestMixedChainStaysIntact interleaves unsigned entries, the ones a locked vault writes, with signed ones,
// across a retention cut that is signed too.
func TestMixedChainStaysIntact(t *testing.T) {
	dir := t.TempDir()
	key, _ := testKey(t)
	start := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	signed, moveSigned := signedLogger(t, dir, key, start)
	unsigned, moveUnsigned := clockAt(start)
	unsigned.vaultDir, unsigned.retentionDays = dir, 90

	mustAppend(t, unsigned, "op.unsigned.one")
	mustAppend(t, signed, "op.signed.one")
	mustAppend(t, unsigned, "op.unsigned.two")
	mustAppend(t, signed, "op.signed.two")

	later := start.AddDate(0, 0, 100)
	moveSigned(later)
	moveUnsigned(later)
	mustAppend(t, signed, "op.after.cut")
	mustAppend(t, unsigned, "op.unsigned.after.cut")

	lines := readLines(t, filepath.Join(signed.logsDir(), later.Format("2006-01-02")+".jsonl"))
	var cut Entry
	if err := json.Unmarshal(lines[0], &cut); err != nil || cut.Kind != KindCut || cut.MAC == "" {
		t.Fatalf("first line after retention = %s, want a signed retention-cut marker", lines[0])
	}

	report := verifyWith(t, dir, key)
	if report.Broken || len(report.Days) != 1 {
		t.Fatalf("report = %+v, want one intact day", report)
	}
	day := report.Days[0]
	if day.Entries != 2 || day.Unverified != 1 || day.Changed != 0 || day.Unchecked != 0 {
		t.Fatalf("day = %+v, want 2 entries, 1 unverified", day)
	}
}

func TestMixedChainInOneDay(t *testing.T) {
	dir := t.TempDir()
	key, _ := testKey(t)
	at := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	signed, _ := signedLogger(t, dir, key, at)
	unsigned, _ := clockAt(at)
	unsigned.vaultDir, unsigned.retentionDays = dir, 90
	mustAppend(t, unsigned, "op.one")
	mustAppend(t, signed, "op.two")
	mustAppend(t, unsigned, "op.three")
	mustAppend(t, signed, "op.four")

	report := verifyWith(t, dir, key)
	if day := report.Days[0]; report.Broken || day.Entries != 4 || day.Unverified != 2 || day.Changed != 0 {
		t.Fatalf("report = %+v, want 4 entries, 2 unverified, intact", report)
	}
	if day := verifyWith(t, dir, nil).Days[0]; day.Unverified != 2 || day.Unchecked != 2 {
		t.Fatalf("day without a checker = %+v, want 2 unverified and 2 unchecked", day)
	}
}

type failingChecker struct{}

func (failingChecker) Check([][]byte) ([]bool, error) { return nil, errors.New("synthetic failure") }

type shortChecker struct{}

func (shortChecker) Check([][]byte) ([]bool, error) { return nil, nil }

// TestVerifyFailsClosed makes sure a checker that cannot answer fails Verify instead of passing entries.
func TestVerifyFailsClosed(t *testing.T) {
	dir := t.TempDir()
	key, _ := testKey(t)
	logger, _ := signedLogger(t, dir, key, time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC))
	mustAppend(t, logger, "op.one")
	for name, checker := range map[string]Checker{"failing": failingChecker{}, "short": shortChecker{}} {
		if _, err := VerifyWith(dir, checker); err == nil {
			t.Errorf("VerifyWith(%s checker) error = nil, want the check to fail", name)
		}
	}
}

func TestMissingEntriesAreCounted(t *testing.T) {
	dir := t.TempDir()
	logger, _ := clockAt(time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC))
	logger.vaultDir, logger.retentionDays = dir, 90
	for _, op := range []string{"a", "b", "c", "d", "e"} {
		mustAppend(t, logger, op)
	}
	path := filepath.Join(logger.logsDir(), "2026-03-01.jsonl")
	lines := readLines(t, path)
	writeLines(t, path, [][]byte{lines[0], lines[3], lines[4]})
	if report := verifyWith(t, dir, nil); !report.Broken || report.Days[0].Missing != 2 {
		t.Fatalf("report = %+v, want 2 missing entries", report)
	}
}

func TestValidateRefusesWhatNoInvokeLogs(t *testing.T) {
	good := Fields{Path: "mcp", Client: &ClientInfo{Name: "client", Version: "1.0"}, Operation: "wiki.pages.list",
		Version: 1, Connection: "wiki", Effect: "read", Result: "success", Duration: time.Second}
	if err := good.Validate(); err != nil {
		t.Fatalf("Validate(good) = %v", err)
	}
	bad := map[string]func(*Fields){
		"unknown path":           func(f *Fields) { f.Path = "http" },
		"empty result":           func(f *Fields) { f.Result = "" },
		"result with spaces":     func(f *Fields) { f.Result = "not a code" },
		"unknown effect":         func(f *Fields) { f.Effect = "destroy" },
		"negative version":       func(f *Fields) { f.Version = -1 },
		"negative duration":      func(f *Fields) { f.Duration = -time.Second },
		"endless duration":       func(f *Fields) { f.Duration = MaxDuration + time.Second },
		"newline in operation":   func(f *Fields) { f.Operation = "wiki\n{\"seq\":1}" },
		"escape in connection":   func(f *Fields) { f.Connection = "wiki\x1b[2J" },
		"overlong operation":     func(f *Fields) { f.Operation = strings.Repeat("a", maxTextLength+1) },
		"invalid UTF-8":          func(f *Fields) { f.Connection = "wiki\xff" },
		"control in client name": func(f *Fields) { f.Client = &ClientInfo{Name: "a\x00b"} },
		"overlong client":        func(f *Fields) { f.Client = &ClientInfo{Version: strings.Repeat("1", maxClientLength+1)} },
	}
	for name, change := range bad {
		f := good
		change(&f)
		if err := f.Validate(); err == nil {
			t.Errorf("Validate(%s) = nil, want an error", name)
		} else if strings.Contains(err.Error(), "wiki") || strings.Contains(err.Error(), "\x1b") {
			t.Errorf("Validate(%s) error %q quotes the value", name, err)
		}
	}
}

// TestSignedLogHoldsNoKeyMaterial checks the files a signing logger writes for the vault key and the log
// key, in every encoding they could leak in.
func TestSignedLogHoldsNoKeyMaterial(t *testing.T) {
	dir := t.TempDir()
	key, id := testKey(t)
	logger, _ := signedLogger(t, dir, key, time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC))
	mustAppend(t, logger, "op.one")
	data, err := os.ReadFile(filepath.Join(logger.logsDir(), "2026-03-01.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{id.String(), strings.ToLower(id.String()), hex.EncodeToString(key.key)} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("the log holds key material")
		}
	}
}
