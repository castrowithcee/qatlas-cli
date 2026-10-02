package vault

import (
	"errors"
	"slices"
	"testing"
	"time"
)

// legacyFingerprint is the fingerprint a scope without forward credentials had before they existed: the hash
// of the canonical form written then, which the current code must still produce byte for byte.
const legacyFingerprint = "65981c6b64ee5acbedc6266f5cb6a9f88584f9e134a4325e8449e216dd51e491"

func forwardScope() Scope {
	s := wikiScope()
	s.Forward = []ForwardSecret{{Name: "shared", Fields: []string{"user", "pass"}}}
	return s
}

func TestFingerprintWithoutForwardIsUnchanged(t *testing.T) {
	scope := Scope{
		Connection: "wiki", Credential: "wiki-reader", Provider: "bookstack", Origin: "https://wiki.example.invalid",
		Permissions: []string{"read"}, Paths: []string{"/p"}, FilesRead: []string{"/r"},
	}
	if got := Fingerprint(scope, "id-1"); got != legacyFingerprint {
		t.Errorf("Fingerprint() without forward = %s, want the stored legacy value %s", got, legacyFingerprint)
	}
	scope.Forward = []ForwardSecret{}
	if got := Fingerprint(scope, "id-1"); got != legacyFingerprint {
		t.Errorf("Fingerprint() with an empty forward list = %s, want the legacy value", got)
	}
}

func TestFingerprintCoversForward(t *testing.T) {
	base := Fingerprint(forwardScope(), "id-1")
	if base == Fingerprint(wikiScope(), "id-1") {
		t.Fatalf("a released forward credential left the fingerprint as it was")
	}
	reordered := forwardScope()
	reordered.Forward = []ForwardSecret{{Name: "shared", Fields: []string{"pass", "user"}}}
	if Fingerprint(reordered, "id-1") != base {
		t.Errorf("reordering the fields changed the fingerprint")
	}
	changes := map[string]func(*Scope){
		"another credential": func(s *Scope) { s.Forward[0].Name = "other" },
		"fewer fields":       func(s *Scope) { s.Forward[0].Fields = []string{"user"} },
		"more fields":        func(s *Scope) { s.Forward[0].Fields = []string{"user", "pass", "key"} },
		"a second credential": func(s *Scope) {
			s.Forward = append(s.Forward, ForwardSecret{Name: "second", Fields: []string{"x"}})
		},
	}
	for name, change := range changes {
		s := forwardScope()
		change(&s)
		if Fingerprint(s, "id-1") == base {
			t.Errorf("%s left the fingerprint as it was", name)
		}
	}
}

func TestAllowsForwardCredential(t *testing.T) {
	scope := forwardScope()
	b := Bindings{
		IDs:       map[string]string{"wiki-reader": "id-1", "shared": "id-2", "other": "id-3"},
		Approvals: map[string]string{"wiki": Fingerprint(scope, "id-1")},
	}
	if !b.Allows(scope, "wiki-reader") || !b.Allows(scope, "shared") {
		t.Errorf("Allows() refused the own credential or a released forward credential")
	}
	if b.Allows(scope, "other") {
		t.Errorf("Allows() handed out a credential the scope does not release")
	}
	if !b.AllowsRole(scope, "shared", "user") || b.AllowsRole(scope, "shared", "key") {
		t.Errorf("AllowsRole() does not follow the fields of the forward credential")
	}
	// The approval was given without the forward credential: the scope that adds it no longer matches.
	old := wikiScope()
	b.Approvals["wiki"] = Fingerprint(old, "id-1")
	if b.Allows(scope, "shared") || b.Allows(scope, "wiki-reader") {
		t.Errorf("Allows() accepted a scope that releases more than the approved one")
	}
	if !b.Allows(old, "wiki-reader") || b.Allows(old, "shared") {
		t.Errorf("Allows() changed for a scope without forward credentials")
	}
	// A forward credential the vault holds no entry for is never handed out.
	delete(b.IDs, "shared")
	b.Approvals["wiki"] = Fingerprint(scope, "id-1")
	if b.Allows(scope, "shared") {
		t.Errorf("Allows() handed out a credential without an entry")
	}
}

