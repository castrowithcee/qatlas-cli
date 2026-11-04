package invokelog

import (
	"bytes"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/cryptotest"
	"time"

	"filippo.io/age"
)

// legacyFixture holds day files written under legacyKeyLabel by the release before keyLabel, signed with
// fixtureKey: a signed retention cut, signed and unsigned invokes, a connection change and an mcp exchange
// over three days. They are never rewritten; a test copies them.
const legacyFixture = "testdata/legacy-label"

// fixtureRecipient is the public key of fixtureKey, so a change to how the deterministic identity comes
// about fails here rather than as a mismatching check value.
const fixtureRecipient = "age1y2gxulng8qv84pn9kahajj92q8x5yr7zuaufaj0edm0e22k0afuswad364"

// fixtureKey returns the vault key legacyFixture was signed with and its log key. The identity comes from a
// fixed random stream, so no key text needs to be stored.
func fixtureKey(t *testing.T) (*Key, *age.X25519Identity) {
	t.Helper()
	cryptotest.SetGlobalRandom(t, 981)
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if id.Recipient().String() != fixtureRecipient {
		t.Fatal("the fixed test vault key changed; the fixture can no longer be checked")
	}
	key, err := DeriveKey(id)
	if err != nil {
		t.Fatal(err)
	}
	return key, id
}

// copyFixture copies legacyFixture into a fresh vault directory and returns it.
func copyFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	logs := filepath.Join(dir, logsDirName)
	if err := os.MkdirAll(logs, dirMode); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(legacyFixture, "*.jsonl"))
	if err != nil || len(files) != 3 {
		t.Fatalf("fixture files = %v, %v, want three days", files, err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(logs, filepath.Base(file)), data, fileMode); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// labelMAC is the check value of unsigned under the key HKDF derives from id with label, computed here
// from the definition rather than through Key.
func labelMAC(t *testing.T, id *age.X25519Identity, label string, unsigned []byte) string {
	t.Helper()
	derived, err := hkdf.Key(sha256.New, []byte(id.String()), nil, label, sha256.Size)
	if err != nil {
		t.Fatal(err)
	}
	h := hmac.New(sha256.New, derived)
	h.Write(unsigned)
	return hex.EncodeToString(h.Sum(nil))
}

func TestLegacyFixtureVerifies(t *testing.T) {
	key, id := fixtureKey(t)
	dir := copyFixture(t)

	report := verifyWith(t, dir, key)
	if report.Broken || !report.Checked || len(report.Days) != 3 {
		t.Fatalf("report = %+v, want three intact, checked days", report)
	}
	want := []DayReport{{Date: "2026-03-01", Entries: 3, Unverified: 1}, {Date: "2026-03-02", Entries: 2},
		{Date: "2026-03-04", Entries: 1}}
	for i, day := range report.Days {
		if day != want[i] {
			t.Fatalf("day %d = %+v, want %+v", i, day, want[i])
		}
	}
	if report := verifyWith(t, dir, nil); report.Broken || report.Days[0].Unchecked != 2 {
		t.Fatalf("report without a checker = %+v, want an intact chain", report)
	}

	// The fixture is what the former release wrote: no mac_key, every check value under the former label.
	for _, line := range allLines(t, dir) {
		var entry Entry
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(line, []byte("mac_key")) {
			t.Fatalf("fixture line %s names a log key", line)
		}
		if entry.MAC == "" {
			continue
		}
		unsigned, mac, err := unsignedOf(line)
		if err != nil || string(mac) != labelMAC(t, id, "qatlas invocation log v1", unsigned) {
			t.Fatalf("fixture line %d is not signed under the former label", entry.Seq)
		}
	}
	assertNoKeyMaterial(t, dir, key, id)
}

// TestAppendAfterLegacyFixtureChangesLabelOnce continues the former release's chain: the entries already
// there stay byte for byte as they were, the first new entry marks the change by naming the current label,
// every later one does too, and the whole chain checks.
func TestAppendAfterLegacyFixtureChangesLabelOnce(t *testing.T) {
	key, id := fixtureKey(t)
	dir := copyFixture(t)
	before := map[string][]byte{}
	for _, name := range []string{"2026-03-01.jsonl", "2026-03-02.jsonl", "2026-03-04.jsonl"} {
		before[name], _ = os.ReadFile(filepath.Join(dir, logsDirName, name))
	}

	signed, moveSigned := signedLogger(t, dir, key, time.Date(2026, 3, 4, 15, 0, 0, 0, time.UTC))
	unsigned, moveUnsigned := clockAt(time.Date(2026, 3, 4, 15, 30, 0, 0, time.UTC))
	unsigned.vaultDir, unsigned.retentionDays = dir, 90
	mustAppend(t, signed, "op.new.one")
	mustAppend(t, unsigned, "op.new.unsigned")
	moveSigned(time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC))
	moveUnsigned(time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC))
	mustAppend(t, signed, "op.new.two")

	for name, old := range before {
		now, _ := os.ReadFile(filepath.Join(dir, logsDirName, name))
		if !bytes.HasPrefix(now, old) {
			t.Fatalf("%s was rewritten", name)
		}
	}
	lines := allLines(t, dir)
	if len(lines) != 10 {
		t.Fatalf("got %d lines, want the 7 of the fixture and 3 new ones", len(lines))
	}
	legacy, added := lines[:7], lines[7:]
	if !bytes.Contains(added[0], []byte(`,"mac_key":2,"mac":"`)) || !bytes.HasSuffix(added[1], []byte(`"prev_hash":"`+hashHex(added[0])+`","mac":""}`)) ||
		!bytes.Contains(added[2], []byte(`,"mac_key":2,"mac":"`)) {
		t.Fatalf("new lines = %s, want the current label on the signed ones only", added)
	}
	var first Entry
	_ = json.Unmarshal(added[0], &first)
	if first.Seq != 9 || first.PrevHash != hashHex(legacy[6]) || first.MACKey != currentMACKey {
		t.Fatalf("first new entry = %+v, want it chained from the fixture's last line", first)
	}
	for _, line := range [][]byte{added[0], added[2]} {
		unsigned, mac, err := unsignedOf(line)
		if err != nil || string(mac) != labelMAC(t, id, "qatlas-connect invocation log v1", unsigned) {
			t.Fatalf("line %s is not signed under the current label", line)
		}
	}

	report := verifyWith(t, dir, key)
	if report.Broken || len(report.Days) != 4 {
		t.Fatalf("report = %+v, want four intact days", report)
	}
	for _, day := range report.Days {
		if day.Changed != 0 || day.Unchecked != 0 || day.Missing != 0 || day.Problem != "" {
			t.Fatalf("day = %+v, want every check value valid", day)
		}
	}
	for _, day := range verifyLines(t, dir, key, "", "") {
		for _, line := range day.Lines {
			want := MACNone
			if line.Entry.MAC != "" {
				want = MACValid
			}
			if line.MAC != want || line.Chain != ChainIntact {
				t.Fatalf("%s line %d = %+v, want intact and %v", day.Date, line.Entry.Seq, line, want)
			}
		}
	}
	assertNoKeyMaterial(t, dir, key, id)
}

