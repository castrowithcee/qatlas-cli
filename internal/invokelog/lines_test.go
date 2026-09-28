package invokelog

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"
)

// countingChecker counts the lines it is asked about and answers with the key it wraps.
type countingChecker struct {
	key   *Key
	lines int
}

func (c *countingChecker) Check(lines [][]byte) ([]bool, error) {
	c.lines += len(lines)
	return c.key.Check(lines)
}

// threeSignedDays writes op.a and op.b on 2026-03-01, op.c, op.d and op.e on 2026-03-02, and op.f on
// 2026-03-03, all signed with key, and returns the logs directory.
func threeSignedDays(t *testing.T, dir string, key *Key) string {
	t.Helper()
	logger, moveTo := signedLogger(t, dir, key, time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC))
	mustAppend(t, logger, "op.a")
	mustAppend(t, logger, "op.b")
	moveTo(time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC))
	mustAppend(t, logger, "op.c")
	mustAppend(t, logger, "op.d")
	mustAppend(t, logger, "op.e")
	moveTo(time.Date(2026, 3, 3, 8, 0, 0, 0, time.UTC))
	mustAppend(t, logger, "op.f")
	return logger.logsDir()
}

func verifyLines(t *testing.T, dir string, checker Checker, from, to string) []DayLines {
	t.Helper()
	days, err := VerifyLines(dir, checker, from, to)
	if err != nil {
		t.Fatalf("VerifyLines() error = %v", err)
	}
	return days
}

func TestVerifyLinesReportsEveryLineOfTheRange(t *testing.T) {
	dir := t.TempDir()
	key, _ := testKey(t)
	threeSignedDays(t, dir, key)

	checker := &countingChecker{key: key}
	days := verifyLines(t, dir, checker, "2026-03-02", "2026-03-02")
	if len(days) != 1 || days[0].Date != "2026-03-02" || len(days[0].Lines) != 3 {
		t.Fatalf("VerifyLines() = %+v, want the three lines of 2026-03-02 alone", days)
	}
	if checker.lines != 3 {
		t.Errorf("the checker was asked about %d lines, want only the 3 of the range", checker.lines)
	}
	for i, line := range days[0].Lines {
		if !line.Parsed || line.MAC != MACValid || line.Chain != ChainIntact || line.Missing != 0 {
			t.Errorf("line %d = %+v, want an intact, valid line", i, line)
		}
	}
	if got := days[0].Lines[1].Entry.Operation; got != "op.d" {
		t.Errorf("line 1 operation = %q, want op.d", got)
	}

	if all := verifyLines(t, dir, nil, "", ""); len(all) != 3 {
		t.Errorf("an open range returned %d days, want 3", len(all))
	} else if all[0].Lines[0].MAC != MACUnchecked {
		t.Errorf("without a checker a signed line is %v, want MACUnchecked", all[0].Lines[0].MAC)
	}
	if none := verifyLines(t, t.TempDir(), key, "", ""); none != nil {
		t.Errorf("a missing logs directory returned %+v, want nothing", none)
	}
}

// A changed line fails its own check value, and the next entry, which no longer chains from it, marks it
// rather than itself: the change is in the earlier line.
func TestVerifyLinesMarksAChangedLine(t *testing.T) {
	dir := t.TempDir()
	key, _ := testKey(t)
	logs := threeSignedDays(t, dir, key)
	path := filepath.Join(logs, "2026-03-02.jsonl")
	lines := readLines(t, path)
	lines[1] = bytes.Replace(lines[1], []byte(`"op.d"`), []byte(`"op.x"`), 1)
	writeLines(t, path, lines)

	day := verifyLines(t, dir, key, "2026-03-02", "2026-03-02")[0]
	if got := day.Lines[1]; got.MAC != MACInvalid || got.Chain != ChainNext {
		t.Errorf("changed line = %+v, want MACInvalid and ChainNext", got)
	}
	for _, i := range []int{0, 2} {
		if got := day.Lines[i]; got.MAC != MACValid || got.Chain != ChainIntact {
			t.Errorf("line %d = %+v, want it intact", i, got)
		}
	}
	// VerifyWith still reports the same break as before.
	if report := verifyWith(t, dir, key); !report.Broken || report.Days[1].Changed != 1 {
		t.Errorf("VerifyWith() = %+v, want the day broken with one changed line", report)
	}
}

// A change to the last line of a day outside the range is reported on the first line of the range, which is
// the first line that no longer chains.
func TestVerifyLinesMarksABreakBeforeTheRange(t *testing.T) {
	dir := t.TempDir()
	key, _ := testKey(t)
	logs := threeSignedDays(t, dir, key)
	path := filepath.Join(logs, "2026-03-01.jsonl")
	lines := readLines(t, path)
	lines[1] = bytes.Replace(lines[1], []byte(`"op.b"`), []byte(`"op.x"`), 1)
	writeLines(t, path, lines)

	day := verifyLines(t, dir, nil, "2026-03-02", "2026-03-02")[0]
	if got := day.Lines[0]; got.Chain != ChainPrevious || got.MAC != MACUnchecked {
		t.Errorf("first line of the range = %+v, want ChainPrevious and MACUnchecked", got)
	}
}

func TestVerifyLinesMarksAGapAndAnUnparsedLine(t *testing.T) {
	dir := t.TempDir()
	key, _ := testKey(t)
	logs := threeSignedDays(t, dir, key)
	path := filepath.Join(logs, "2026-03-02.jsonl")
	lines := readLines(t, path)
	writeLines(t, path, [][]byte{lines[1], []byte("not a log entry")})

	day := verifyLines(t, dir, key, "2026-03-02", "2026-03-02")[0]
	if len(day.Lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(day.Lines))
	}
	if got := day.Lines[0]; got.Missing != 1 || got.Chain != ChainIntact || got.MAC != MACValid {
		t.Errorf("line after the deleted one = %+v, want Missing 1 and otherwise intact", got)
	}
	if got := day.Lines[1]; got.Parsed || got.Chain != ChainUnparsed {
		t.Errorf("unparsed line = %+v, want Parsed false and ChainUnparsed", got)
	}
}
