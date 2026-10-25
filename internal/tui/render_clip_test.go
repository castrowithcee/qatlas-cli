package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestClipCellsStyledTruecolor(t *testing.T) {
	const on = "\x1b[1;38;2;214;226;238;48;2;20;51;107m"
	text := "State: " + on + "open closed" + "\x1b[m" + " tail"
	got := clipCells(text, 12)
	if w := ansi.StringWidth(got); w != 12 {
		t.Fatalf("width = %d, want 12: %q", w, got)
	}
	if !strings.Contains(got, on) || !strings.HasSuffix(got, "\x1b[m") {
		t.Fatalf("sequence cut or style left open: %q", got)
	}
	if got := clipCells(text, 3); got != "Sta" {
		t.Fatalf("got %q", got)
	}
	if full := clipCells(text, 100); full != text {
		t.Fatalf("fitting text changed: %q", full)
	}
	if got := clipCells("plain text", 5); got != "plain" || !strings.HasPrefix("plain text", got) {
		t.Fatalf("got %q", got)
	}
}
