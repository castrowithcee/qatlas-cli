package invokelog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The self-exec pattern below runs one real Append from a freshly started process, so the concurrent-writer
// test in this file exercises the file lock across processes, not only across goroutines of one.
const (
	workerModeEnv = "QATLAS_INVOKELOG_TEST_WORKER"
	workerDirEnv  = "QATLAS_INVOKELOG_TEST_DIR"
)

func TestMain(m *testing.M) {
	if os.Getenv(workerModeEnv) == "1" {
		logger := New(os.Getenv(workerDirEnv), 90)
		if err := logger.Append(Fields{Path: "cli", Operation: "worker.append", Result: "success"}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// clockAt returns a logger whose clock is fixed at t until moveTo advances it.
func clockAt(t time.Time) (*Logger, func(time.Time)) {
	current := t.UTC()
	logger := &Logger{now: func() time.Time { return current }}
	return logger, func(next time.Time) { current = next.UTC() }
}

func mustAppend(t *testing.T, logger *Logger, operation string) {
	t.Helper()
	if err := logger.Append(Fields{Path: "cli", Operation: operation, Effect: "read", Result: "success",
		Duration: 5 * time.Millisecond}); err != nil {
		t.Fatalf("Append(%q) error = %v", operation, err)
	}
}

func TestAppendChainsAcrossDays(t *testing.T) {
	dir := t.TempDir()
	logger, moveTo := clockAt(time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC))
	logger.vaultDir = dir
	logger.retentionDays = 90

	mustAppend(t, logger, "op.one")
	mustAppend(t, logger, "op.two")
	moveTo(time.Date(2026, 1, 3, 9, 0, 0, 0, time.UTC)) // a day with no invoke in between
	mustAppend(t, logger, "op.three")

	if _, err := os.Stat(filepath.Join(logger.logsDir(), "2026-01-02.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("a day with no invoke must not get a file, stat error = %v", err)
	}

	report, err := Verify(dir)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if report.Broken {
		t.Fatalf("Verify() report = %+v, want an intact chain", report)
	}
	if len(report.Days) != 2 {
		t.Fatalf("len(report.Days) = %d, want 2", len(report.Days))
	}
	if report.Days[0].Entries != 2 || report.Days[1].Entries != 1 {
		t.Fatalf("report.Days = %+v, want 2 then 1 entries", report.Days)
	}
	for _, day := range report.Days {
		if day.Unverified != day.Entries {
			t.Fatalf("day %s: Unverified = %d, want %d (every entry is unverified until it is signed)",
				day.Date, day.Unverified, day.Entries)
		}
	}
}

func readLines(t *testing.T, path string) [][]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return splitLines(data)
}

func writeLines(t *testing.T, path string, lines [][]byte) {
	t.Helper()
	var buf bytes.Buffer
	for _, line := range lines {
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(path, buf.Bytes(), fileMode); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func TestVerifyDetectsTamperedLine(t *testing.T) {
	dir := t.TempDir()
	logger, _ := clockAt(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	logger.vaultDir = dir
	logger.retentionDays = 90
	mustAppend(t, logger, "op.one")
	mustAppend(t, logger, "op.two")
	mustAppend(t, logger, "op.three")

	path := filepath.Join(logger.logsDir(), "2026-02-01.jsonl")
	lines := readLines(t, path)
	lines[1] = bytes.Replace(lines[1], []byte(`"op.two"`), []byte(`"op.tampered"`), 1)
	writeLines(t, path, lines)

	report, err := Verify(dir)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !report.Broken {
		t.Fatalf("Verify() report = %+v, want a tampered line to break the chain", report)
	}
}

func TestVerifyDetectsDeletedLine(t *testing.T) {
	dir := t.TempDir()
	logger, _ := clockAt(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	logger.vaultDir = dir
	logger.retentionDays = 90
	mustAppend(t, logger, "op.one")
	mustAppend(t, logger, "op.two")
	mustAppend(t, logger, "op.three")

	path := filepath.Join(logger.logsDir(), "2026-02-01.jsonl")
	lines := readLines(t, path)
	writeLines(t, path, append(lines[:1], lines[2:]...))

	report, err := Verify(dir)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !report.Broken {
		t.Fatalf("Verify() report = %+v, want a deleted line to break the chain", report)
	}
}

func TestVerifyDetectsMissingMiddleDay(t *testing.T) {
	dir := t.TempDir()
	logger, moveTo := clockAt(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	logger.vaultDir = dir
	logger.retentionDays = 90
	mustAppend(t, logger, "day1")
	moveTo(time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC))
	mustAppend(t, logger, "day2")
	moveTo(time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC))
	mustAppend(t, logger, "day3")

	if err := os.Remove(filepath.Join(logger.logsDir(), "2026-03-02.jsonl")); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	report, err := Verify(dir)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !report.Broken {
		t.Fatalf("Verify() report = %+v, want a missing middle day file to break the chain", report)
	}
}

func TestRetentionCutIsNotReportedAsAGap(t *testing.T) {
	dir := t.TempDir()
	logger, moveTo := clockAt(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	logger.vaultDir = dir
	logger.retentionDays = 5

	mustAppend(t, logger, "old.one")
	moveTo(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	mustAppend(t, logger, "old.two")
	// Far enough forward that logger.retentionDays expires 2026-01-01 and 2026-01-02 on the next append.
	moveTo(time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC))
	mustAppend(t, logger, "fresh.one")
	mustAppend(t, logger, "fresh.two")

	if _, err := os.Stat(filepath.Join(logger.logsDir(), "2026-01-01.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("2026-01-01.jsonl should have been removed by retention, stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(logger.logsDir(), "2026-01-02.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("2026-01-02.jsonl should have been removed by retention, stat error = %v", err)
	}

	freshLines := readLines(t, filepath.Join(logger.logsDir(), "2026-01-20.jsonl"))
	if len(freshLines) != 3 {
		t.Fatalf("2026-01-20.jsonl has %d lines, want 3 (a cut marker plus two invoke entries)", len(freshLines))
	}
	if !bytes.Contains(freshLines[0], []byte(`"kind":"retention-cut"`)) {
		t.Fatalf("first surviving line of the cut day = %s, want a retention-cut marker", freshLines[0])
	}

	report, err := Verify(dir)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if report.Broken {
		t.Fatalf("Verify() report = %+v, want a documented retention cut, not a break", report)
	}
	if len(report.Days) != 1 || report.Days[0].Entries != 2 {
		t.Fatalf("report.Days = %+v, want one day with 2 invoke entries (the marker does not count)", report.Days)
	}
}

// TestVerifyDetectsSeqOneWithWrongGenesisHash covers the other exemption Verify grants a first entry: seq 1
// is only ever accepted when it chains from the canonical genesis hash, never unconditionally.
func TestVerifyDetectsSeqOneWithWrongGenesisHash(t *testing.T) {
	dir := t.TempDir()
	logger, _ := clockAt(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	logger.vaultDir = dir
	logger.retentionDays = 90
	mustAppend(t, logger, "op.one")
	mustAppend(t, logger, "op.two")

	path := filepath.Join(logger.logsDir(), "2026-04-01.jsonl")
	lines := readLines(t, path)
	var first map[string]any
	if err := json.Unmarshal(lines[0], &first); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if first["seq"] != float64(1) {
		t.Fatalf("first entry seq = %v, want 1", first["seq"])
	}
	first["prev_hash"] = strings.Repeat("f", 64)
	tampered, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	lines[0] = tampered
	writeLines(t, path, lines)

	report, err := Verify(dir)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !report.Broken {
		t.Fatalf("Verify() report = %+v, want seq 1 with the wrong genesis hash to break the chain", report)
	}
}

// TestVerifyDetectsOldestDayRemovedWithoutRetention is the bug this correction fixes: deleting the oldest
// day file by hand, without going through Append's own retention step, must not read as an intact chain
// just because the entry that follows happens to be the first one Verify can now see.
func TestVerifyDetectsOldestDayRemovedWithoutRetention(t *testing.T) {
	dir := t.TempDir()
	logger, moveTo := clockAt(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	logger.vaultDir = dir
	logger.retentionDays = 90
	mustAppend(t, logger, "day1.one")
	moveTo(time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC))
	mustAppend(t, logger, "day2.one")
	mustAppend(t, logger, "day2.two")

	if err := os.Remove(filepath.Join(logger.logsDir(), "2026-05-01.jsonl")); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	report, err := Verify(dir)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !report.Broken {
		t.Fatalf("Verify() report = %+v, want a hand-deleted oldest day without a retention cut to break the chain",
			report)
	}
	if len(report.Days) != 1 || report.Days[0].Problem == "" {
		t.Fatalf("report.Days = %+v, want the surviving day to name the problem", report.Days)
	}
}

// TestRetentionCutTwiceStaysIntact covers two retention cuts across several days: the second cut removes
// every day that still exists, the one holding the first cut's own marker included, so the only way its own
// marker can still match the retained window's start is if the sequence counter kept counting through both
// cuts instead of restarting once nothing survived to read a last entry from.
func TestRetentionCutTwiceStaysIntact(t *testing.T) {
	dir := t.TempDir()
	logger, moveTo := clockAt(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	logger.vaultDir = dir
	logger.retentionDays = 5

	mustAppend(t, logger, "day1.one") // seq 1, day 2026-01-01
	moveTo(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	mustAppend(t, logger, "day2.one") // seq 2

	// First cut: both 2026-01-01 and 2026-01-02 expire at once, removing every file that exists so far; its
	// marker and the surviving entry land on 2026-01-20.
	moveTo(time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC))
	mustAppend(t, logger, "day20.one") // cut marker (seq 3, through_seq 2) + entry seq 4

	// Second cut, far enough forward that 2026-01-20 expires too, again removing every file that exists,
	// the day that held the first cut's own marker included.
	moveTo(time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC))
	mustAppend(t, logger, "day-feb.one") // cut marker (seq 5, through_seq 4) + entry seq 6

	for _, name := range []string{"2026-01-01.jsonl", "2026-01-02.jsonl", "2026-01-20.jsonl"} {
		if _, err := os.Stat(filepath.Join(logger.logsDir(), name)); !os.IsNotExist(err) {
			t.Fatalf("%s should have been removed by a retention cut, stat error = %v", name, err)
		}
	}

	lines := readLines(t, filepath.Join(logger.logsDir(), "2026-02-15.jsonl"))
	if len(lines) != 2 {
		t.Fatalf("2026-02-15.jsonl has %d lines, want 2 (a cut marker plus the invoke entry)", len(lines))
	}
	var marker map[string]any
	if err := json.Unmarshal(lines[0], &marker); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	// The sequence counter must keep counting through a cut that removes everything, not restart at 1 as if
	// this were a fresh installation: that would hide, rather than document, the four entries it replaced.
	if marker["seq"] != float64(5) {
		t.Fatalf("second cut marker seq = %v, want 5 (the counter must not restart after removing everything)",
			marker["seq"])
	}

	report, err := Verify(dir)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if report.Broken {
		t.Fatalf("Verify() report = %+v, want two retention cuts across several days to stay intact", report)
	}
}

// TestVerifyDetectsGapAfterADocumentedCut covers a cut followed by a further, undocumented deletion: once a
// day that survived a retention cut is itself removed by hand, the surviving marker's through_seq no longer
// matches the next surviving entry's sequence number, and that must break the chain.
func TestVerifyDetectsGapAfterADocumentedCut(t *testing.T) {
	dir := t.TempDir()
	logger, moveTo := clockAt(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	logger.vaultDir = dir
	logger.retentionDays = 5

	mustAppend(t, logger, "day1.one") // seq 1, 2026-01-01
	moveTo(time.Date(2026, 1, 8, 0, 0, 0, 0, time.UTC))
	mustAppend(t, logger, "day2.one") // seq 2, 2026-01-08: 7 days after day1, still inside the window below
	moveTo(time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC))
	// Cutoff is 2026-01-05: only 2026-01-01 (seq 1) expires here, 2026-01-08 (seq 2) does not, so this cut's
	// marker (through_seq 1) chains from seq 2, not from the entry it documents removing.
	mustAppend(t, logger, "day3.one") // cut marker (through_seq 1, seq 3) + seq 4, both on 2026-01-10

	if _, err := os.Stat(filepath.Join(logger.logsDir(), "2026-01-01.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("2026-01-01.jsonl should have been removed by the first retention cut, stat error = %v", err)
	}

	// Removing 2026-01-08 (seq 2) by hand, outside of Append's own retention step, leaves the surviving
	// marker's through_seq (1) one behind where the chain would need to continue (3) for it to still match.
	if err := os.Remove(filepath.Join(logger.logsDir(), "2026-01-08.jsonl")); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	report, err := Verify(dir)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !report.Broken {
		t.Fatalf("Verify() report = %+v, want the gap after the documented cut to break the chain", report)
	}
}

func TestAppendConcurrentGoroutines(t *testing.T) {
	dir := t.TempDir()
	logger := New(dir, 90)

	const n = 40
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := logger.Append(Fields{Path: "cli", Operation: fmt.Sprintf("op.%d", i), Result: "success"}); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("Append() error = %v", err)
	}

	report, err := Verify(dir)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if report.Broken {
		t.Fatalf("Verify() report broken with concurrent goroutine writers: %+v", report)
	}
	total := 0
	for _, day := range report.Days {
		total += day.Entries
	}
	if total != n {
		t.Fatalf("total entries = %d, want %d", total, n)
	}
}

func TestAppendConcurrentProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns subprocesses")
	}
	dir := t.TempDir()

	const n = 6
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0])
			cmd.Env = append(os.Environ(), workerModeEnv+"=1", workerDirEnv+"="+dir)
			if out, err := cmd.CombinedOutput(); err != nil {
				errs <- fmt.Errorf("worker process failed: %w: %s", err, out)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	report, err := Verify(dir)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if report.Broken {
		t.Fatalf("Verify() report broken with concurrent process writers: %+v", report)
	}
	total := 0
	for _, day := range report.Days {
		total += day.Entries
	}
	if total != n {
		t.Fatalf("total entries = %d, want %d", total, n)
	}
}

func TestAppendNeverWritesArgumentsOrSecrets(t *testing.T) {
	dir := t.TempDir()
	logger := New(dir, 90)
	const canary = "canary-secret-value-should-never-appear"
	// Fields has no argument or result field at all, so there is no way to pass the canary through it; this
	// test documents that guarantee and catches a future field added the wrong way.
	if err := logger.Append(Fields{
		Path: "cli", Operation: "github.search_issues", Version: 1, Connection: "personal",
		Effect: "read", Result: "success", Duration: time.Millisecond,
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	files, err := dayFiles(logger.logsDir())
	if err != nil || len(files) != 1 {
		t.Fatalf("dayFiles() = %v, %v, want exactly one file", files, err)
	}
	data, err := os.ReadFile(filepath.Join(logger.logsDir(), files[0]))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(data), canary) {
		t.Fatalf("log file contains the canary: %s", data)
	}
}

func TestAppendFailureIsReportedNotPanicked(t *testing.T) {
	dir := t.TempDir()
	// A file in place of the logs directory makes MkdirAll fail, which is the shape of a real
	// "unwritable directory" failure without needing root or a read-only filesystem in the test.
	if err := os.WriteFile(filepath.Join(dir, logsDirName), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	logger := New(dir, 90)
	if err := logger.Append(Fields{Path: "cli", Operation: "op", Result: "success"}); err == nil {
		t.Fatal("Append() error = nil, want an error because the logs directory could not be created")
	}
}

func TestValidateConnectionChangeEntries(t *testing.T) {
	good := func(path, operation, effect string) Fields {
		return Fields{Path: path, Operation: operation, Connection: "wiki", Effect: effect, Result: "success"}
	}
	for _, path := range []string{"tui", "web", "cli"} {
		for operation, effect := range connectionOperations {
			if err := good(path, operation, effect).Validate(); err != nil {
				t.Errorf("%s %s: Validate() = %v", path, operation, err)
			}
		}
	}
	bad := map[string]Fields{
		"unknown path":              {Path: "ssh", Operation: OperationConnectionChange, Connection: "wiki", Effect: "update", Result: "success"},
		"tui with an invoke":        {Path: "tui", Operation: "bookstack.read", Effect: "read", Result: "success"},
		"web without operation":     {Path: "web", Result: "success"},
		"wrong effect":              good("tui", OperationConnectionDelete, "update"),
		"no connection":             {Path: "web", Operation: OperationConnectionCreate, Effect: "create", Result: "success"},
		"control character in name": {Path: "tui", Operation: OperationConnectionCreate, Connection: "a\nb", Effect: "create", Result: "success"},
		"overlong name":             {Path: "tui", Operation: OperationConnectionCreate, Connection: strings.Repeat("x", 300), Effect: "create", Result: "success"},
		"invalid utf-8 in name":     {Path: "tui", Operation: OperationConnectionCreate, Connection: "\xff", Effect: "create", Result: "success"},
		"result is not a code":      {Path: "tui", Operation: OperationConnectionCreate, Connection: "wiki", Effect: "create", Result: "Bad Result"},
	}
	for name, f := range bad {
		if err := f.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil, want an error", name)
		}
	}
}

func TestSignedConnectionEntriesVerify(t *testing.T) {
	dir := t.TempDir()
	key, _ := testKey(t)
	logger, _ := signedLogger(t, dir, key, time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC))
	for _, f := range []Fields{
		{Path: "cli", Operation: "bookstack.read", Effect: "read", Result: "success"},
		{Path: "tui", Operation: OperationConnectionCreate, Connection: "wiki", Effect: "create", Result: "success"},
		{Path: "web", Operation: OperationConnectionChange, Connection: "wiki", Effect: "update", Result: "success"},
	} {
		if err := f.Validate(); err != nil {
			t.Fatal(err)
		}
		if err := logger.Append(f); err != nil {
			t.Fatal(err)
		}
	}
	report := verifyWith(t, dir, key)
	if report.Broken || !report.Checked {
		t.Fatalf("report = %+v, want an intact, checked chain", report)
	}
}
