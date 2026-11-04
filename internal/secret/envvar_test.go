package secret

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/envvar"
)

func TestDerivedEnvNameUsesNewPrefix(t *testing.T) {
	if got := DerivedEnvName("wiki-reader", "token-id"); got != "QCONNECT_WIKI_READER_TOKEN_ID" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveDerivedNameFallsBackToLegacy(t *testing.T) {
	newName := DerivedEnvName(credName, role)
	oldName := envvar.Legacy(newName)
	cases := []struct {
		name     string
		env      map[string]string
		want     string
		wantWarn bool
	}{
		{"new", map[string]string{newName: "new-v"}, "new-v", false},
		{"old", map[string]string{oldName: "old-v"}, "old-v", true},
		{"both", map[string]string{newName: "new-v", oldName: "old-v"}, "new-v", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var warn bytes.Buffer
			defer envvar.SetWarnWriter(&warn)()
			env := c.env
			r := NewWith(func(k string) string { return env[k] }, nil, nil, nil)
			for i := 0; i < 2; i++ {
				v, err := r.Resolve(context.Background(), credName, keyringCred(), role)
				if err != nil || v.Secret != c.want {
					t.Fatalf("Resolve = %v", err)
				}
			}
			out := warn.String()
			if !c.wantWarn {
				if out != "" {
					t.Fatalf("unexpected warning %q", out)
				}
				return
			}
			if strings.Count(out, "\n") != 1 || !strings.Contains(out, newName) || strings.Contains(out, "old-v") {
				t.Fatalf("warning = %q", out)
			}
			if !r.LookupDerived(newName) || r.Lookup(newName) {
				t.Fatal("LookupDerived/Lookup disagree on the legacy name")
			}
		})
	}
}

func TestEnvTypeCredentialKeepsItsOwnName(t *testing.T) {
	var warn bytes.Buffer
	defer envvar.SetWarnWriter(&warn)()
	env := map[string]string{"QATLAS_FOO": "v"}
	r := NewWith(func(k string) string { return env[k] }, nil, nil, nil)
	cred := config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{role: "QCONNECT_FOO"}}
	if _, err := r.Resolve(context.Background(), credName, cred, role); err == nil {
		t.Fatal("an env credential must read exactly the variable it names")
	}
	if warn.Len() != 0 {
		t.Fatalf("warning %q", warn.String())
	}
}

func TestSystemStoreSelectorNames(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		wantNone bool
		wantWarn bool
	}{
		{"new", map[string]string{"QCONNECT_CREDENTIAL_STORE": "none"}, true, false},
		{"old", map[string]string{"QATLAS_CREDENTIAL_STORE": "none"}, true, true},
		{"both", map[string]string{"QCONNECT_CREDENTIAL_STORE": "auto", "QATLAS_CREDENTIAL_STORE": "none"}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var warn bytes.Buffer
			defer envvar.SetWarnWriter(&warn)()
			t.Setenv("QCONNECT_CREDENTIAL_STORE", "")
			t.Setenv("QATLAS_CREDENTIAL_STORE", "")
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			store, err := SystemStore()
			if err != nil {
				t.Fatal(err)
			}
			_, none := store.(unavailableStore)
			if none != c.wantNone {
				t.Fatalf("none = %v", none)
			}
			if (warn.Len() > 0) != c.wantWarn || (c.wantWarn && !strings.Contains(warn.String(), "QCONNECT_CREDENTIAL_STORE")) {
				t.Fatalf("warning = %q", warn.String())
			}
		})
	}
}
