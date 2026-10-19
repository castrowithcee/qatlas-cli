package approval

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	soonText  = "2030-01-02T12:00:00Z"
	laterText = "2030-01-02T18:00:00Z"
)

func setLimit(cfg *config.Config, name, limit string) {
	conn := cfg.Connections[name]
	conn.ActiveUntil = limit
	cfg.Connections[name] = conn
}

// Lengthening a limit by editing the file alone leaves the connection open, and the report reads both ends.
func TestLengtheningALimitIsAnOpenChange(t *testing.T) {
	local := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = local })
	cfg, v := fixture(t)
	setLimit(cfg, "wiki-write", soonText)
	if _, _, err := Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatal(err)
	}
	if report, err := Pending(cfg, v); err != nil || len(report.Open) != 0 {
		t.Fatalf("Pending() = %+v, %v, want nothing open", report, err)
	}

	setLimit(cfg, "wiki-write", laterText)
	report, err := Pending(cfg, v)
	if err != nil {
		t.Fatal(err)
	}
	if got := openNames(report); !reflect.DeepEqual(got, []string{"wiki-write"}) {
		t.Fatalf("open after lengthening = %v", got)
	}
	want := []FieldChange{{Field: FieldActiveUntil, Before: "2030-01-02 12:00:00 UTC", After: "2030-01-02 18:00:00 UTC"}}
	if got := report.Open[0].Fields; !reflect.DeepEqual(got, want) {
		t.Errorf("fields = %+v, want %+v", got, want)
	}
	if !DirectApprovable(report.Open[0]) {
		t.Error("a change of the limit alone is not directly approvable")
	}

	setLimit(cfg, "wiki-write", "")
	report, err = Pending(cfg, v)
	if err != nil {
		t.Fatal(err)
	}
	want = []FieldChange{{Field: FieldActiveUntil, Before: "2030-01-02 12:00:00 UTC", After: "unlimited"}}
	if len(report.Open) != 1 || !reflect.DeepEqual(report.Open[0].Fields, want) {
		t.Errorf("lifting = %+v, want %+v", report.Open, want)
	}
}

// A connection without a limit keeps its approval, and the instant counts however it is written.
func TestScopeCarriesTheLimitInOneForm(t *testing.T) {
	cfg, _ := fixture(t)
	setLimit(cfg, "wiki-read", "2030-01-02T14:00:00+02:00")
	resolved, err := cfg.Resolve("wiki-read", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := secret.ScopeOf(resolved).ActiveUntil; got != soonText {
		t.Errorf("ActiveUntil = %q, want %q", got, soonText)
	}
	resolved, _ = cfg.Resolve("wiki-write", "")
	if got := secret.ScopeOf(resolved).ActiveUntil; got != "" {
		t.Errorf("a connection without a limit has ActiveUntil %q", got)
	}
}

func TestExpiredVorbildIsLapsed(t *testing.T) {
	cfg, v := fixture(t)
	setLimit(cfg, "wiki-write", soonText)
	if _, _, err := Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatal(err)
	}
	token, err := v.CreateToken("agent", []string{"wiki-read", "wiki-write"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	before, after := time.Date(2030, 1, 2, 11, 0, 0, 0, time.UTC), time.Date(2030, 1, 2, 12, 0, 0, 0, time.UTC)
	if got, err := LapsedModels(cfg, v, token, before); err != nil || len(got) != 0 {
		t.Fatalf("LapsedModels() before the limit = %v, %v, want none", got, err)
	}
	if got, err := LapsedModels(cfg, v, token, after); err != nil || !reflect.DeepEqual(got, []string{"wiki-write"}) {
		t.Fatalf("LapsedModels() after the limit = %v, %v, want the expired vorbild", got, err)
	}
}
