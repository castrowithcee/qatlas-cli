package vault

import (
	"slices"
	"testing"
	"time"
)

// The fingerprint of an active connection is the one it had before connections could be disabled, so every
// approval given then stays valid; a disabled one differs.
func TestFingerprintOfAnActiveConnectionIsUnchanged(t *testing.T) {
	const before = "4e83b7a5c7201175c9e37bccbfb39049023b62d0e8d15ea98e0e2205ca0e928a"
	if got := Fingerprint(wikiScope(), "id-1"); got != before {
		t.Fatalf("Fingerprint() = %s, want the fingerprint from before the state existed", got)
	}
	off := wikiScope()
	off.Disabled = true
	if Fingerprint(off, "id-1") == before {
		t.Fatal("a disabled connection has the fingerprint of an active one")
	}
}

func TestTokenNeverCoversSwitchingOn(t *testing.T) {
	v := tokenVault(t)
	vorbild := vorbildScope(t.TempDir())
	vorbild.Paths = nil
	covered := vorbild
	covered.Connection, covered.Effects, covered.Disabled = "covered", []string{"read"}, true
	approveScopes(t, v, vorbild, covered)
	token := createToken(t, v, "agent", "vorbild")

	on := covered
	on.Disabled = false
	result, err := v.ApproveWithToken(token.Value, []Scope{vorbild, on}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changes) != 1 || result.Changes[0].Approved || !slices.Contains(result.Changes[0].Gaps, GapState) {
		t.Fatalf("switching on = %+v, want it open for %s", result.Changes, GapState)
	}
	if approvals, _ := v.Approvals(); !approvals["covered"].Scope.Disabled {
		t.Fatal("the approval of the disabled connection was replaced")
	}

	// Switching off narrows, so the vorbild covers it.
	activeApproved := covered
	activeApproved.Disabled = false
	approveScopes(t, v, activeApproved)
	result, err = v.ApproveWithToken(token.Value, []Scope{vorbild, covered}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changes) != 1 || !result.Changes[0].Approved {
		t.Fatalf("switching off = %+v, want it approved", result.Changes)
	}
}

func TestDisabledVorbildCoversNothing(t *testing.T) {
	v := tokenVault(t)
	vorbild := vorbildScope(t.TempDir())
	vorbild.Paths = nil
	covered := vorbild
	covered.Connection, covered.Effects = "covered", []string{"read"}
	approveScopes(t, v, vorbild)
	token := createToken(t, v, "agent", "vorbild")

	off := vorbild
	off.Disabled = true
	result, err := v.ApproveWithToken(token.Value, []Scope{off, covered}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(result.Lapsed, []string{"vorbild"}) {
		t.Errorf("Lapsed = %v, want the disabled vorbild", result.Lapsed)
	}
	for _, c := range result.Changes {
		if c.Connection == "covered" && c.Approved {
			t.Error("a disabled vorbild covered a change")
		}
	}
}
