package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The option bar is a row of fields, each a label and a few fixed alternatives (see arch "Optionsleisten").
// The Activity screen and the filter bar of the lists draw their fields with the helpers below.

// barMarker is what stands in front of a bar field: "> " while the field has the focus.
func barMarker(focused bool) string {
	if focused {
		return "> "
	}
	return "  "
}

// barChips draws the alternatives of one field, the active one in brackets and highlighted, so the
// brackets and not the colour say which one it is.
func barChips(values []string, active int) string {
	chips := make([]string, len(values))
	for i, v := range values {
		if i == active {
			chips[i] = styles.activeRow.Render("[" + v + "]")
			continue
		}
		chips[i] = v
	}
	return strings.Join(chips, " ")
}

// packBar sets the fields and the status of a bar into lines of width cells: on one line where they fit,
// otherwise field by field, the status ending the last line.
func packBar(fields []string, status string, width int) []string {
	w := lipgloss.Width
	n := len(fields)
	all := strings.Join(fields, "  ")
	if w(all)+2+w(status) <= width {
		return []string{all + strings.Repeat(" ", width-w(all)-w(status)) + status}
	}
	if n >= 3 {
		first, rest := fields[0], strings.Join(fields[1:], "  ")
		if w(first) >= w(rest) && w(rest)+2+w(status) <= width {
			return []string{first, rest + "  " + status}
		}
	}
	if w(fields[n-1])+2+w(status) <= width {
		return append(append([]string{}, fields[:n-1]...), fields[n-1]+"  "+status)
	}
	return append(append([]string{}, fields...), "  "+status)
}
