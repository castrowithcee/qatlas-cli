package vault

import (
	"slices"
	"testing"
	"time"
)

var (
	limitNow   = time.Date(2030, 1, 2, 9, 0, 0, 0, time.UTC)
	limitLater = "2030-01-02T18:00:00Z"
	limitSoon  = "2030-01-02T12:00:00Z"
)

// A connection without a limit keeps the fingerprint it had before limits existed; one with a limit differs,
// and a different limit differs again.
func TestFingerprintOfAnUnlimitedConnectionIsUnchanged(t *testing.T) {
	const before = "4e83b7a5c7201175c9e37bccbfb39049023b62d0e8d15ea98e0e2205ca0e928a"
	if got := Fingerprint(wikiScope(), "id-1"); got != before {
		t.Fatalf("Fingerprint() = %s, want the fingerprint from before limits existed", got)
	}
	soon, later := wikiScope(), wikiScope()
	soon.ActiveUntil, later.ActiveUntil = limitSoon, limitLater
	if Fingerprint(soon, "id-1") == before || Fingerprint(soon, "id-1") == Fingerprint(later, "id-1") {
		t.Fatal("the limit is not part of the fingerprint")
	}
}

func TestTokenNeverCoversSettingOrLengtheningALimit(t *testing.T) {
	for name, tc := range map[string]struct {
		approved, now string
		covered       bool
	}{
		"set":        {approved: "", now: limitSoon},
		"lengthen":   {approved: limitSoon, now: limitLater},
		"lift":       {approved: limitSoon, now: ""},
		"shorten":    {approved: limitLater, now: limitSoon, covered: true},
		"unreadable": {approved: limitSoon, now: "soon"},
	} {
		v := tokenVault(t)
		vorbild := vorbildScope(t.TempDir())
		vorbild.Paths = nil
		child := vorbild
		child.Connection, child.Effects, child.ActiveUntil = "child", []string{"read"}, tc.approved
		approveScopes(t, v, vorbild, child)
		token := createToken(t, v, "agent", "vorbild")

		next := child
		next.ActiveUntil = tc.now
		result, err := v.ApproveWithToken(token.Value, []Scope{vorbild, next}, nil, limitNow)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Changes) != 1 || result.Changes[0].Approved != tc.covered ||
			slices.Contains(result.Changes[0].Gaps, GapActiveUntil) == tc.covered {
			t.Errorf("%s: changes = %+v, covered want %v", name, result.Changes, tc.covered)
		}
	}
}

func TestExpiredVorbildCoversNothing(t *testing.T) {
	for _, limit := range []string{"approved", "configured"} {
		v := tokenVault(t)
		vorbild := vorbildScope(t.TempDir())
		vorbild.Paths = nil
		covered := vorbild
		covered.Connection, covered.Effects = "covered", []string{"read"}
		past := "2030-01-02T08:00:00Z"
		approved := vorbild
		if limit == "approved" {
			approved.ActiveUntil = past
		}
		approveScopes(t, v, approved)
		token := createToken(t, v, "agent", "vorbild")

		current := vorbild
		current.ActiveUntil = past
		result, err := v.ApproveWithToken(token.Value, []Scope{current, covered}, nil, limitNow)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(result.Lapsed, []string{"vorbild"}) {
			t.Errorf("%s: Lapsed = %v, want the expired vorbild", limit, result.Lapsed)
		}
		for _, c := range result.Changes {
			if c.Connection == "covered" && c.Approved {
				t.Errorf("%s: an expired vorbild covered a change", limit)
			}
		}
	}
}
