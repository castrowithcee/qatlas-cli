package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The selftest and everything below it, the hidden worker included, is exempt from the configuration
// migration; no other command is.
func TestSelftestIsExemptFromMigration(t *testing.T) {
	root := newRootCommand(&Options{}, defaultRegistry())
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		below := c.Name() == "selftest" || (c.Parent() != nil && selfContained(c.Parent()))
		if got := selfContained(c); got != below {
			t.Errorf("selfContained(%q) = %v, want %v", c.CommandPath(), got, below)
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	for _, path := range []string{"selftest", "selftest storage", "selftest worker"} {
		c, _, err := root.Find(strings.Fields(path))
		if err != nil || !selfContained(c) {
			t.Errorf("%q: err %v, want a self-contained command", path, err)
		}
	}
}

// A configuration of an older schema stays as it is under the selftest, while another command migrates it.
func TestSelftestLeavesOlderConfigurationAlone(t *testing.T) {
	dir := schemaOneFixture(t)
	before, _ := os.ReadFile(configIn(dir))

	code, _, stderr := runWithInput(t, testOptionsIn(t, dir, nil), "", "selftest", "--config", configIn(dir))
	if code != exitOK || strings.Contains(stderr, "migrated") {
		t.Fatalf("selftest: exit %d, stderr %q", code, stderr)
	}
	if after, _ := os.ReadFile(configIn(dir)); string(after) != string(before) {
		t.Fatalf("the configuration changed under the selftest:\n%s", after)
	}

	code, _, stderr = runWithInput(t, testOptionsIn(t, dir, nil), "", "config", "validate", "--config", configIn(dir))
	if code != exitOK || !strings.Contains(stderr, "migrated") {
		t.Fatalf("control run: exit %d, stderr %q, want a migration", code, stderr)
	}
}

// An unknown profile is a usage error.
func TestSelftestRejectsUnknownProfile(t *testing.T) {
	code, _, stderr := runWithInput(t, &Options{}, "", "selftest", "storage", "--profile", "huge")
	if code != exitUsage || !strings.Contains(stderr, `unknown selftest profile "huge"`) {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}
