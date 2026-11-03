package cli

import (
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

const wikiConnectionID = "0123456789abcdef0123456789abcdef"

// withWikiID gives the wiki connection a stable id in the configuration file; alerts keeps none, as a
// configuration of an older version would.
func withWikiID(t *testing.T, cfg string) {
	t.Helper()
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	old := "  wiki:\n    service: wiki\n    credential: reader\n"
	if !strings.Contains(string(data), old) {
		t.Fatal("test configuration changed")
	}
	updated := strings.Replace(string(data), old, old+"    id: "+wikiConnectionID+"\n", 1)
	if err := os.WriteFile(cfg, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionsShowTheIDAndTheOptionTakesIt(t *testing.T) {
	cfg, _, _ := catalogEnvironment(t)
	withWikiID(t, cfg)
	var reads atomic.Int32

	code, stdout, stderr := runTools(t, &reads, "connections", "--config", cfg)
	if code != exitOK || stderr != "" || !strings.Contains(stdout, "- name: wiki\n    id: "+wikiConnectionID+"\n    provider: bookstack") ||
		strings.Count(stdout, "id:") != 1 {
		t.Errorf("connections: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	byName, _, _ := runTools(t, &reads, "describe", "bookstack.pages.list", "--connection", "wiki", "--config", cfg)
	for _, selector := range []string{wikiConnectionID, "oldname|" + wikiConnectionID} {
		code, _, stderr := runTools(t, &reads, "describe", "bookstack.pages.list", "--connection", selector,
			"--config", cfg)
		if code != byName || stderr != "" {
			t.Errorf("describe --connection %s: exit=%d stderr=%q", selector, code, stderr)
		}
	}
	code, _, stderr = runTools(t, &reads, "describe", "bookstack.pages.list", "--connection", "nope", "--config", cfg)
	if code != exitUsage || !strings.Contains(stderr, "unknown-connection") || !strings.Contains(stderr, "name or id") {
		t.Errorf("unknown connection: exit=%d stderr=%q", code, stderr)
	}
}
