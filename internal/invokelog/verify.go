package invokelog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DayReport is one day file's part of a Report.
type DayReport struct {
	Date string
	// Entries counts the invocation entries of the day; a retention-cut marker does not count as one.
	Entries int
	// Unverified counts the invocation entries of the day whose MAC is empty: written without the log key,
	// which is expected while the vault is locked without a vault process, unencrypted, or absent, and
	// never breaks the chain by itself.
	Unverified int
	// Unchecked counts the invocation entries of the day that carry a MAC nobody checked, because Verify
	// was given no Checker.
	Unchecked int
	// Changed counts the lines of the day, entries and retention-cut markers alike, whose MAC is malformed
	// or does not match the line. Each one breaks the chain.
	Changed int
	// Missing counts the sequence numbers skipped right before an entry of this day that no retention cut
	// documents: entries deleted from the middle, or a whole day file.
	Missing uint64
	// Problem names the first way this day's entries fail to chain from what precedes them, or fail their
	// check value, or "" when neither happens.
	Problem string
}

// Report is the result of Verify: one row per day file that exists, in date order.
type Report struct {
	Days []DayReport
	// Checked is true when the check values were checked, which takes a Checker.
	Checked bool
	// Broken is true when the chain is broken somewhere that is not a documented retention cut: a changed
	// or deleted entry, a day file missing from the middle, or a check value that does not match.
	Broken bool
}

// Verify is VerifyWith without a Checker: it checks the chain alone and counts every entry that carries
// a check value as unchecked.
func Verify(vaultDir string) (Report, error) { return VerifyWith(vaultDir, nil) }

