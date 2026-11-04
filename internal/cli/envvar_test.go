package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/envvar"
)

func TestUpdateCheckEnvironmentNames(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		off      bool
		wantWarn bool
	}{
		{"none", nil, false, false},
		{"new", map[string]string{"QCONNECT_NO_UPDATE_CHECK": "1"}, true, false},
		{"old", map[string]string{"QATLAS_NO_UPDATE_CHECK": "1"}, true, true},
		{"both", map[string]string{"QCONNECT_NO_UPDATE_CHECK": "1", "QATLAS_NO_UPDATE_CHECK": "1"}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var warn bytes.Buffer
			defer envvar.SetWarnWriter(&warn)()
			t.Setenv("QCONNECT_NO_UPDATE_CHECK", "")
			t.Setenv("QATLAS_NO_UPDATE_CHECK", "")
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			got := tuiUpdater(&Options{}, "1.2.3")
			if (got == nil) != c.off {
				t.Fatalf("updater nil = %v, want %v", got == nil, c.off)
			}
			if (warn.Len() > 0) != c.wantWarn || (c.wantWarn && !strings.Contains(warn.String(), "QCONNECT_NO_UPDATE_CHECK")) {
				t.Fatalf("warning = %q", warn.String())
			}
		})
	}
}
