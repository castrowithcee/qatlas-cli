package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/helptopics"
)

// The help topics are a section of qatlas.1, the page every installation carries, and get no page of their
// own.
func TestHelpTopicsAreASectionOfTheMainPage(t *testing.T) {
	dir := t.TempDir()
	if err := generate(dir, "v0.0.0-test"); err != nil {
		t.Fatalf("generate() = %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "qatlas.1"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)
	section := strings.Index(page, ".SH HELP TOPICS")
	if section < 0 || section > strings.Index(page, ".SH SEE ALSO") {
		t.Fatalf("qatlas.1 has no HELP TOPICS section before SEE ALSO:\n%s", page)
	}
	for _, topic := range helptopics.All() {
		if !strings.Contains(page, ".SS "+topic.Name+"\n") {
			t.Errorf("qatlas.1 has no subsection %s", topic.Name)
		}
		if _, err := os.Stat(filepath.Join(dir, "qatlas-"+topic.Name+".1")); err == nil {
			t.Errorf("topic %s got a page of its own", topic.Name)
		}
	}
	for _, want := range []string{
		`qatlas invoke <tool\-id> \-\-connection <name> \-\-arg name=value`,
		"## Qatlas",
		`QATLAS_<CREDENTIAL>_<ROLE>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("qatlas.1 does not carry %q", want)
		}
	}
	for _, line := range strings.Split(page[section:], "\n") {
		if strings.HasPrefix(line, "'") {
			t.Errorf("a text line reads as a roff request: %q", line)
		}
	}
}

func TestRoffKeepsTextAsText(t *testing.T) {
	for in, want := range map[string]string{
		`.hidden`:      `\&.hidden`,
		`'quoted'`:     `\&'quoted'`,
		`a\b`:          `a\eb`,
		`--connection`: `\-\-connection`,
	} {
		if got := roff(in); got != want {
			t.Errorf("roff(%q) = %q, want %q", in, got, want)
		}
	}
}