func allLines(t *testing.T, dir string) [][]byte {
	t.Helper()
	files, _ := dayFiles(filepath.Join(dir, logsDirName))
	var lines [][]byte
	for _, name := range files {
		lines = append(lines, readLines(t, filepath.Join(dir, logsDirName, name))...)
	}
	return lines
}

func assertNoKeyMaterial(t *testing.T, dir string, key *Key, id *age.X25519Identity) {
	t.Helper()
	data := bytes.Join(allLines(t, dir), nil)
	for _, secret := range []string{id.String(), strings.ToLower(id.String()), hex.EncodeToString(key.key),
		hex.EncodeToString(key.legacy)} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("the log holds key material")
		}
	}
}

// signAs is how chainLines signs one line.
type signAs int

const (
	asUnsigned signAs = iota
	asLegacy          // under legacyKeyLabel, as the former release did
	asCurrent         // under keyLabel, as Append does
	// asCurrentClaimingLegacy and asLegacyClaimingCurrent sign under one label while the line names the other.
	asCurrentClaimingLegacy
	asLegacyClaimingCurrent
)

// chainLines writes one line per entry of how, the n-th on day 2026-03-01 plus days[n], chained the way
// Append chains them and signed with key as how says, and returns the lines.
func chainLines(t *testing.T, dir string, key *Key, how []signAs, days []int) [][]byte {
	t.Helper()
	logs := filepath.Join(dir, logsDirName)
	if err := os.MkdirAll(logs, dirMode); err != nil {
		t.Fatal(err)
	}
	prev := genesisHash
	var lines [][]byte
	for i, as := range how {
		at := time.Date(2026, 3, 1+days[i], 8, i, 0, 0, time.UTC)
		entry := Entry{Seq: uint64(i + 1), Time: at, Path: "cli", Operation: "op.line", Effect: "read",
			Result: "success", PrevHash: prev}
		if as == asCurrent || as == asLegacyClaimingCurrent {
			entry.MACKey = currentMACKey
		}
		raw, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		switch as {
		case asLegacy, asLegacyClaimingCurrent:
			raw = withMAC(raw, key.legacy)
		case asCurrent, asCurrentClaimingLegacy:
			raw = withMAC(raw, key.key)
		}
		lines = append(lines, raw)
		prev = hashHex(raw)
		if err := appendLines(filepath.Join(logs, at.Format("2006-01-02")+".jsonl"), [][]byte{raw}); err != nil {
			t.Fatal(err)
		}
	}
	return lines
}