func TestForwardNeedsNewApprovalAfterChange(t *testing.T) {
	lowWorkFactor(t)
	v := New(t.TempDir())
	if err := v.Set("wiki-reader", "token", "synthetic-token", offering("s3cret-phrase")); err != nil {
		t.Fatal(err)
	}
	if err := v.Set("shared", "user", "synthetic-user", nil); err != nil {
		t.Fatal(err)
	}
	scope := forwardScope()
	if err := v.Approve([]Scope{scope}); err != nil {
		t.Fatalf("Approve() = %v", err)
	}
	if err := v.CheckApprovalFor(scope, "shared"); err != nil {
		t.Fatalf("CheckApprovalFor() after Approve() = %v", err)
	}
	wider := forwardScope()
	wider.Forward[0].Fields = []string{"user", "pass", "key"}
	if err := v.CheckApprovalFor(wider, "shared"); !errors.Is(err, ErrApprovalRequired) {
		t.Errorf("CheckApprovalFor() after a change of the fields = %v, want ErrApprovalRequired", err)
	}
	if err := v.CheckApproval(wider); !errors.Is(err, ErrApprovalRequired) {
		t.Errorf("CheckApproval() after a change of the fields = %v, want ErrApprovalRequired", err)
	}
	released := wikiScope()
	if err := v.CheckApproval(released); !errors.Is(err, ErrApprovalRequired) {
		t.Errorf("CheckApproval() after the release was withdrawn = %v, want a new approval", err)
	}
}

func TestTokenCoverageOfForward(t *testing.T) {
	v := tokenVault(t)
	if err := v.Set("shared", "user", "synthetic-user", nil); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	vorbild := vorbildScope(base)
	vorbild.Forward = []ForwardSecret{
		{Name: "shared", Fields: []string{"user", "pass"}}, {Name: "extra", Fields: []string{"x"}},
	}
	approveScopes(t, v, vorbild)
	token := createToken(t, v, "agent", "vorbild")

	plain := vorbildScope(base)
	cases := []struct {
		name    string
		forward []ForwardSecret
		gaps    []string
	}{
		{"none", nil, nil},
		{"a subset", []ForwardSecret{{Name: "shared", Fields: []string{"pass", "user"}}}, nil},
		{"the same set", vorbild.Forward, nil},
		{"a superset", []ForwardSecret{
			{Name: "shared", Fields: []string{"user", "pass"}}, {Name: "extra", Fields: []string{"x"}},
			{Name: "third", Fields: []string{"y"}},
		}, []string{GapForward}},
		{"another credential", []ForwardSecret{{Name: "third", Fields: []string{"y"}}}, []string{GapForward}},
		{"other fields", []ForwardSecret{{Name: "shared", Fields: []string{"user", "key"}}}, []string{GapForward}},
	}
	for _, tc := range cases {
		change := plain
		change.Connection = "agent-" + tc.name
		change.Forward = tc.forward
		result, err := v.ApproveWithToken(token.Value, []Scope{vorbild, change}, []string{change.Connection},
			time.Now())
		if err != nil || len(result.Changes) != 1 {
			t.Fatalf("%s: ApproveWithToken() = %+v, %v", tc.name, result, err)
		}
		got := result.Changes[0]
		if got.Approved != (tc.gaps == nil) || !slices.Equal(got.Gaps, tc.gaps) {
			t.Errorf("%s: change = %+v, want gaps %v", tc.name, got, tc.gaps)
		}
	}

	// A vorbild without any forward credential covers none.
	bare := vorbildScope(base)
	bare.Connection = "bare"
	approveScopes(t, v, bare)
	bareToken := createToken(t, v, "bare-agent", "bare")
	change := plain
	change.Connection = "wants-forward"
	change.Forward = []ForwardSecret{{Name: "shared", Fields: []string{"user"}}}
	result, err := v.ApproveWithToken(bareToken.Value, []Scope{bare, change}, []string{change.Connection}, time.Now())
	if err != nil || len(result.Changes) != 1 || result.Changes[0].Approved ||
		!slices.Equal(result.Changes[0].Gaps, []string{GapForward}) {
		t.Errorf("a vorbild without forward covered a release: %+v, %v", result, err)
	}
}
