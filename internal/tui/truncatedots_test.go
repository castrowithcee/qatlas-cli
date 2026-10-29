package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestTruncateDotsUsesEllipsisAndKeepsWidth(t *testing.T) {
	for width := 0; width <= 12; width++ {
		got := truncateDots("a long line of text", width)
		if lipgloss.Width(got) > width {
			t.Errorf("width %d: %q is %d cells", width, got, lipgloss.Width(got))
		}
		if strings.Contains(got, ".") {
			t.Errorf("width %d: %q still cuts with dots", width, got)
		}
		if width >= 1 && !strings.HasSuffix(got, "…") {
			t.Errorf("width %d: %q does not end in an ellipsis", width, got)
		}
	}
	if got := truncateDots("short", 10); got != "short" {
		t.Errorf("fitting text changed: %q", got)
	}
}
