package invokelog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DayReport is one day file's part of a Report.
type DayReport struct {
	Date string
	// Entries counts the invocation entries of the day; a retention-cut marker does not count as one.
	Entries int
	// Unverified counts the entries of the day whose MAC is still empty. In this build that is every one
	// of them, which is expected and never breaks the chain by itself.
	Unverified int
	// Problem names the first way this day's entries fail to chain from what precedes them, or "" when
	// they chain correctly.
	Problem string
}

// Report is the result of Verify: one row per day file that exists, in date order.
type Report struct {
	Days []DayReport
	// Broken is true when the chain is broken somewhere that is not a documented retention cut: a changed
	// or deleted entry, or a day file missing from the middle.
	Broken bool
}

// Verify walks every day file under vaultDir/logs in date order and checks the hash chain: each entry's
// sequence number must follow the one before it by exactly one, and its prev_hash must be the SHA-256 of
// the exact previous line, across day boundaries. The very first entry Verify encounters at all is exempt
// from that rule only two ways: its own sequence number is 1, in which case it is checked against the
// canonical genesis hash instead, or its sequence number is exactly one more than the greatest through_seq
// of every surviving retention-cut marker, which documents that entries up to there were removed on
// purpose. Any other first entry means entries are missing without such a marker, which is reported the
// same way a day file missing from the middle of what otherwise chains together is: that case is never
// exempt. A missing logs directory is an installation that never invoked anything yet, which is an intact,
// empty chain, not an error.
func Verify(vaultDir string) (Report, error) {
	dir := filepath.Join(vaultDir, logsDirName)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return Report{}, nil
		}
		return Report{}, err
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return Report{}, err
	}
	defer unlock()

	files, err := dayFiles(dir)
	if err != nil {
		return Report{}, err
	}
	type day struct {
		date  string
		lines [][]byte
	}
	days := make([]day, 0, len(files))
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return Report{}, fmt.Errorf("read %s: %w", name, err)
		}
		days = append(days, day{date: dayFilePattern.FindStringSubmatch(name)[1], lines: splitLines(data)})
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

	var (
		report        Report
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
				if entry.MAC == "" {
					dayReport.Unverified++
				}
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
				dayReport.Problem = firstProblem(dayReport.Problem,
					fmt.Sprintf("entries before sequence %d are missing without a retention cut", entry.Seq))
				report.Broken = true
			case entry.Seq != previousSeq+1 || entry.PrevHash != hashHex(previousRaw):
				dayReport.Problem = firstProblem(dayReport.Problem,
					fmt.Sprintf("entry with sequence %d does not chain from the previous entry", entry.Seq))
				report.Broken = true
			}
			sawFirstEntry = true
			previousRaw, previousSeq = raw, entry.Seq
		}
		report.Days = append(report.Days, dayReport)
	}
	return report, nil
}

func firstProblem(existing, candidate string) string {
	if existing != "" {
		return existing
	}
	return candidate
}
