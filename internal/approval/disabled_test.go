package approval

import (
	"context"
	"reflect"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func setDisabled(cfg *config.Config, name string, disabled bool) {
	conn := cfg.Connections[name]
	conn.Disabled = disabled
	cfg.Connections[name] = conn
}

// Switching a connection on again by editing the file alone leaves it open, and the report names the state
// as it was and is; switching it off is a change too.
func TestStateChangeIsAnOpenChange(t *testing.T) {
	cfg, v := fixture(t)
	setDisabled(cfg, "wiki-write", true)
	if _, _, err := Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatal(err)
	}

	setDisabled(cfg, "wiki-write", false)
	report, err := Pending(cfg, v)
	if err != nil {
		t.Fatal(err)
	}
	if got := openNames(report); !reflect.DeepEqual(got, []string{"wiki-write"}) {
		t.Fatalf("open after switching on = %v", got)
	}
	want := []FieldChange{{Field: FieldState, Before: "disabled", After: "active"}}
	if got := report.Open[0].Fields; !reflect.DeepEqual(got, want) {
		t.Errorf("fields = %+v, want %+v", got, want)
	}
	if !DirectApprovable(report.Open[0]) {
		t.Error("a change of the state alone is not directly approvable")
	}

	if _, _, err := Approve(context.Background(), cfg, v, []string{"wiki-write"}); err != nil {
		t.Fatal(err)
	}
	setDisabled(cfg, "wiki-write", true)
	report, err = Pending(cfg, v)
	if err != nil {
		t.Fatal(err)
	}
	want = []FieldChange{{Field: FieldState, Before: "active", After: "disabled"}}
	if len(report.Open) != 1 || !reflect.DeepEqual(report.Open[0].Fields, want) {
		t.Errorf("switching off = %+v, want %+v", report.Open, want)
	}
}

// An approval given before the state existed stays valid for an active connection.
func TestActiveConnectionKeepsItsApproval(t *testing.T) {
	cfg, v := fixture(t)
	if _, _, err := Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatal(err)
	}
	resolved, err := cfg.Resolve("wiki-read", "")
	if err != nil {
		t.Fatal(err)
	}
	if secret.ScopeOf(resolved).Disabled {
		t.Fatal("an active connection has a disabled scope")
	}
	if report, err := Pending(cfg, v); err != nil || len(report.Open) != 0 {
		t.Fatalf("Pending() = %+v, %v, want nothing open", report, err)
	}
}

func TestDisabledVorbildIsLapsed(t *testing.T) {
	cfg, v := fixture(t)
	if _, _, err := Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatal(err)
	}
	token, err := v.CreateToken("agent", []string{"wiki-read", "wiki-write"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := LapsedModels(cfg, v, token); err != nil || len(got) != 0 {
		t.Fatalf("LapsedModels() = %v, %v, want none", got, err)
	}
	// Disabled as configured now, though still approved active.
	setDisabled(cfg, "wiki-write", true)
	if got, err := LapsedModels(cfg, v, token); err != nil || !reflect.DeepEqual(got, []string{"wiki-write"}) {
		t.Fatalf("LapsedModels() = %v, %v, want the disabled vorbild", got, err)
	}
	// Disabled and approved so.
	if _, _, err := Approve(context.Background(), cfg, v, []string{"wiki-write"}); err != nil {
		t.Fatal(err)
	}
	if got, err := LapsedModels(cfg, v, token); err != nil || !reflect.DeepEqual(got, []string{"wiki-write"}) {
		t.Fatalf("LapsedModels() = %v, %v, want the disabled vorbild", got, err)
	}
}
