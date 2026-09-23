package helptopics

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The topics are plain ASCII, so a rune is a cell wherever they are shown, and each has a unique name.
func TestTopicsArePlainAndDistinct(t *testing.T) {
	names := map[string]bool{}
	for _, topic := range All() {
		if names[topic.Name] || topic.Name == "" || topic.Short == "" || topic.Text == "" {
			t.Errorf("topic %q is unnamed, repeated, or empty", topic.Name)
		}
		names[topic.Name] = true
		for i, r := range topic.Text {
			if r > '~' || (r < ' ' && r != '\n') {
				t.Errorf("topic %s has the character %q at byte %d", topic.Name, r, i)
			}
		}
	}
}

func TestWrapFitsEveryWidthAndKeepsTheText(t *testing.T) {
	for _, topic := range All() {
		for _, width := range []int{1, 2, 7, 20, 36, 40, 56, 72, 80, 200} {
			wrapped := Wrap(topic.Text, width)
			for _, line := range strings.Split(wrapped, "\n") {
				if utf8.RuneCountInString(line) > width {
					t.Errorf("%s at %d: line %q is wider", topic.Name, width, line)
				}
			}
			if width >= 36 && strings.Join(strings.Fields(wrapped), " ") != strings.Join(strings.Fields(topic.Text), " ") {
				t.Errorf("%s at %d: wrapping changed the words", topic.Name, width)
			}
		}
		if Wrap(topic.Text, 1<<20) != topic.Text {
			t.Errorf("%s: a line that fits was changed", topic.Name)
		}
	}
}

func TestWrapIndentsContinuations(t *testing.T) {
	for _, tt := range []struct {
		line  string
		width int
		want  string
	}{
		{"one two three four", 9, "one two\nthree\nfour"},
		{"1. first item text", 12, "1. first\n   item text"},
		{"  - bullet item here", 14, "  - bullet\n    item here"},
		{"  qatlas tools --query x", 16, "  qatlas tools\n  --query x"},
		{"abcdefghij", 4, "abcd\nefgh\nij"},
	} {
		if got := Wrap(tt.line, tt.width); got != tt.want {
			t.Errorf("Wrap(%q, %d) = %q, want %q", tt.line, tt.width, got, tt.want)
		}
	}
}
