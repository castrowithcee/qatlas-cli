package cli

import (
	"testing"
	"time"
)

func TestShorterLimit(t *testing.T) {
	h := time.Hour
	for _, c := range []struct{ a, b, want time.Duration }{
		{0, 0, 0}, {h, 0, h}, {0, h, h}, {h, 2 * h, h}, {2 * h, h, h}, {-h, 0, 0}, {-h, h, h},
	} {
		if got := shorterLimit(c.a, c.b); got != c.want {
			t.Errorf("shorterLimit(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
