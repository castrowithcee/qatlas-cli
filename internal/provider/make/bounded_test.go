package makeapi

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBoundedKeepsRuneBoundary(t *testing.T) {
	limit := 2048
	cases := []struct {
		name  string
		in    string
		bytes int
	}{
		{"multibyte straddles limit", strings.Repeat("a", limit-1) + "é" + "x", limit - 1},
		{"exactly at limit", strings.Repeat("a", limit), limit},
		{"ascii over limit", strings.Repeat("a", limit+10), limit},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := bounded(c.in)
			if len(got) != c.bytes || !utf8.ValidString(got) || !strings.HasPrefix(c.in, got) {
				t.Fatalf("bounded() = %d bytes, valid=%v; want %d bytes", len(got), utf8.ValidString(got), c.bytes)
			}
		})
	}
}
