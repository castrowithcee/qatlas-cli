package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
)

// The square heatmap: the same days and levels as heatGrid, drawn as two-cell squares in the heat colours of
// the theme, with the date of each week above its column. Without colour (NO_COLOR, a terminal that shows none,
// or a locale that cannot draw squares) it falls back to the glyphs of heatGlyphSet, so a level never rests on
// its colour alone.

const (
	// heatSquareLabel is the width of the weekday label; heatSquareCol the cells of one week column.
	heatSquareLabel = 4
	heatSquareCol   = 3
)

// heatColoured reports whether the squares can carry their level as a colour.
func heatColoured() bool {
	return styles.palette.heat[0] != "" && lipgloss.ColorProfile() != termenv.Ascii && !heatASCII()
}

// heatSquare is the two-cell mark of a level: a square in its heat colour, or the glyph of the level.
func heatSquare(level int) string {
	if !heatColoured() {
		g := heatGlyphSet()
		glyph := g.zero
		if level > 0 {
			glyph = g.levels[level-1]
		}
		return strings.Repeat(glyph, 2)
	}
	return lipgloss.NewStyle().Foreground(styles.palette.heat[level]).Render("■■")
}

// heatSquaresWidth is the width in cells of a grid of weeks columns.
func heatSquaresWidth(weeks int) int { return heatSquareLabel + heatSquareCol*weeks - 1 }

// heatSquares draws days as seven weekday rows under a header of dates, one column of heatSquareCol cells per
// week, at most as wide as width. Like heatGrid it keeps the latest weeks that fit and returns the days it
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
	g := heatGlyphSet()
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
		if d.Date > today {
			continue
		}
		if d.Altered || d.Gap || (d.Calls == 0 && d.Date < retentionStart) {
			glyph, style := g.cell(d, levels[i], retentionStart)
			rows[row][col] = style.Render(strings.Repeat(glyph, 2))
			continue
		}
		rows[row][col] = heatSquare(levels[i])
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
