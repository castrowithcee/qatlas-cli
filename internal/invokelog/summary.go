package invokelog

import (
	"cmp"
	"slices"
	"time"
)

// SizeGroup is the response sizes of every invoke of one tool on one connection. An invoke without a
// resp_bytes counter (a failed CLI invoke, or a line written before the counter existed) is counted in Calls
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

// IsInvoke reports whether e records an invoke, as opposed to a retention cut, a connection change, an
// approval, or an mcp baseline or navigation exchange.
func IsInvoke(e Entry) bool {
	_, reserved := mcpOperations[e.Operation]
	return e.Kind == "" && e.Operation != "" && !managementOperations[e.Operation] && !reserved
}

// Area is the part of an MCP session's context cost an entry belongs to.
type Area string

// The areas, in the order a session meets them.
const (
	AreaBaseline   Area = "baseline"
	AreaNavigation Area = "navigation"
	AreaInvoke     Area = "invoke"
)

var areaOrder = []Area{AreaBaseline, AreaNavigation, AreaInvoke}

// ToolBytes is what one operation of one area cost: the number of exchanges and the bytes of their
// JSON-RPC requests and responses, error responses included. A counter an entry lacks adds 0.
type ToolBytes struct {
	Operation string
	Calls     int
	ReqBytes  int64
	RespBytes int64
}

// Total is the request and response bytes together.
func (t ToolBytes) Total() int64 { return t.ReqBytes + t.RespBytes }

// AreaSummary is the sum of an area's operations, which come largest total first, then most calls, then by
// operation.
type AreaSummary struct {
	Area Area
	ToolBytes
	Tools []ToolBytes
}

// SessionSummary is what one MCP session cost, with every area that has an entry in the fixed order of
// AreaBaseline, AreaNavigation, AreaInvoke.
type SessionSummary struct {
	Session string
	Areas   []AreaSummary
}

// SummarizeSessions groups the mcp entries that carry a session by session, area, and operation. Sessions
// come in the order of their first entry. Entries without a session, from the CLI or from before sessions
// existed, are left out; Summarize still counts their invokes.
func SummarizeSessions(entries []Entry) []SessionSummary {
	type tools = map[string]*ToolBytes
	sessions := map[string]map[Area]tools{}
	var order []string
	for _, e := range entries {
		area, ok := areaOf(e)
		if !ok || e.Session == "" {
			continue
		}
		byArea := sessions[e.Session]
		if byArea == nil {
			byArea = map[Area]tools{}
			sessions[e.Session] = byArea
			order = append(order, e.Session)
		}
		if byArea[area] == nil {
			byArea[area] = tools{}
		}
		t := byArea[area][e.Operation]
		if t == nil {
			t = &ToolBytes{Operation: e.Operation}
			byArea[area][e.Operation] = t
		}
		t.Calls++
		if e.ReqBytes != nil {
			t.ReqBytes += *e.ReqBytes
		}
		if e.RespBytes != nil {
			t.RespBytes += *e.RespBytes
		}
	}
	out := make([]SessionSummary, 0, len(order))
	for _, id := range order {
		summary := SessionSummary{Session: id}
		for _, area := range areaOrder {
			byTool := sessions[id][area]
			if len(byTool) == 0 {
				continue
			}
			as := AreaSummary{Area: area, Tools: make([]ToolBytes, 0, len(byTool))}
			for _, t := range byTool {
				as.Tools = append(as.Tools, *t)
				as.Calls += t.Calls
				as.ReqBytes += t.ReqBytes
				as.RespBytes += t.RespBytes
			}
			slices.SortFunc(as.Tools, func(a, b ToolBytes) int {
				return cmp.Or(cmp.Compare(b.Total(), a.Total()), cmp.Compare(b.Calls, a.Calls),
					cmp.Compare(a.Operation, b.Operation))
			})
			summary.Areas = append(summary.Areas, as)
		}
		out = append(out, summary)
	}
	return out
}

// areaOf returns the area of an mcp entry: baseline or navigation by its reserved operation, invoke for any
// other invoke on the mcp path.
func areaOf(e Entry) (Area, bool) {
	if area, ok := mcpOperations[e.Operation]; ok {
		return area, e.Kind == "" && e.Path == "mcp"
	}
	return AreaInvoke, e.Path == "mcp" && IsInvoke(e)
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
