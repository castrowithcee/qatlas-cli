package invokelog

import (
	"reflect"
	"testing"
)

func sized(op, conn string, resp *int64) Entry {
	return Entry{Operation: op, Connection: conn, RespBytes: resp}
}

func ptr(n int64) *int64 { return &n }

func TestSummarizeNearestRankPercentiles(t *testing.T) {
	var entries []Entry
	// 1..20 shuffled by insertion order: p50 = rank 10, p95 = rank 19.
	for _, n := range []int64{20, 1, 19, 2, 18, 3, 17, 4, 16, 5, 15, 6, 14, 7, 13, 8, 12, 9, 11, 10} {
		entries = append(entries, sized("a.op", "c1", ptr(n*100)))
	}
	got := Summarize(entries)
	want := []SizeGroup{{Operation: "a.op", Connection: "c1", Calls: 20, Bytes: 21000, Max: 2000, P50: 1000, P95: 1900}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Summarize() = %+v, want %+v", got, want)
	}
}

func TestSummarizeEdgeCases(t *testing.T) {
	one := Summarize([]Entry{sized("op", "c", ptr(70))})
	if want := (SizeGroup{Operation: "op", Connection: "c", Calls: 1, Bytes: 70, Max: 70, P50: 70, P95: 70}); one[0] != want {
		t.Errorf("one value = %+v, want %+v", one[0], want)
	}
	same := Summarize([]Entry{sized("op", "c", ptr(5)), sized("op", "c", ptr(5)), sized("op", "c", ptr(5))})
	if want := (SizeGroup{Operation: "op", Connection: "c", Calls: 3, Bytes: 15, Max: 5, P50: 5, P95: 5}); same[0] != want {
		t.Errorf("equal values = %+v, want %+v", same[0], want)
	}
	two := Summarize([]Entry{sized("op", "c", ptr(10)), sized("op", "c", ptr(30))})
	if two[0].P50 != 10 || two[0].P95 != 30 {
		t.Errorf("two values: p50 %d p95 %d, want 10 and 30", two[0].P50, two[0].P95)
	}
}

func TestSummarizeCountsMissingCountersApartAndZeroAsAValue(t *testing.T) {
	got := Summarize([]Entry{
		sized("op", "c", ptr(0)),
		sized("op", "c", nil),
		sized("op", "c", ptr(100)),
		sized("only.old", "c", nil),
	})
	want := []SizeGroup{
		{Operation: "op", Connection: "c", Calls: 3, NoCounter: 1, Bytes: 100, Max: 100, P50: 0, P95: 100},
		{Operation: "only.old", Connection: "c", Calls: 1, NoCounter: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Summarize() = %+v, want %+v", got, want)
	}
	if got[0].Counted() != 2 || got[1].Counted() != 0 {
		t.Errorf("Counted() = %d and %d, want 2 and 0", got[0].Counted(), got[1].Counted())
	}
}

func TestSummarizeIgnoresWhatIsNoInvoke(t *testing.T) {
	got := Summarize([]Entry{
		{Kind: KindCut, Cut: &Cut{ThroughSeq: 3}},
		{Operation: OperationConnectionCreate, Connection: "c", Path: "tui"},
		{Operation: OperationApprove, Connection: "c"},
		{Operation: OperationApprove},
		{Operation: OperationLock, Path: "cli", Result: "program-replaced"},
		{Operation: OperationHandover, Path: "cli", Result: "success"},
		{},
		sized("op", "c", ptr(7)),
	})
	if len(got) != 1 || got[0].Operation != "op" || got[0].Calls != 1 {
		t.Fatalf("Summarize() = %+v, want only op", got)
	}
	if Summarize(nil) == nil {
		t.Error("Summarize(nil) = nil, want an empty slice")
	}
}

func TestSummarizeOrderIsStable(t *testing.T) {
	entries := []Entry{
		sized("b", "x", ptr(10)), sized("a", "y", ptr(10)), sized("a", "x", ptr(10)),
		sized("big", "x", ptr(500)), sized("many", "x", ptr(5)), sized("many", "x", ptr(5)),
	}
	var names []string
	for _, g := range Summarize(entries) {
		names = append(names, g.Operation+"@"+g.Connection)
	}
	want := []string{"big@x", "many@x", "a@x", "a@y", "b@x"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("order = %v, want %v", names, want)
	}
}

func TestEstimateTokens(t *testing.T) {
	if EstimateTokens(3500) != 1000 || EstimateTokens(0) != 0 {
		t.Errorf("EstimateTokens = %d and %d", EstimateTokens(3500), EstimateTokens(0))
	}
}
