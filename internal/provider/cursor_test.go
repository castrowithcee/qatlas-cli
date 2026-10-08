package provider_test

import (
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestBoundCursorRoundTripsAndRefusesOtherBindings(t *testing.T) {
	binding := provider.CursorBinding("records", "person", "name")
	cursor := provider.EncodeCursor(binding, "abc123")
	if inner, ok := provider.DecodeCursor(binding, cursor, 100); !ok || inner != "abc123" {
		t.Fatalf("DecodeCursor() = %q, %v", inner, ok)
	}
	other := provider.CursorBinding("records", "company", "name")
	for name, c := range map[string]string{"other binding": cursor, "empty": "", "garbage": "!!", "short": "AAAA"} {
		used := binding
		if name == "other binding" {
			used = other
		}
		if _, ok := provider.DecodeCursor(used, c, 100); ok {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, ok := provider.DecodeCursor(binding, cursor, 5); ok {
		t.Error("an overlong cursor was accepted")
	}
}
