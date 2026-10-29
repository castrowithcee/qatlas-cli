package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
)

// The square heatmap: days drawn as squares in the heat colours of the theme, as columns of weeks (the Overview)
// or as a row per week (the Activity screen). Without colour (NO_COLOR, a terminal that shows none,
// or a locale that cannot draw squares) it falls back to the glyphs of heatGlyphSet, so a level never rests on
// its colour alone.

const (
	// heatSquareLabel is the width of the weekday label; heatSquareCol the cells of one week column.
	heatSquareLabel = 4
	heatSquareCol   = 3
)

// heatColoured reports whether the squares can carry their level as a colour. With 16 colours there are too
// few greens to tell four levels apart, so the shade glyphs carry them.
func heatColoured() bool {
	return styles.palette.heat[0] != "" && !noColour() && currentProfile() != profile16 && utf8Terminal()
}

// heatSquare is the two-cell mark of a level: a square in its heat colour, or the glyph of the level.
func heatSquare(level int) string { return heatBlock(level, 2) }

// heatBlock is the mark of a level n cells wide.
func heatBlock(level, n int) string {
	if !heatColoured() {
		g := heatGlyphSet()
		glyph := g.zero
		if level > 0 {
			glyph = g.levels[level-1]
		}
		mark := strings.Repeat(glyph, n)
		switch {
		case styles.palette.heat[0] == "":
			return mark
		case level > 0:
			return styles.ok.Render(mark)
		}
		return styles.hint.Render(mark)
	}
	return lipgloss.NewStyle().Foreground(styles.palette.heat[level].lip()).Render(strings.Repeat("■", n))
}

// heatCell is the n-cell mark of one day: a day the check flagged or one outside the retention shows that
// before its level, a day after today has none.
func heatCell(d invokelog.DayActivity, level int, today, retentionStart string, n int) string {
	switch {
	case d.Date > today:
		return strings.Repeat(" ", n)
	case d.Altered || d.Gap || (d.Calls == 0 && d.Date < retentionStart):
		glyph, style := heatGlyphSet().cell(d, level, retentionStart)
		return style.Render(strings.Repeat(glyph, n))
	}
	return heatBlock(level, n)
}

// heatSquaresWidth is the width in cells of a grid of weeks columns.
func heatSquaresWidth(weeks int) int { return heatSquareLabel + heatSquareCol*weeks - 1 }

// heatSquares draws days as seven weekday rows under a header of dates, one column of heatSquareCol cells per
// week, at most as wide as width. It keeps the latest weeks that fit and returns the days it
// drew; a day after today has no square.
func heatSquares(days []invokelog.DayActivity, today, retentionStart string, width, maxWeeks int) ([]string, []invokelog.DayActivity) {
	if len(days) == 0 {
		return nil, nil
	}
	weeks := min(maxWeeks, (width-heatSquareLabel+1)/heatSquareCol)
	if weeks < 1 {
		return nil, nil
	}
	days, offset, total := heatWeeks(days, weeks)
	levels := invokelog.ActivityLevel(days)
	weekdays, monthNames := heatWeekdayNames(), heatMonthNames()
	rows := make([][]string, 7)
	for r := range rows {
		rows[r] = make([]string, total)
		for c := range rows[r] {
			rows[r][c] = "  "
		}
	}
	for i, d := range days {
		col, row := (offset+i)/7, (offset+i)%7
		if d.Date <= today {
			rows[row][col] = heatCell(d, levels[i], today, retentionStart, 2)
		}
	}
	first, _ := time.ParseInLocation(invokelog.DateLayout, days[0].Date, time.Local)
	monday := first.AddDate(0, 0, -offset)
	month := ""
	header := make([]string, total)
	for c := range header {
		header[c] = fmt.Sprintf("%2d", monday.AddDate(0, 0, 7*c).Day())
	}
	month = monthNames[monday.Month()-1]
	lines := []string{styles.hint.Render(padLine(month, heatSquareLabel) + strings.Join(header, " "))}
	for r := range rows {
		lines = append(lines, styles.hint.Render(padLine(weekdays[r], heatSquareLabel))+strings.Join(rows[r], " "))
	}
	return lines, days
}

// heatSquareLegend is the line that explains the squares: from the emptiest to the strongest level.
func heatSquareLegend() string {
	var marks []string
	for level := 0; level <= invokelog.ActivityLevels; level++ {
		marks = append(marks, heatSquare(level))
	}
	return styles.hint.Render(tr(tOverviewLess)) + strings.Join(marks, " ") + styles.hint.Render(tr(tOverviewMore))
}

const (
	// heatWeekLabel is the width of the date that leads a week row.
	heatWeekLabel = 7
	// heatWideCell and heatNarrowCell are the cells of one day in a week row, a blank between two days not counted.
	heatWideCell   = 4
	heatNarrowCell = 2
)

// heatWeekCell is the width of a day in a week row for width cells: the wide one where it fits.
func heatWeekCell(width int) int {
	if width >= heatWeekLabel+7*(heatWideCell+1)-1 {
		return heatWideCell
	}
	return heatNarrowCell
}

// heatWeekRows draws days as one row per week, oldest first, under a header of the weekdays: Monday to Sunday
// as columns, each row led by the date of its Monday. Like heatSquares it keeps the latest weeks (at most
// maxWeeks) and returns the days it drew; a day after today has no mark.
func heatWeekRows(days []invokelog.DayActivity, today, retentionStart string, width, maxWeeks int) ([]string, []invokelog.DayActivity) {
	cell := heatWeekCell(width)
	if len(days) == 0 || maxWeeks < 1 || width < heatWeekLabel+7*(cell+1)-1 {
		return nil, nil
	}
	days, offset, total := heatWeeks(days, maxWeeks)
	levels := invokelog.ActivityLevel(days)
	marks := make([][]string, total)
	for w := range marks {
		marks[w] = make([]string, 7)
		for d := range marks[w] {
			marks[w][d] = strings.Repeat(" ", cell)
		}
	}
	for i, d := range days {
		marks[(offset+i)/7][(offset+i)%7] = heatCell(d, levels[i], today, retentionStart, cell)
	}
	first, _ := time.ParseInLocation(invokelog.DateLayout, days[0].Date, time.Local)
	monday := first.AddDate(0, 0, -offset)
	weekdays, monthNames := heatWeekdayNames(), heatMonthNames()
	names := make([]string, 7)
	for d := range names {
		names[d] = padLine(truncateCells(weekdays[d], cell), cell)
	}
	lines := []string{styles.hint.Render(padLine("", heatWeekLabel) + strings.Join(names, " "))}
	for w := range marks {
		day := monday.AddDate(0, 0, 7*w)
		label := fmt.Sprintf("%s %02d", monthNames[day.Month()-1], day.Day())
		lines = append(lines, styles.hint.Render(padLine(label, heatWeekLabel))+strings.Join(marks[w], " "))
	}
	return lines, days
}
