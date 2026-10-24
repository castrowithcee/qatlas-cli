package invokelog

import (
	"slices"
	"testing"
	"time"
)

func activityLine(at time.Time, op, result string) LineResult {
	return LineResult{Parsed: true, MAC: MACValid, Entry: Entry{Time: at, Path: "cli", Operation: op, Result: result}}
}

func TestDailyActivityCountsPerLocalDayAndResult(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = old })

	// 2026-03-02 02:00 UTC is still 2026-03-01 21:00 in New York, so it lies in the UTC file of the 2nd
	// and on the local 1st.
	late := time.Date(2026, 3, 2, 2, 0, 0, 0, time.UTC)
	days := []DayLines{
		{Date: "2026-03-01", Lines: []LineResult{
			activityLine(time.Date(2026, 3, 1, 15, 0, 0, 0, time.UTC), "a.b", "success"),
			activityLine(time.Date(2026, 3, 1, 16, 0, 0, 0, time.UTC), "a.b", "denied"),
			{Parsed: true, Entry: Entry{Time: time.Date(2026, 3, 1, 17, 0, 0, 0, time.UTC), Kind: KindCut}},
		}},
		{Date: "2026-03-02", Lines: []LineResult{
			activityLine(late, "a.b", "success"),
			activityLine(time.Date(2026, 3, 2, 15, 0, 0, 0, time.UTC), "a.b", "success"),
		}},
	}
	got := DailyActivity(days, "2026-02-28", "2026-03-03")
	if len(got) != 4 {
		t.Fatalf("DailyActivity() = %d days, want 4 (every day of the range)", len(got))
	}
	want := map[string][3]int{"2026-02-28": {}, "2026-03-01": {3, 2, 1}, "2026-03-02": {1, 1, 0}, "2026-03-03": {}}
	for _, d := range got {
		w := want[d.Date]
		if d.Calls != w[0] || d.Succeeded() != w[1] || d.Failed() != w[2] {
			t.Errorf("%s = calls %d ok %d failed %d, want %v", d.Date, d.Calls, d.Succeeded(), d.Failed(), w)
		}
	}
	// The same days LocalDayEntries selects.
	if entries := LocalDayEntries(days, "2026-03-01", "2026-03-01"); len(entries) != 4 {
		t.Fatalf("LocalDayEntries() = %d entries, want 4", len(entries))
	}
}

func TestDailyActivityFlagsAlteredAndGapDaysAndOpenEnds(t *testing.T) {
	at := time.Date(2026, 5, 10, 12, 0, 0, 0, time.Local)
	bad := activityLine(at, "x.y", "success")
	bad.MAC = MACInvalid
	gap := activityLine(at.AddDate(0, 0, 2), "x.y", "success")
	gap.Missing = 3
	days := []DayLines{
		{Date: "2026-05-10", Lines: []LineResult{bad}},
		{Date: "2026-05-12", Lines: []LineResult{gap, {Parsed: false}}},
	}
	got := DailyActivity(days, "", "")
	if len(got) != 3 || got[0].Date != "2026-05-10" || got[2].Date != "2026-05-12" {
		t.Fatalf("DailyActivity() = %+v, want 3 days from the first line to the last", got)
	}
	if !got[0].Altered || got[0].Gap || got[1].Altered || got[1].Gap || got[1].Calls != 0 {
		t.Errorf("flags = %+v, want altered on the 10th only and nothing on the 11th", got[:2])
	}
	if !got[2].Gap || !got[2].Altered {
		t.Errorf("12th = %+v, want gap and altered (an unreadable line belongs to its file's day)", got[2])
	}
	if DailyActivity(nil, "", "") != nil || DailyActivity(days, "2026-06-01", "2026-05-01") != nil {
		t.Errorf("no line or an inverted range must yield no day")
	}
}

func TestActivityLevelByQuartile(t *testing.T) {
	counts := []int{0, 1, 2, 3, 4, 5, 6, 7, 8}
	var days []DayActivity
	for _, c := range counts {
		days = append(days, DayActivity{Calls: c})
	}
	got := ActivityLevel(days)
	want := []int{0, 1, 1, 2, 2, 3, 3, 4, 4}
	if !slices.Equal(got, want) {
		t.Errorf("ActivityLevel() = %v, want %v", got, want)
	}
	if got := ActivityLevel([]DayActivity{{Calls: 5}, {Calls: 5}}); !slices.Equal(got, []int{1, 1}) {
		t.Errorf("equal days = %v, want all on the lowest level", got)
	}
}