// VerifyWith walks every day file under vaultDir/logs in date order and checks the hash chain: each entry's
// sequence number must follow the one before it by exactly one, and its prev_hash must be the SHA-256 of
// the exact previous line, across day boundaries. The very first entry Verify encounters at all is exempt
// from that rule only two ways: its own sequence number is 1, in which case it is checked against the
// canonical genesis hash instead, or its sequence number is exactly one more than the greatest through_seq
// of every surviving retention-cut marker, which documents that entries up to there were removed on
// purpose. Any other first entry means entries are missing without such a marker, which is reported the
// same way a day file missing from the middle of what otherwise chains together is: that case is never
// exempt. A missing logs directory is an installation that never invoked anything yet, which is an intact,
// empty chain, not an error.
//
// Every line that carries a check value is checked with checker, when there is one: a malformed check value
// or one that does not match counts as changed and breaks the chain, while an empty one only counts as
// unverified. Without a checker a well-formed check value counts as unchecked instead. A check that cannot
// be made at all fails Verify rather than passing a line unchecked. The files are read under the logs
// lock, which is released before the first check, so a slow checker never holds up a writer.
func VerifyWith(vaultDir string, checker Checker) (Report, error) {
	dir := filepath.Join(vaultDir, logsDirName)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return Report{}, nil
		}
		return Report{}, err
	}
	days, err := readDays(dir)
	if err != nil {
		return Report{}, err
	}

	// A surviving retention-cut marker may sit anywhere among the days that are still there, not only in
	// the very first one: a later cut can remove the day that held an earlier marker along with the day that
	// documented it. The greatest through_seq of every marker still around is always the one that matches
	// the sequence number of the first entry Verify can currently see, because retention only ever removes
	// the oldest contiguous run of days.
	var cutThroughSeq uint64
	haveCut := false
	for _, d := range days {
		for _, raw := range d.lines {
			var entry Entry
			if json.Unmarshal(raw, &entry) != nil || entry.Kind != KindCut || entry.Cut == nil {
				continue
			}
			if !haveCut || entry.Cut.ThroughSeq > cutThroughSeq {
				cutThroughSeq, haveCut = entry.Cut.ThroughSeq, true
			}
		}
	}

	// Every well-formed check value is checked at once, before the walk, so a checker that asks the vault
	// process does so in as few requests as it can; the walk below takes the answers in the same order.
	var results []bool
	if checker != nil {
		var signed [][]byte
		for _, d := range days {
			for _, raw := range d.lines {
				var entry Entry
				if json.Unmarshal(raw, &entry) == nil && entry.MAC != "" && wellFormed(raw) {
					signed = append(signed, raw)
				}
			}
		}
		if len(signed) > 0 {
			results, err = checker.Check(signed)
			if err != nil {
				return Report{}, fmt.Errorf("cannot check the check values: %w", err)
			}
			if len(results) != len(signed) {
				return Report{}, errors.New("cannot check the check values: the checker answered for the wrong number of lines")
			}
		}
	}

	var (
		report        = Report{Checked: checker != nil}
		previousRaw   []byte
		previousSeq   uint64
		sawFirstEntry bool
	)
	for _, d := range days {
		dayReport := DayReport{Date: d.date}
		for _, raw := range d.lines {
			var entry Entry
			if err := json.Unmarshal(raw, &entry); err != nil {
				dayReport.Problem = firstProblem(dayReport.Problem, "a line does not parse as a log entry")
				report.Broken = true
				continue
			}
			if entry.Kind != KindCut {
				dayReport.Entries++
			}
			switch {
			case !sawFirstEntry && entry.Seq == 1:
				if entry.PrevHash != genesisHash {
					dayReport.Problem = firstProblem(dayReport.Problem, "the first entry does not chain from the genesis hash")
					report.Broken = true
				}
			case !sawFirstEntry && haveCut && entry.Seq == cutThroughSeq+1:
				// Documented: this is exactly where the retained window's own retention cut says it continues.
			case !sawFirstEntry:
				if haveCut && entry.Seq > cutThroughSeq+1 {
					dayReport.Missing += entry.Seq - cutThroughSeq - 1
				} else if !haveCut && entry.Seq > 1 {
					dayReport.Missing += entry.Seq - 1
				}
				dayReport.Problem = firstProblem(dayReport.Problem,
					fmt.Sprintf("entries before sequence %d are missing without a retention cut", entry.Seq))
				report.Broken = true
			case entry.Seq != previousSeq+1 || entry.PrevHash != hashHex(previousRaw):
				if entry.Seq > previousSeq+1 {
					dayReport.Missing += entry.Seq - previousSeq - 1
				}
				dayReport.Problem = firstProblem(dayReport.Problem,
					fmt.Sprintf("entry with sequence %d does not chain from the previous entry", entry.Seq))
				report.Broken = true
			}
			countMAC(&dayReport, &report, checker != nil, &results, entry, raw)
			sawFirstEntry = true
			previousRaw, previousSeq = raw, entry.Seq
		}
		report.Days = append(report.Days, dayReport)
	}
	return report, nil
}

// countMAC counts the check value of one line into its day's report. results holds the checker's answers
// for the well-formed check values not counted yet, in order; this takes the next one when the line has one.
func countMAC(day *DayReport, report *Report, checked bool, results *[]bool, entry Entry, raw []byte) {
	invocation := entry.Kind != KindCut
	switch {
	case entry.MAC == "":
		if invocation {
			day.Unverified++
		}
		return
	case wellFormed(raw) && !checked:
		if invocation {
			day.Unchecked++
		}
		return
	case wellFormed(raw):
		valid := (*results)[0]
		*results = (*results)[1:]
		if valid {
			return
		}
	}
	day.Changed++
	day.Problem = firstProblem(day.Problem, fmt.Sprintf("entry with sequence %d does not match its check value", entry.Seq))
	report.Broken = true
}

func wellFormed(raw []byte) bool {
	_, _, err := unsignedOf(raw)
	return err == nil
}

type day struct {
	date  string
	lines [][]byte
}

// readDays reads every day file of dir under the logs lock, which it releases before it returns.
func readDays(dir string) ([]day, error) {
	unlock, err := lockDir(dir)
	if err != nil {
		return nil, err
	}
	defer unlock()

	files, err := dayFiles(dir)
	if err != nil {
		return nil, err
	}
	days := make([]day, 0, len(files))
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		days = append(days, day{date: dayFilePattern.FindStringSubmatch(name)[1], lines: splitLines(data)})
	}
	return days, nil
}

func firstProblem(existing, candidate string) string {
	if existing != "" {
		return existing
	}
	return candidate
}
