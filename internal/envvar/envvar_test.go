package envvar

import (
	"bytes"
	"strings"
	"testing"
)

func lookup(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestGetWithPrecedenceAndWarning(t *testing.T) {
	var buf bytes.Buffer
	defer SetWarnWriter(&buf)()

	if got := GetWith(lookup(map[string]string{"QCONNECT_X": "new"}), "QCONNECT_X"); got != "new" || buf.Len() != 0 {
		t.Fatalf("new: %q, warning %q", got, buf.String())
	}
	both := lookup(map[string]string{"QCONNECT_X": "new", "QATLAS_X": "old"})
	if got := GetWith(both, "QCONNECT_X"); got != "new" || buf.Len() != 0 {
		t.Fatalf("both: %q, warning %q", got, buf.String())
	}
	old := lookup(map[string]string{"QATLAS_X": "old-secret"})
	for i := 0; i < 3; i++ {
		if got := GetWith(old, "QCONNECT_X"); got != "old-secret" {
			t.Fatalf("old: %q", got)
		}
	}
	out := buf.String()
	if strings.Count(out, "\n") != 1 || !strings.Contains(out, "QCONNECT_X") || !strings.Contains(out, "QATLAS_X") {
		t.Fatalf("warning = %q", out)
	}
	if strings.Contains(out, "old-secret") {
		t.Fatal("warning leaks the value")
	}
	if got := GetWith(lookup(nil), "QCONNECT_X"); got != "" {
		t.Fatalf("unset: %q", got)
	}
}

func TestEmptyNewFallsBack(t *testing.T) {
	var buf bytes.Buffer
	defer SetWarnWriter(&buf)()
	got := GetWith(lookup(map[string]string{"QCONNECT_X": "", "QATLAS_X": "old"}), "QCONNECT_X")
	if got != "old" || buf.Len() == 0 {
		t.Fatalf("got %q, warning %q", got, buf.String())
	}
}

func TestOtherNamesHaveNoFallback(t *testing.T) {
	var buf bytes.Buffer
	defer SetWarnWriter(&buf)()
	if got := GetWith(lookup(map[string]string{"QATLAS_X": "old"}), "OTHER_X"); got != "" || buf.Len() != 0 {
		t.Fatalf("got %q, warning %q", got, buf.String())
	}
}

func TestNoWriterStaysSilent(t *testing.T) {
	defer SetWarnWriter(nil)()
	if got := GetWith(lookup(map[string]string{"QATLAS_X": "old"}), "QCONNECT_X"); got != "old" {
		t.Fatalf("got %q", got)
	}
}

func TestSet(t *testing.T) {
	var buf bytes.Buffer
	defer SetWarnWriter(&buf)()
	t.Setenv("QATLAS_Y", "v")
	if !Set("QCONNECT_Y") || buf.Len() != 0 {
		t.Fatalf("Set = false or warned: %q", buf.String())
	}
	if Set("QCONNECT_Z_UNSET") {
		t.Fatal("unset reported set")
	}
}

func TestLegacyHome(t *testing.T) {
	if got := Legacy("QCONNECT_HOME"); got != "QATLAS_CLI_HOME" {
		t.Fatalf("got %q", got)
	}
}
