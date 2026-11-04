//go:build linux || darwin || windows

package main

import (
	"bytes"
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/approval"
	"github.com/castrowithcee/qatlas-connect/internal/capability"
	qconfig "github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/invokelog"
	"github.com/castrowithcee/qatlas-connect/internal/provider/bookstack"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
	"github.com/castrowithcee/qatlas-connect/internal/vault"
)

// TestSignedInvokeLogEndToEnd drives the built binary's signed invocation log: it continues two days the
// former release signed under its log key label; while the vault is locked an invoke is logged unverified;
// once 'vault unlock' at a pseudo-terminal started the vault process, invokes over the CLI and an MCP broker
// started before the unlock are logged signed by that process under the current label, and 'vault logs
// verify' has it check the former and the current entries alike. A line changed by hand, the last one, is
// reported changed, and so is a line signed under the former label after the current one. 'vault lock'
// ends the process, after which verify checks the chain alone and reports the check values as not checked.
// Neither a secret nor any key material ever reaches an output or the log.
func TestSignedInvokeLogEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("the acceptance run builds the binary")
	}

	const (
		storedID     = "canary-log-id-5c02aa"
		storedSecret = "canary-log-secret-e41f97"
		passphrase   = "canary-log-passphrase-7d3b10"
	)

	dir := t.TempDir()
	bin := buildBinary(t, dir)
	server := mock(t, "Token "+storedID+":"+storedSecret, []map[string]any{page(1, "Signed Runbook")}, "<p>x</p>")

	configPath := filepath.Join(dir, "config.yaml")
	config := fmt.Sprintf(`version: 4
services:
  wiki:
    provider: bookstack
    base_url: %s
credentials:
  vault-reader:
    type: vault
connections:
  wiki:
    service: wiki
    credential: vault-reader
defaults:
  connections:
    bookstack: wiki
`, server.URL)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}
	v := vault.New(dir)
	offer := func(string) (string, error) { return passphrase, nil }
	if err := v.Set("vault-reader", "token-id", storedID, offer); err != nil {
		t.Fatalf("seed token-id: %v", err)
	}
	if err := v.Set("vault-reader", "token-secret", storedSecret, offer); err != nil {
		t.Fatalf("seed token-secret: %v", err)
	}
	snap, err := v.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	vaultKey := snap.Identity
	reg := capability.NewRegistry()
	if err := bookstack.Register(reg); err != nil {
		t.Fatal(err)
	}
	cfg, err := qconfig.Load(configPath, reg)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	if approved, _, err := approval.Approve(context.Background(), cfg, v, nil); err != nil || len(approved) != 1 {
		t.Fatalf("approving the connection: %v, %v", approved, err)
	}

	runtimeDir := shortRuntimeDir(t)

	var seen strings.Builder
	c := &runner{bin: bin, seen: &seen, env: []string{
		"HOME=" + dir,
		"PATH=" + os.Getenv("PATH"),
		"QCONNECT_CONFIG=" + configPath,
		"XDG_RUNTIME_DIR=" + runtimeDir,
		secret.StoreSelector + "=none",
	}}
	var processID int
	t.Cleanup(func() {
		_, _, _ = c.run(t, "vault", "lock")
		if processID != 0 {
			killProcess(processID)
		}
	})

	broker := startBroker(t, c)

	logsDir := filepath.Join(dir, "vault", "logs")
	now := time.Now().UTC()
	former := seedFormerLog(t, logsDir, vaultKey, now.AddDate(0, 0, -2), now.AddDate(0, 0, -1))
	readLog := func(t *testing.T) (string, []map[string]any) {
		t.Helper()
		all, _ := filepath.Glob(filepath.Join(logsDir, "*.jsonl"))
		var files []string
		for _, file := range all {
			if _, seeded := former[file]; !seeded {
				files = append(files, file)
			}
		}
		if len(files) != 1 {
			t.Fatalf("day files = %v, want exactly one beside the former release's", files)
		}
		data, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		var entries []map[string]any
		for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			var entry map[string]any
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatalf("log line %q: %v", line, err)
			}
			entries = append(entries, entry)
		}
		return files[0], entries
	}
	type verifyDay struct {
		Entries, Unverified, Unchecked, Changed int
		Problem                                 string
	}
	verify := func(t *testing.T) (int, string, []verifyDay, string) {
		t.Helper()
		code, stdout, stderr := c.run(t, "vault", "logs", "verify", "--output", "json")
		var doc struct {
			Broken   bool        `json:"broken"`
			MACCheck string      `json:"mac_check"`
			Days     []verifyDay `json:"days"`
		}
		if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
			t.Fatalf("verify: exit %d, stdout %q, stderr %q: %v", code, stdout, stderr, err)
		}
		return code, doc.MACCheck, doc.Days, stderr
	}

	if code, _, stderr := c.run(t, "invoke", "bookstack.pages.list"); code != 1 || !strings.Contains(stderr, "vault-locked") ||
		strings.Contains(stderr, "warning") {
		t.Fatalf("invoke while locked: exit %d, stderr %q", code, stderr)
	}

	out, code := runAtTerminal(t, c, passphrase+"\n", "vault", "unlock")
	match := regexp.MustCompile(`unlocked in a vault process \(pid (\d+)\)`).FindStringSubmatch(out)
	if code != 0 || match == nil {
		t.Fatalf("unlock: exit %d, output %q", code, out)
	}
	processID, _ = strconv.Atoi(match[1])

	if code, stdout, stderr := c.run(t, "invoke", "bookstack.pages.list"); code != 0 || !strings.Contains(stdout, "Signed Runbook") ||
		stderr != "" {
		t.Fatalf("invoke pages.list: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if code, _, stderr := c.runInput(t, `{"id":1}`, "invoke", "bookstack.pages.get"); code != 0 || stderr != "" {
		t.Fatalf("invoke pages.get: exit %d, stderr %q", code, stderr)
	}
	if invoke := broker.call(t, `{"operation":"bookstack.pages.list","connection":"wiki"}`, "qconnect.invoke"); invoke.Result.IsError {
		t.Fatalf("MCP invoke = %s", invoke.Result.Structured)
	}

	// An entry is appended after its response was written, so it may follow the answer by a moment. The search
	// waits for the invoke entry, since signing it takes longer than the search and the order is the append order.
	awaitEntries := func(t *testing.T, want int) (string, []map[string]any) {
		t.Helper()
		var file string
		var entries []map[string]any
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			if file, entries = readLog(t); len(entries) >= want {
				return file, entries
			}
			if time.Now().After(deadline) {
				t.Fatalf("the log holds %d entries, want %d: %v", len(entries), want, entries)
			}
		}
	}
	awaitEntries(t, 4)

	if search := broker.call(t, `{"query":"pages"}`, "qconnect.search"); search.Result.IsError {
		t.Fatalf("MCP search = %s", search.Result.Structured)
	}
	file, entries := awaitEntries(t, 5)
	if len(entries) != 5 {
		t.Fatalf("the log holds %d entries, want 5: %v", len(entries), entries)
	}
	for i, entry := range entries {
		signed := entry["mac"] != ""
		if signed != (i > 0) || signed != (entry["mac_key"] == 2.0) {
			t.Fatalf("entry %d = %v, want only the entry written while locked unsigned, the rest under the current label",
				i, entry)
		}
	}
	for file, data := range former {
		if now, _ := os.ReadFile(file); !bytes.Equal(now, data) {
			t.Fatalf("the former release's day file %s was rewritten", filepath.Base(file))
		}
	}
	if entries[3]["path"] != "mcp" || entries[4]["path"] != "mcp" || entries[4]["operation"] != "qconnect.search" ||
		entries[3]["session"] == nil || entries[3]["session"] != entries[4]["session"] || entries[4]["connection"] != nil {
		t.Fatalf("last entries = %v, %v, want the broker's invoke and search under one session", entries[3], entries[4])
	}

	code, macCheck, days, stderr := verify(t)
	if code != 0 || macCheck != "checked" || len(days) != 3 || stderr != "" {
		t.Fatalf("verify: exit %d, mac_check %q, days %+v, stderr %q", code, macCheck, days, stderr)
	}
	for _, day := range days[:2] {
		if day != (verifyDay{Entries: 2}) {
			t.Fatalf("verify of the former release's day: %+v, want 2 entries, every check value valid", day)
		}
	}
	days = days[2:]
	if days[0].Entries != 5 || days[0].Unverified != 1 || days[0].Unchecked != 0 || days[0].Changed != 0 {
		t.Fatalf("verify: days %+v", days)
	}

	// The last line by hand: its chain stays intact, since nothing follows it, but its check value fails.
	original, _ := os.ReadFile(file)
	at := strings.LastIndex(string(original), `"path":"mcp"`)
	if at < 0 {
		t.Fatal("the broker's entry was not found to change")
	}
	tampered := string(original[:at]) + `"path":"cli"` + string(original[at+len(`"path":"mcp"`):])
	if err := os.WriteFile(file, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, days, stderr = verify(t)
	if code == 0 || len(days) != 3 || days[2].Changed != 1 ||
		!strings.Contains(days[2].Problem, "does not match its check value") || !strings.Contains(stderr, "broken") {
		t.Fatalf("verify of a changed line: exit %d, days %+v, stderr %q", code, days, stderr)
	}

	// A line under the former label after the current one, chained and signed the way the former release
	// would have, is a change too.
	lines := bytes.Split(bytes.TrimRight(original, "\n"), []byte("\n"))
	var last invokelog.Entry
	if err := json.Unmarshal(lines[len(lines)-1], &last); err != nil {
		t.Fatal(err)
	}
	downgraded := append(append([]byte(nil), original...), formerLine(t, vaultKey, invokelog.Entry{Seq: last.Seq + 1,
		Time: last.Time, Path: "cli", Operation: "bookstack.pages.list", Effect: "read", Result: "success",
		PrevHash: hashOf(lines[len(lines)-1])})...)
	if err := os.WriteFile(file, append(downgraded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, days, stderr = verify(t)
	if code == 0 || len(days) != 3 || days[2].Changed != 1 || !strings.Contains(days[2].Problem, "former log key") ||
		!strings.Contains(stderr, "broken") {
		t.Fatalf("verify of a line under the former label: exit %d, days %+v, stderr %q", code, days, stderr)
	}
	if err := os.WriteFile(file, original, 0o600); err != nil {
		t.Fatal(err)
	}

	if code, stdout, stderr := c.run(t, "vault", "lock"); code != 0 || !strings.Contains(stdout, "the vault is locked") {
		t.Fatalf("lock: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	for deadline := time.Now().Add(5 * time.Second); processAlive(processID); {
		if time.Now().After(deadline) {
			t.Fatalf("the vault process %d did not end when it was locked", processID)
		}
		time.Sleep(20 * time.Millisecond)
	}
	processID = 0

	code, macCheck, days, stderr = verify(t)
	if code != 0 || macCheck != "vault-locked" || len(days) != 3 || days[0].Unchecked != 2 || days[1].Unchecked != 2 ||
		days[2].Unchecked != 4 || days[2].Unverified != 1 || stderr != "" {
		t.Fatalf("verify after lock: exit %d, mac_check %q, days %+v, stderr %q", code, macCheck, days, stderr)
	}

	broker.stop(t)
	logData, _ := os.ReadFile(file)
	for _, data := range former {
		logData = append(logData, data...)
	}
	for _, canary := range []string{storedID, storedSecret, passphrase, vaultKey, strings.ToLower(vaultKey), "AGE-SECRET-KEY"} {
		if strings.Contains(seen.String(), canary) || strings.Contains(string(logData), canary) {
			t.Errorf("secret or key material reached the output or the log")
		}
	}
}

// formerLabel is the context the former release derived its log key with.
const formerLabel = "qatlas invocation log v1"

// seedFormerLog writes two signed entries on each of days, chained from the genesis hash and signed under
// formerLabel with the vault key identity, the way the former release wrote them, and returns each file's
// contents by path.
func seedFormerLog(t *testing.T, logsDir, identity string, days ...time.Time) map[string][]byte {
	t.Helper()
	if err := os.MkdirAll(logsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	prev, seq := hashOf(nil), uint64(1)
	for _, day := range days {
		path := filepath.Join(logsDir, day.Format("2006-01-02")+".jsonl")
		var data []byte
		for _, op := range []string{"bookstack.pages.list", "bookstack.pages.get"} {
			line := formerLine(t, identity, invokelog.Entry{Seq: seq, Time: day.Truncate(time.Second), Path: "cli",
				Operation: op, Connection: "wiki", Effect: "read", Result: "success", PrevHash: prev})
			data = append(append(data, line...), '\n')
			prev, seq = hashOf(line), seq+1
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		files[path] = data
	}
	return files
}

// formerLine is entry as the former release stored it: without mac_key, with the HMAC-SHA256 under the log
// key HKDF derives from identity with formerLabel as its check value.
func formerLine(t *testing.T, identity string, entry invokelog.Entry) []byte {
	t.Helper()
	unsigned, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	key, err := hkdf.Key(sha256.New, []byte(identity), nil, formerLabel, sha256.Size)
	if err != nil {
		t.Fatal(err)
	}
	h := hmac.New(sha256.New, key)
	h.Write(unsigned)
	line := bytes.TrimSuffix(unsigned, []byte(`""}`))
	return append(line, `"`+hex.EncodeToString(h.Sum(nil))+`"}`...)
}

func hashOf(line []byte) string {
	sum := sha256.Sum256(line)
	return hex.EncodeToString(sum[:])
}
