package approval

import (
	"reflect"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

func TestDiffShowsExcludeChange(t *testing.T) {
	approved := vault.Approval{Scope: scopeFixture()}
	approved.Scope.Exclude = []string{"a", "b"}
	now := scopeFixture()
	now.Exclude = []string{"b", "c"}
	fields := diff(approved, now, "", nil)
	if len(fields) != 1 {
		t.Fatalf("diff() = %+v, want only exclude", fields)
	}
	got := fieldOf(t, fields, FieldExclude)
	if !reflect.DeepEqual(got.Added, []string{"c"}) || !reflect.DeepEqual(got.Removed, []string{"a"}) ||
		!reflect.DeepEqual(got.Kept, []string{"b"}) {
		t.Errorf("exclude change = %+v", got)
	}
	if DirectApprovable(Change{Fields: fields}) {
		t.Error("DirectApprovable() = true for a changed exclude list")
	}
	now.Exclude = nil
	if got := diff(vault.Approval{Scope: scopeFixture()}, now, "", nil); len(got) != 0 {
		t.Errorf("diff() of equal scopes = %+v", got)
	}
}