// withMAC fills the empty mac field of unsigned with its HMAC under raw key bytes, bypassing Key.sign, the
// way a writer under either label would.
func withMAC(unsigned, key []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(unsigned)
	out := append([]byte(nil), bytes.TrimSuffix(unsigned, []byte(`""}`))...)
	return append(out, `"`+hex.EncodeToString(h.Sum(nil))+`"}`...)
}

func TestLabelTransitionRules(t *testing.T) {
	key, _ := testKey(t)
	const (
		u = asUnsigned
		l = asLegacy
		c = asCurrent
	)
	for _, tt := range []struct {
		name        string
		how         []signAs
		days        []int
		broken      bool
		withoutKey  bool // also broken without a checker
		problemPart string
	}{
		{name: "a new chain starts under the current label", how: []signAs{c, c, u, c}},
		{name: "one change from the former label", how: []signAs{l, u, l, c, u, c}, days: []int{0, 0, 1, 1, 2, 2}},
		{name: "former label after the change", how: []signAs{l, c, c, l}, broken: true, withoutKey: true,
			problemPart: "sequence 4 is signed under the former log key"},
		{name: "current back to former", how: []signAs{c, l}, broken: true, withoutKey: true,
			problemPart: "sequence 2 is signed under the former log key"},
		{name: "an unsigned line does not reset the change", how: []signAs{l, c, u, l}, broken: true, withoutKey: true},
		{name: "a later day does not reset the change", how: []signAs{l, c, l}, days: []int{0, 1, 2}, broken: true,
			withoutKey: true},
		{name: "current key behind a line naming the former", how: []signAs{l, asCurrentClaimingLegacy, c}, broken: true,
			problemPart: "sequence 2 does not match its check value"},
		{name: "former key behind a line naming the current", how: []signAs{l, asLegacyClaimingCurrent, c}, broken: true,
			problemPart: "sequence 2 does not match its check value"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.days == nil {
				tt.days = make([]int, len(tt.how))
			}
			dir := t.TempDir()
			chainLines(t, dir, key, tt.how, tt.days)
			report := verifyWith(t, dir, key)
			if report.Broken != tt.broken {
				t.Fatalf("report = %+v, want broken %v", report, tt.broken)
			}
			if tt.problemPart != "" && !strings.Contains(problems(report), tt.problemPart) {
				t.Fatalf("problems %q, want %q", problems(report), tt.problemPart)
			}
			if got := verifyWith(t, dir, nil).Broken; got != tt.withoutKey {
				t.Fatalf("broken without a checker = %v, want %v", got, tt.withoutKey)
			}
		})
	}
}

func problems(r Report) string {
	var out []string
	for _, day := range r.Days {
		out = append(out, day.Problem)
	}
	return strings.Join(out, "; ")
}

