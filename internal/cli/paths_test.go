package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// A connection bound to paths exists for discovery and invoke only in a project inside them. Everywhere
// else every command leaves it out, and naming it is refused as unknown without naming any path.
func TestBoundConnectionsFollowTheWorkingDirectory(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	project := filepath.Join(t.TempDir(), "kunde-a")
	outside := filepath.Join(t.TempDir(), "kunde-ab")
	for _, dir := range []string{filepath.Join(project, "src"), outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	absent := filepath.Join(t.TempDir(), "absent")
	cfg := writeConfig(t, fmt.Sprintf(`version: 1
services:
  wiki:
    provider: bookstack
    base_url: https://wiki.example.invalid
credentials:
  reader:
    type: env
    values:
      token-id: PATHS_TOKEN_ID
      token-secret: PATHS_TOKEN_SECRET
connections:
  customer:
    service: wiki
    credential: reader
    permissions: [read]
    paths: [%q]
  shared:
    service: wiki
    credential: reader
    permissions: [read]
  ghost:
    service: wiki
    credential: reader
    permissions: [read]
    paths: [%q]
defaults:
  connections:
    bookstack: customer
`, project, absent))
	var reads atomic.Int32

	t.Chdir(outside)
	code, stdout, stderr := runTools(t, &reads, "connections", "--config", cfg, "--output", "json")
	if code != exitOK || stderr != "" || !strings.Contains(stdout, `"shared"`) ||
		strings.Contains(stdout, "customer") || strings.Contains(stdout, "ghost") {
		t.Fatalf("connections outside: exit=%d stdout=%s stderr=%q", code, stdout, stderr)
	}
	code, stdout, _ = runTools(t, &reads, "providers", "--config", cfg, "--output", "json")
	var providers struct {
		Providers []struct {
			Provider    string `json:"provider"`
			Connections int    `json:"connections"`
			Configured  int    `json:"configured"`
		} `json:"providers"`
	}
	if err := json.Unmarshal([]byte(stdout), &providers); code != exitOK || err != nil {
		t.Fatalf("providers: exit=%d err=%v", code, err)
	}
	for _, p := range providers.Providers {
		if p.Provider == "bookstack" && (p.Connections != 1 || p.Configured != 1) {
			t.Errorf("providers outside counts bookstack %+v, want only shared", p)
		}
	}
	code, stdout, _ = runTools(t, &reads, "tools", "bookstack", "--config", cfg, "--output", "json")
	if code != exitOK || strings.Contains(stdout, "customer") || !strings.Contains(stdout, "shared") {
		t.Errorf("tools outside: exit=%d stdout=%s", code, stdout)
	}
	for _, args := range [][]string{
		{"describe", "bookstack.pages.list", "--connection", "customer"},
		{"invoke", "bookstack.pages.list", "--connection", "customer"},
		{"tools", "bookstack", "--connection", "customer"},
	} {
		code, stdout, stderr = runTools(t, &reads, append(args, "--config", cfg)...)
		if code != exitUsage || stdout != "" || !strings.Contains(stderr, `unknown connection "customer"`) {
			t.Errorf("%v outside: exit=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
		if !strings.Contains(stderr, "bound to the paths of other projects") || strings.Contains(stderr, project) ||
			strings.Contains(stderr, absent) {
			t.Errorf("%v outside: stderr=%q, want the general hint and no path", args, stderr)
		}
	}
	if reads.Load() != 0 {
		t.Errorf("secret lookups = %d, want none", reads.Load())
	}

	t.Chdir(filepath.Join(project, "src"))
	code, stdout, stderr = runTools(t, &reads, "connections", "--config", cfg, "--output", "json")
	if code != exitOK || stderr != "" || !strings.Contains(stdout, `"customer"`) ||
		!strings.Contains(stdout, `"shared"`) || strings.Contains(stdout, "ghost") {
		t.Fatalf("connections inside: exit=%d stdout=%s stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runTools(t, &reads, "describe", "bookstack.pages.list", "--connection", "customer",
		"--config", cfg)
	if code != exitOK {
		t.Errorf("describe inside: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	// Validate is a managing command: it checks every connection wherever it runs, and warns about the path
	// that names no directory by its position alone.
	t.Chdir(outside)
	code, stdout, stderr = runTools(t, &reads, "config", "validate", "--config", cfg)
	if code != exitOK || !strings.HasPrefix(stdout, "configuration is valid") ||
		!strings.Contains(stderr, `qatlas: warning: connection "ghost": paths[0] names no existing directory`) ||
		strings.Contains(stderr, absent) || strings.Contains(stderr, `"customer"`) {
		t.Errorf("validate: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
