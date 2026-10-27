package vault

import (
	"slices"
	"testing"
	"time"
)

func TestFingerprintCoversExclude(t *testing.T) {
	scope := Scope{
		Connection: "wiki", Credential: "wiki-reader", Provider: "bookstack", Origin: "https://wiki.example.invalid",
		Effects: []string{"read"}, Paths: []string{"/p"}, FilesRead: []string{"/r"},
	}
	// Without exclusions the fingerprint is the one written before they existed.
	scope.Exclude = []string{}
	if got := Fingerprint(scope, "id-1"); got != legacyFingerprint {
		t.Errorf("Fingerprint() with an empty exclude list = %s, want the legacy value", got)
	}
	scope.Exclude = []string{"b", "a"}
	with := Fingerprint(scope, "id-1")
	if with == legacyFingerprint {
		t.Error("Fingerprint() ignores exclusions")
	}
	scope.Exclude = []string{"a", "b"}
	if got := Fingerprint(scope, "id-1"); got != with {
		t.Error("Fingerprint() depends on the order of exclusions")
	}
	scope.Exclude = []string{"a"}
	if got := Fingerprint(scope, "id-1"); got == with {
		t.Error("Fingerprint() does not change when an exclusion is dropped")
	}
}

// An exclusion narrows the vorbild, so a change may keep or add exclusions but never drop one.
func TestTokenCoverageOfExclude(t *testing.T) {
	v := tokenVault(t)
	vorbild := vorbildScope(t.TempDir())
	vorbild.Exclude = []string{"x", "y"}
	approveScopes(t, v, vorbild)
	token := createToken(t, v, "agent", "vorbild")

	cases := []struct {
		name    string
		exclude []string
		gaps    []string
	}{
		{"kept", []string{"y", "x"}, nil},
		{"added", []string{"x", "y", "z"}, nil},
		{"one dropped", []string{"x"}, []string{GapExclude}},
		{"all dropped", nil, []string{GapExclude}},
		{"replaced", []string{"x", "z"}, []string{GapExclude}},
	}
	for _, tc := range cases {
		change := vorbild
		change.Connection = "agent-" + tc.name[:3] + tc.name[len(tc.name)-2:]
		change.Exclude = tc.exclude
		result, err := v.ApproveWithToken(token.Value, []Scope{vorbild, change}, []string{change.Connection}, time.Now())
		if err != nil {
			t.Fatalf("%s: ApproveWithToken() = %v", tc.name, err)
		}
		got := result.Changes[0]
		if got.Approved != (tc.gaps == nil) || !slices.Equal(got.Gaps, tc.gaps) {
			t.Errorf("%s: change = %+v, want gaps %v", tc.name, got, tc.gaps)
		}
	}
}
