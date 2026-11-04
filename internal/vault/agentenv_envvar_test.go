package vault

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/envvar"
)

func TestFindAgentTokenEnvironmentNames(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		want     string
		wantWarn bool
	}{
		{"new", map[string]string{"QCONNECT_AGENT_TOKEN": "n"}, "n", false},
		{"old", map[string]string{"QATLAS_AGENT_TOKEN": "o"}, "o", true},
		{"both", map[string]string{"QCONNECT_AGENT_TOKEN": "n", "QATLAS_AGENT_TOKEN": "o"}, "n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var warn bytes.Buffer
			defer envvar.SetWarnWriter(&warn)()
			env := c.env
			got, _, err := FindAgentToken(t.TempDir(), "", func(k string) string { return env[k] })
			if err != nil || got != c.want {
				t.Fatalf("got %q, %v", got, err)
			}
			if (warn.Len() > 0) != c.wantWarn || (c.wantWarn && !strings.Contains(warn.String(), "QCONNECT_AGENT_TOKEN")) {
				t.Fatalf("warning = %q", warn.String())
			}
		})
	}
}

func TestFindAgentTokenFileKeys(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		want     string
		wantWarn bool
	}{
		{"new", "QCONNECT_AGENT_TOKEN=n\n", "n", false},
		{"old", "QATLAS_AGENT_TOKEN=o\n", "o", true},
		{"both", "QATLAS_AGENT_TOKEN=o\nQCONNECT_AGENT_TOKEN=n\n", "n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var warn bytes.Buffer
			defer envvar.SetWarnWriter(&warn)()
			dir := t.TempDir()
			path := filepath.Join(dir, agentEnvPath)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(c.content), 0o600); err != nil {
				t.Fatal(err)
			}
			got, _, err := FindAgentToken(dir, "", func(string) string { return "" })
			if err != nil || got != c.want {
				t.Fatalf("got %q, %v", got, err)
			}
			if (warn.Len() > 0) != c.wantWarn || strings.Contains(warn.String(), "o\n") {
				t.Fatalf("warning = %q", warn.String())
			}
		})
	}
}
