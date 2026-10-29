package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoColourLiteralOutsidePalette keeps every colour value inside the palette: only color.go builds a
// lipgloss colour, from a palette entry.
func TestNoColourLiteralOutsidePalette(t *testing.T) {
	allowed := map[string]bool{"color.go": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || allowed[f] {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "lipgloss.Color(") {
			t.Errorf("%s builds a colour outside the palette", f)
		}
	}
}
