package idempotency

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"
)

func TestResolveGeneratesAndKeepsProvided(t *testing.T) {
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	a, generated, err := Resolve("", nil)
	b, _, _ := Resolve("", nil)
	if err != nil || !generated || !uuid.MatchString(a) || a == b {
		t.Fatalf("generated %q %q (%v, %v)", a, b, generated, err)
	}
	// A fixed source gives a fixed token: the form comes from the random bytes only.
	got, _, _ := Resolve("", bytes.NewReader(make([]byte, 16)))
	if got != "00000000-0000-4000-8000-000000000000" {
		t.Errorf("token = %s", got)
	}
	got, generated, err = Resolve("caller-token-1", nil)
	if got != "caller-token-1" || generated || err != nil {
		t.Errorf("provided = %q %v %v", got, generated, err)
	}
}

func TestResolveRefusesBadInput(t *testing.T) {
	for _, bad := range []string{"has space", "tab\t", "é", strings.Repeat("x", 129)} {
		if _, _, err := Resolve(bad, nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("Resolve(%q) = %v", bad, err)
		}
	}
	if _, _, err := Resolve("", bytes.NewReader([]byte{1, 2})); err == nil {
		t.Error("a short random source must fail, not fall back")
	}
}

func TestIsParam(t *testing.T) {
	for _, name := range []string{"ClientToken", "ClientRequestToken", "clientToken"} {
		if !IsParam(name) {
			t.Errorf("%s is a token member", name)
		}
	}
	if IsParam("Token") {
		t.Error("Token is not")
	}
}
