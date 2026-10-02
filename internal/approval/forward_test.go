package approval

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// A change of forward_secrets, or of the fields of a credential listed there, leaves the connection open and
// shows as the forward field; a direct save of the connection's own form never approves it.
func TestForwardChangeIsAnOpenChange(t *testing.T) {
	cfg, v := fixture(t)
	cfg.Credentials["shared"] = config.Credential{Type: config.CredentialTypeVault, Forward: true,
		Fields: []string{"user", "pass"}}
	if err := v.Set("shared", "user", "synthetic-user", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatal(err)
	}

	read := cfg.Connections["wiki-read"]
	read.ForwardSecrets = []string{"shared"}
	cfg.Connections["wiki-read"] = read
	report, err := Pending(cfg, v)
	if err != nil {
		t.Fatal(err)
	}
	if got := openNames(report); !reflect.DeepEqual(got, []string{"wiki-read"}) {
		t.Fatalf("Pending().Open = %v, want the connection that began to release a credential", got)
	}
	want := []FieldChange{{Field: FieldForward, Before: "(none)", After: "shared (fields: pass user)"}}
	if got := report.Open[0].Fields; !reflect.DeepEqual(got, want) {
		t.Errorf("Fields = %+v, want %+v", got, want)
	}
	if DirectApprovable(report.Open[0]) {
		t.Errorf("DirectApprovable() approved a change of the forward credentials in passing")
	}
	for _, f := range report.Open[0].Fields {
		if strings.Contains(f.Before+f.After, "synthetic") {
			t.Errorf("the diff carries a value: %+v", f)
		}
	}

	if _, _, err := Approve(context.Background(), cfg, v, []string{"wiki-read"}); err != nil {
		t.Fatal(err)
	}
	if report, _ := Pending(cfg, v); len(report.Open) != 0 {
		t.Fatalf("Pending() after Approve() = %v, want nothing open", openNames(report))
	}

	// A field added to the listed credential reopens it.
	shared := cfg.Credentials["shared"]
	shared.Fields = []string{"user", "pass", "token"}
	cfg.Credentials["shared"] = shared
	report, err = Pending(cfg, v)
	if err != nil || !reflect.DeepEqual(openNames(report), []string{"wiki-read"}) {
		t.Fatalf("Pending() after a field was added = %v, %v", openNames(report), err)
	}
	if got := report.Open[0].Fields; len(got) != 1 || got[0].Field != FieldForward ||
		got[0].Before != "shared (fields: pass user)" || got[0].After != "shared (fields: pass token user)" {
		t.Errorf("Fields = %+v, want the forward field with both lists", got)
	}

	// Withdrawing the release reopens it as well.
	read.ForwardSecrets = nil
	cfg.Connections["wiki-read"] = read
	shared.Fields = []string{"user", "pass"}
	cfg.Credentials["shared"] = shared
	report, _ = Pending(cfg, v)
	if len(report.Open) != 1 || report.Open[0].Fields[0].After != "(none)" {
		t.Errorf("Pending() after the release was withdrawn = %+v", report.Open)
	}
}