// TestVerifyDetectsAnAttackOnTheLabelChange changes the line that marks the change, the first one naming
// the current label, every way someone without the vault key could, and expects each to break the chain.
func TestVerifyDetectsAnAttackOnTheLabelChange(t *testing.T) {
	key, _ := testKey(t)
	how := []signAs{asLegacy, asLegacy, asCurrent, asCurrent, asCurrent}
	const mark = 2 // the index of the line that marks the change
	for _, tt := range []struct {
		name string
		edit func(lines [][]byte) [][]byte
		// withoutKey is whether the chain alone already shows the break.
		withoutKey bool
	}{
		{name: "mark removed", withoutKey: true, edit: func(lines [][]byte) [][]byte {
			return append(append([][]byte(nil), lines[:mark]...), lines[mark+1:]...)
		}},
		{name: "mark duplicated", withoutKey: true, edit: func(lines [][]byte) [][]byte {
			return append(append([][]byte(nil), lines[:mark+1]...), lines[mark:]...)
		}},
		{name: "mark moved earlier", withoutKey: true, edit: func(lines [][]byte) [][]byte {
			return [][]byte{lines[0], lines[mark], lines[1], lines[3], lines[4]}
		}},
		{name: "mark moved later", withoutKey: true, edit: func(lines [][]byte) [][]byte {
			return [][]byte{lines[0], lines[1], lines[3], lines[mark], lines[4]}
		}},
		{name: "mark's label dropped", withoutKey: true, edit: func(lines [][]byte) [][]byte {
			return replaceAt(lines, mark, bytes.Replace(lines[mark], []byte(`,"mac_key":2`), nil, 1))
		}},
		{name: "mark's label changed", withoutKey: true, edit: func(lines [][]byte) [][]byte {
			return replaceAt(lines, mark, bytes.Replace(lines[mark], []byte(`"mac_key":2`), []byte(`"mac_key":3`), 1))
		}},
		{name: "mark's label moved within the line", withoutKey: true, edit: func(lines [][]byte) [][]byte {
			moved := bytes.Replace(lines[mark], []byte(`,"mac_key":2`), nil, 1)
			return replaceAt(lines, mark, bytes.Replace(moved, []byte(`{"seq":3,`), []byte(`{"seq":3,"mac_key":2,`), 1))
		}},
		{name: "a second mark put on a former line", withoutKey: true, edit: func(lines [][]byte) [][]byte {
			return replaceAt(lines, 1, bytes.Replace(lines[1], []byte(`,"mac":"`), []byte(`,"mac_key":2,"mac":"`), 1))
		}},
		{name: "every line after the mark relabeled as former", withoutKey: true, edit: func(lines [][]byte) [][]byte {
			out := append([][]byte(nil), lines...)
			for i := mark + 1; i < len(out); i++ {
				out[i] = bytes.Replace(out[i], []byte(`,"mac_key":2`), nil, 1)
			}
			return out
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			lines := chainLines(t, dir, key, how, make([]int, len(how)))
			if report := verifyWith(t, dir, key); report.Broken {
				t.Fatalf("intact report = %+v", report)
			}
			writeLines(t, filepath.Join(dir, logsDirName, "2026-03-01.jsonl"), tt.edit(lines))
			if report := verifyWith(t, dir, key); !report.Broken {
				t.Fatalf("report = %+v, want the chain broken", report)
			}
			if got := verifyWith(t, dir, nil).Broken; got != tt.withoutKey {
				t.Fatalf("broken without a checker = %v, want %v", got, tt.withoutKey)
			}
		})
	}

	// A mark on the last line has no successor whose prev_hash would catch a change; its check value does.
	dir := t.TempDir()
	lines := chainLines(t, dir, key, []signAs{asLegacy, asCurrent}, []int{0, 0})
	writeLines(t, filepath.Join(dir, logsDirName, "2026-03-01.jsonl"),
		replaceAt(lines, 1, bytes.Replace(lines[1], []byte(`,"mac_key":2`), nil, 1)))
	if report := verifyWith(t, dir, key); !report.Broken || report.Days[0].Changed != 1 {
		t.Fatalf("report = %+v, want the last line's dropped label changed", report)
	}
	// A label anywhere but right before mac is no line Append writes, even unchecked.
	moved := bytes.Replace(lines[1], []byte(`,"mac_key":2`), nil, 1)
	moved = bytes.Replace(moved, []byte(`{"seq":2,`), []byte(`{"seq":2,"mac_key":2,`), 1)
	writeLines(t, filepath.Join(dir, logsDirName, "2026-03-01.jsonl"), replaceAt(lines, 1, moved))
	if report := verifyWith(t, dir, nil); !report.Broken || report.Days[0].Changed != 1 {
		t.Fatalf("report without a checker = %+v, want the last line's moved label changed", report)
	}
}

func replaceAt(lines [][]byte, i int, line []byte) [][]byte {
	out := append([][]byte(nil), lines...)
	out[i] = line
	return out
}

// TestNothingIsSignedUnderTheFormerLabel holds sign to the current label, whatever it is handed.
func TestNothingIsSignedUnderTheFormerLabel(t *testing.T) {
	key, _ := testKey(t)
	raw, _ := json.Marshal(Entry{Seq: 1, PrevHash: genesisHash})
	if _, err := key.sign(raw); err == nil {
		t.Fatal("sign() signed a line that does not name the current label")
	}
	dir := t.TempDir()
	logger, _ := signedLogger(t, dir, key, time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC))
	mustAppend(t, logger, "op.one")
	for _, line := range allLines(t, dir) {
		if !bytes.Contains(line, []byte(`,"mac_key":2,"mac":"`)) {
			t.Fatalf("line %s does not name the current label", line)
		}
	}
}
