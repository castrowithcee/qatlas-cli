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
	report, _, err := verify(vaultDir, checker, nil)
	return report, err
}

// MACState is what a check found about one line's check value.
type MACState int

const (
	// MACNone is a line without a check value: written without the log key.
	MACNone MACState = iota
	// MACUnchecked is a line with a well-formed check value nobody checked, because there was no Checker.
	MACUnchecked
	// MACValid is a line whose check value matches it.
	MACValid
	// MACInvalid is a line whose check value is malformed or does not match it.
	MACInvalid
)

// ChainState is where, if anywhere, the hash chain breaks at one line.
type ChainState int

const (
	// ChainIntact is a line the chain runs through, or one only Missing explains.
	ChainIntact ChainState = iota
	// ChainUnparsed is a line that does not parse as a log entry.
	ChainUnparsed
	// ChainGenesis is a first entry with sequence number 1 that does not chain from the genesis hash.
	ChainGenesis
	// ChainNext is a line the next entry does not chain from, although it follows it by sequence number:
	// this line changed after the next one was written, or the next one's own link did.
	ChainNext
	// ChainPrevious is a line that does not chain from the line before it, where that line is not among the
	// lines returned or the sequence number itself is out of order.
	ChainPrevious
)

// LineResult is what VerifyLines found for one line of a day file.
type LineResult struct {
	// Entry is the line decoded; the zero Entry for a line that does not parse.
	Entry Entry
	// Parsed is false for a line that does not parse as a log entry; MAC then says nothing about it.
	Parsed bool
	MAC    MACState
	Chain  ChainState
	// Missing counts the sequence numbers skipped right before this line that no retention cut documents.
	Missing uint64
}

// DayLines holds the LineResult of every line of one day file, in file order.
type DayLines struct {
	Date  string
	Lines []LineResult
}

// VerifyLines walks the chain exactly as VerifyWith does, across every day file, but reports what it found
// line by line for the day files from from through to (YYYY-MM-DD, inclusive; "" leaves that end open),
// in date order. Only the check values of those days are handed to checker, so a narrow range asks the
// vault process for no more than it shows. It only reads, under the logs lock like VerifyWith, and a
// missing logs directory holds no line.
func VerifyLines(vaultDir string, checker Checker, from, to string) ([]DayLines, error) {
	inRange := func(date string) bool { return (from == "" || date >= from) && (to == "" || date <= to) }
	_, lines, err := verify(vaultDir, checker, inRange)
	return lines, err
}

