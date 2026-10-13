package invokelog

import (
	"cmp"
	"slices"
	"time"
)

// SizeGroup is the response sizes of every invoke of one tool on one connection. An invoke without a
// resp_bytes counter (a failed invoke, or a line written before the counter existed) is counted in Calls
// and NoCounter, never as 0, and takes no part in the byte figures; Counted is what is left.
type SizeGroup struct {
	Operation  string
	Connection string
	Calls      int
	NoCounter  int
	// Bytes is the sum of the counters, Max the largest, P50 and P95 the nearest-rank percentiles.
	Bytes int64
	Max   int64
	P50   int64
	P95   int64
}

// Counted is the number of invokes whose response size is known.
func (g SizeGroup) Counted() int { return g.Calls - g.NoCounter }

// EstimateTokens approximates the tokens of a payload of n bytes at 3.5 bytes per token; no tokenizer is
// involved, so it is a rough size for the agent context, not a count.
func EstimateTokens(n int64) int64 { return n * 2 / 7 }

// IsInvoke reports whether e records an invoke, as opposed to a retention cut, a connection change, or an
// approval.
func IsInvoke(e Entry) bool {
	return e.Kind == "" && e.Operation != "" && !managementOperations[e.Operation]
}

// Summarize groups the invokes among entries by operation and connection and sums up their response sizes.
// Other entries are ignored. The groups come largest total first, then most calls, then by operation and
// connection, so the order is the same for the same entries.
func Summarize(entries []Entry) []SizeGroup {
	type key struct{ operation, connection string }
	sizes := map[key][]int64{}
	groups := map[key]*SizeGroup{}
	var order []key
	for _, e := range entries {
		if !IsInvoke(e) {
			continue
		}
		k := key{e.Operation, e.Connection}
		g := groups[k]
		if g == nil {
			g = &SizeGroup{Operation: e.Operation, Connection: e.Connection}
			groups[k] = g
			order = append(order, k)
		}
		g.Calls++
		if e.RespBytes == nil {
			g.NoCounter++
			continue
		}
		sizes[k] = append(sizes[k], *e.RespBytes)
		g.Bytes += *e.RespBytes
	}
	out := make([]SizeGroup, 0, len(order))
	for _, k := range order {
		g := groups[k]
		if s := sizes[k]; len(s) > 0 {
			slices.Sort(s)
			g.Max, g.P50, g.P95 = s[len(s)-1], nearestRank(s, 50), nearestRank(s, 95)
		}
		out = append(out, *g)
	}
	slices.SortFunc(out, func(a, b SizeGroup) int {
		return cmp.Or(cmp.Compare(b.Bytes, a.Bytes), cmp.Compare(b.Calls, a.Calls),
			cmp.Compare(a.Operation, b.Operation), cmp.Compare(a.Connection, b.Connection))
	})
	return out
}

// nearestRank is the nearest-rank percentile p of the sorted, non-empty values.
func nearestRank(sorted []int64, p int) int64 {
	rank := (p*len(sorted) + 99) / 100
	return sorted[max(rank, 1)-1]
}

// DateLayout is how a day is written, for the day files and for a range of local days.
const DateLayout = "2006-01-02"

// UTCFileBounds selects every UTC day file that can overlap the local days from through to (DateLayout,
// inclusive; "" leaves that end open).
func UTCFileBounds(from, to string) (string, string) {
	if from != "" {
		day, _ := time.ParseInLocation(DateLayout, from, time.Local)
		from = day.UTC().Format(DateLayout)
	}
	if to != "" {
		day, _ := time.ParseInLocation(DateLayout, to, time.Local)
		to = day.AddDate(0, 0, 1).Add(-time.Nanosecond).UTC().Format(DateLayout)
	}
	return from, to
}

// InLocalDays reports whether t falls on a local day from through to (DateLayout, inclusive; "" leaves
// that end open).
func InLocalDays(t time.Time, from, to string) bool {
	day := t.In(time.Local).Format(DateLayout)
	return (from == "" || day >= from) && (to == "" || day <= to)
}

// LocalDayEntries returns the entries of the lines VerifyLines read that fall on the local days from
// through to; the UTC files it was given for those days overlap them, so it drops the rest. A line that
// does not parse holds no entry.
func LocalDayEntries(days []DayLines, from, to string) []Entry {
	var entries []Entry
	for _, day := range days {
		for _, line := range day.Lines {
			if line.Parsed && (line.Entry.Time.IsZero() || InLocalDays(line.Entry.Time, from, to)) {
				entries = append(entries, line.Entry)
			}
		}
	}
	return entries
}
