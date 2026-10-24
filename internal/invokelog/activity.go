package invokelog

import (
	"slices"
	"time"
)

// DayActivity is what the log holds for one local day: the invokes by result, and what the check found
// wrong with the day's lines.
type DayActivity struct {
	// Date is the local day, in DateLayout.
	Date string
	// Calls counts the invokes (see IsInvoke) of the day; Results counts them by their result.
	Calls   int
	Results map[string]int
	// Altered is true when a line of the day does not parse, fails its check value, or breaks the chain.
	Altered bool
	// Gap is true when sequence numbers are missing right before a line of the day that no retention cut
	// documents.
	Gap bool
}

// Succeeded counts the invokes of the day whose result is "success".
func (d DayActivity) Succeeded() int { return d.Results["success"] }

// Failed counts the invokes of the day with any other result.
func (d DayActivity) Failed() int { return d.Calls - d.Succeeded() }

// ActivityLevels is the number of levels above zero calls that ActivityLevel distinguishes.
const ActivityLevels = 4

// DailyActivity counts the invokes among the lines VerifyLines read (or any subset of them) per local day,
// for every local day from through to (DateLayout, inclusive), days without a line included; "" leaves an
// end open at the first or last day that holds a line. A line without a usable time, and a line that does
// not parse, belongs to the day of its file, which is a UTC day. Lines outside from and to are ignored.
func DailyActivity(days []DayLines, from, to string) []DayActivity {
	byDay := map[string]*DayActivity{}
	touch := func(date string) *DayActivity {
		a := byDay[date]
		if a == nil {
			a = &DayActivity{Date: date}
			byDay[date] = a
		}
		return a
	}
	for _, file := range days {
		for _, line := range file.Lines {
			date := file.Date
			if line.Parsed && !line.Entry.Time.IsZero() {
				date = line.Entry.Time.In(time.Local).Format(DateLayout)
			}
			if (from != "" && date < from) || (to != "" && date > to) {
				continue
			}
			a := touch(date)
			if !line.Parsed || line.MAC == MACInvalid || line.Chain != ChainIntact {
				a.Altered = true
			}
			if line.Missing > 0 {
				a.Gap = true
			}
			if line.Parsed && IsInvoke(line.Entry) {
				a.Calls++
				if a.Results == nil {
					a.Results = map[string]int{}
				}
				a.Results[line.Entry.Result]++
			}
		}
	}
	if len(byDay) == 0 && (from == "" || to == "") {
		return nil
	}
	if from == "" || to == "" {
		dates := make([]string, 0, len(byDay))
		for date := range byDay {
			dates = append(dates, date)
		}
		slices.Sort(dates)
		if from == "" {
			from = dates[0]
		}
		if to == "" {
			to = dates[len(dates)-1]
		}
	}
	first, err1 := time.ParseInLocation(DateLayout, from, time.Local)
	last, err2 := time.ParseInLocation(DateLayout, to, time.Local)
	if err1 != nil || err2 != nil {
		return nil
	}
	var out []DayActivity
	for day := first; !day.After(last); day = day.AddDate(0, 0, 1) {
		date := day.Format(DateLayout)
		if a := byDay[date]; a != nil {
			out = append(out, *a)
		} else {
			out = append(out, DayActivity{Date: date})
		}
	}
	return out
}

// ActivityLevel returns for every day its level: 0 for a day without calls, else 1 to ActivityLevels by
// the quartile of its call count among the days that have calls (nearest rank), so a few busy days do not
// flatten the rest.
func ActivityLevel(days []DayActivity) []int {
	var counts []int
	for _, d := range days {
		if d.Calls > 0 {
			counts = append(counts, d.Calls)
		}
	}
	slices.Sort(counts)
	var bounds [ActivityLevels - 1]int
	for i := range bounds {
		if len(counts) > 0 {
			rank := ((i+1)*25*len(counts) + 99) / 100
			bounds[i] = counts[max(rank, 1)-1]
		}
	}
	levels := make([]int, len(days))
	for i, d := range days {
		if d.Calls == 0 {
			continue
		}
		levels[i] = 1
		for _, b := range bounds {
			if d.Calls > b {
				levels[i]++
			}
		}
	}
	return levels
}
