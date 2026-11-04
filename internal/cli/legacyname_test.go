package cli

import (
	"strings"
	"testing"
)

func TestCalledByLegacyName(t *testing.T) {
	for program, want := range map[string]bool{
		"qatlas": true, "/usr/local/bin/qatlas": true, "./QATLAS.EXE": true, "qatlas.exe": true, "qconnect": false, "/x/qconnect": false, "qatlas-connect": false, "qatlasx": false,
	} {
		if got := calledByLegacyName(program); got != want {
			t.Errorf("calledByLegacyName(%q) = %v, want %v", program, got, want)
		}
	}
}

func TestNoteLegacyNameSilentForHiddenAndCurrentName(t *testing.T) {
	root := newRootCommand(&Options{}, defaultRegistry())
	for _, tt := range []struct {
		program string
		args    []string
		want    bool
	}{
		{"/bin/qatlas", []string{"providers"}, true},
		{"/bin/qatlas", []string{"mcp"}, true},
		{"/bin/qatlas", []string{"vault", "serve"}, false},
		{"/bin/qconnect", []string{"providers"}, false},
	} {
		var out strings.Builder
		noteLegacyName(tt.program, root, tt.args, &out)
		if got := strings.Contains(out.String(), "qconnect"); got != tt.want {
			t.Errorf("noteLegacyName(%q, %v) wrote %q, want a note: %v", tt.program, tt.args, out.String(), tt.want)
		}
	}
}