// verify is the walk VerifyWith and VerifyLines share. inRange is nil for VerifyWith, which checks every
// check value and records no line; otherwise it selects the days whose check values are checked and whose
// lines are recorded, and the Report it returns counts every other day's check values as unchecked.
func verify(vaultDir string, checker Checker, inRange func(date string) bool) (Report, []DayLines, error) {
	dir := filepath.Join(vaultDir, logsDirName)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return Report{}, nil, nil
		}
		return Report{}, nil, err
	}
	days, err := readDays(dir)
	if err != nil {
		return Report{}, nil, err
	}
	record := func(date string) bool { return inRange != nil && inRange(date) }
	checks := func(date string) bool { return checker != nil && (inRange == nil || inRange(date)) }

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
			if !checks(d.date) {
				continue
			}
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
				return Report{}, nil, fmt.Errorf("cannot check the check values: %w", err)
			}
			if len(results) != len(signed) {
				return Report{}, nil, errors.New("cannot check the check values: the checker answered for the wrong number of lines")
			}
		}
	}

	var (
		report        = Report{Checked: checker != nil}
		out           []DayLines
		previousRaw   []byte
		previousSeq   uint64
		sawFirstEntry bool
		// previousLine is the recorded result of the line previousRaw is, nil when it was not recorded.
		previousLine *LineResult
	)
	for _, d := range days {
		dayReport := DayReport{Date: d.date}
		var lines []LineResult
		if record(d.date) {
			lines = make([]LineResult, 0, len(d.lines))
		}
		for i, raw := range d.lines {
			var line *LineResult
			if lines != nil {
				lines = append(lines, LineResult{})
				line = &lines[len(lines)-1]
			}
			var entry Entry
			if err := json.Unmarshal(raw, &entry); err != nil {
				dayReport.Problem = firstProblem(dayReport.Problem, "a line does not parse as a log entry")
				report.Broken = true
				if line != nil {
					line.Chain = ChainUnparsed
				}
				continue
			}
			if line != nil {
				line.Entry, line.Parsed = entry, true
			}
			if entry.Kind != KindCut {
				dayReport.Entries++
			}
			switch {
			case !sawFirstEntry && entry.Seq == 1:
				if entry.PrevHash != genesisHash {
					dayReport.Problem = firstProblem(dayReport.Problem, "the first entry does not chain from the genesis hash")
					report.Broken = true
					setChain(line, ChainGenesis)
				}
			case !sawFirstEntry && haveCut && entry.Seq == cutThroughSeq+1:
				// Documented: this is exactly where the retained window's own retention cut says it continues.
			case !sawFirstEntry:
				var missing uint64
				if haveCut && entry.Seq > cutThroughSeq+1 {
					missing = entry.Seq - cutThroughSeq - 1
				} else if !haveCut && entry.Seq > 1 {
					missing = entry.Seq - 1
				}
				dayReport.Missing += missing
				dayReport.Problem = firstProblem(dayReport.Problem,
					fmt.Sprintf("entries before sequence %d are missing without a retention cut", entry.Seq))
				report.Broken = true
				if line != nil {
					line.Missing = missing
					if missing == 0 {
						line.Chain = ChainPrevious
					}
				}
			case entry.Seq != previousSeq+1 || entry.PrevHash != hashHex(previousRaw):
				if entry.Seq > previousSeq+1 {
					dayReport.Missing += entry.Seq - previousSeq - 1
					if line != nil {
						line.Missing = entry.Seq - previousSeq - 1
					}
				} else if entry.Seq == previousSeq+1 && previousLine != nil {
					setChain(previousLine, ChainNext)
				} else {
					setChain(line, ChainPrevious)
				}
				dayReport.Problem = firstProblem(dayReport.Problem,
					fmt.Sprintf("entry with sequence %d does not chain from the previous entry", entry.Seq))
				report.Broken = true
			}
			state := countMAC(&dayReport, &report, checks(d.date), &results, entry, raw)
			if line != nil {
				line.MAC = state
			}
			sawFirstEntry = true
			previousRaw, previousSeq = raw, entry.Seq
			previousLine = nil
			if line != nil {
				// lines never grows past its capacity, so the pointer stays valid for the rest of the walk.
				previousLine = &lines[i]
			}
		}
		report.Days = append(report.Days, dayReport)
		if lines != nil {
			out = append(out, DayLines{Date: d.date, Lines: lines})
		}
	}
	return report, out, nil
}

// setChain records state on line unless line is nil or already records a break of its own.
func setChain(line *LineResult, state ChainState) {
	if line != nil && line.Chain == ChainIntact {
		line.Chain = state
	}
}

// countMAC counts the check value of one line into its day's report and returns what it found. results
// holds the checker's answers for the well-formed check values not counted yet, in order; this takes the
// next one when the line has one and checked is true.
func countMAC(day *DayReport, report *Report, checked bool, results *[]bool, entry Entry, raw []byte) MACState {
	invocation := entry.Kind != KindCut
	switch {
	case entry.MAC == "":
		if invocation {
			day.Unverified++
		}
		return MACNone
	case wellFormed(raw) && !checked:
		if invocation {
			day.Unchecked++
		}
		return MACUnchecked
	case wellFormed(raw):
		valid := (*results)[0]
		*results = (*results)[1:]
		if valid {
			return MACValid
		}
	}
	day.Changed++
	day.Problem = firstProblem(day.Problem, fmt.Sprintf("entry with sequence %d does not match its check value", entry.Seq))
	report.Broken = true
	return MACInvalid
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
