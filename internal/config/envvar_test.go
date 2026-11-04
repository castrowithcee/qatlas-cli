package config

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/envvar"
)

func TestPathEnvironmentNames(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		want     string
		wantWarn string
	}{
		{"new config", map[string]string{"QCONNECT_CONFIG": "/n/c.yaml"}, "/n/c.yaml", ""},
		{"old config", map[string]string{"QATLAS_CONFIG": "/o/c.yaml"}, "/o/c.yaml", "QCONNECT_CONFIG"},
		{"both config", map[string]string{"QCONNECT_CONFIG": "/n/c.yaml", "QATLAS_CONFIG": "/o/c.yaml"}, "/n/c.yaml", ""},
		{"new home", map[string]string{"QCONNECT_HOME": "/n"}, filepath.Join("/n", "config.yaml"), ""},
		{"old home", map[string]string{"QATLAS_CLI_HOME": "/o"}, filepath.Join("/o", "config.yaml"), "QCONNECT_HOME"},
		{"both home", map[string]string{"QCONNECT_HOME": "/n", "QATLAS_CLI_HOME": "/o"}, filepath.Join("/n", "config.yaml"), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var warn bytes.Buffer
			defer envvar.SetWarnWriter(&warn)()
			for _, k := range []string{"QCONNECT_CONFIG", "QATLAS_CONFIG", "QCONNECT_HOME", "QATLAS_CLI_HOME"} {
				t.Setenv(k, "")
			}
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			got, err := Path("")
			if err != nil || got != c.want {
				t.Fatalf("Path = %q, %v; want %q", got, err, c.want)
			}
			if _, err := Path(""); err != nil {
				t.Fatal(err)
			}
			if c.wantWarn == "" {
				if warn.Len() != 0 {
					t.Fatalf("unexpected warning %q", warn.String())
				}
				return
			}
			if strings.Count(warn.String(), "\n") != 1 || !strings.Contains(warn.String(), c.wantWarn) {
				t.Fatalf("warning = %q", warn.String())
			}
		})
	}
}

func TestMigrateHomeSeesBothSpellingsAsExplicit(t *testing.T) {
	for _, name := range []string{"QCONNECT_CONFIG", "QATLAS_CONFIG", "QCONNECT_HOME", "QATLAS_CLI_HOME"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			for _, k := range []string{"QCONNECT_CONFIG", "QATLAS_CONFIG", "QCONNECT_HOME", "QATLAS_CLI_HOME"} {
				t.Setenv(k, "")
			}
			t.Setenv(name, filepath.Join(home, "x"))
			if m, err := MigrateHome(""); m != nil || err != nil {
				t.Fatalf("MigrateHome = %v, %v", m, err)
			}
			if LegacyDefault(filepath.Join(home, "any")) {
				t.Fatal("LegacyDefault true with an explicit place")
			}
		})
	}
}
