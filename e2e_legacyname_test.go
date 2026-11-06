package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

const legacyNote = "this program is now called qconnect"

// legacyCopy makes the built binary available under the former name in a directory of its own: a link
// where the platform has them, else a copy.
func legacyCopy(t *testing.T, bin string) string {
	t.Helper()
	name := "qatlas"
	if runtime.GOOS == "windows" {
		name = "qatlas.exe"
	}
	dest := filepath.Join(t.TempDir(), name)
	if runtime.GOOS != "windows" {
		if err := os.Symlink(bin, dest); err != nil {
			t.Fatal(err)
		}
		return dest
	}
	in, err := os.Open(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return dest
}

// The former program name keeps working: the answer is the same, and the note on the new name is on
// stderr only, never in the JSON or the MCP stream.
func TestFormerProgramNameWorksAndNotesOnStderrOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	dir := t.TempDir()
	bin := buildBinary(t, dir)
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("version: 4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + dir, "PATH=" + os.Getenv("PATH"), secret.StoreSelector + "=none"}
	var seen strings.Builder
	current := &runner{bin: bin, env: env, seen: &seen}
	former := &runner{bin: legacyCopy(t, bin), env: env, seen: &seen}

	code, stdout, stderr := current.run(t, "--config", configPath, "providers", "--output", "json")
	if code != 0 || strings.Contains(stderr, legacyNote) {
		t.Fatalf("current name: exit %d, stderr %q; want success and no note", code, stderr)
	}
	code, formerStdout, stderr := former.run(t, "--config", configPath, "providers", "--output", "json")
	if code != 0 || !strings.Contains(stderr, legacyNote) || strings.Count(stderr, legacyNote) != 1 {
		t.Fatalf("former name: exit %d, stderr %q; want success and one note", code, stderr)
	}
	if formerStdout != stdout || !json.Valid([]byte(formerStdout)) || strings.Contains(formerStdout, legacyNote) {
		t.Errorf("former name stdout differs or is no JSON: %q", formerStdout)
	}

	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	for name, r := range map[string]*runner{"qconnect": current, "qatlas": former} {
		code, stdout, stderr := r.runInput(t, input, "--config", configPath, "mcp")
		if code != 0 {
			t.Fatalf("%s mcp: exit %d, stderr %q", name, code, stderr)
		}
		if strings.Contains(stdout, legacyNote) {
			t.Errorf("%s mcp: the note is in the protocol stream: %q", name, stdout)
		}
		if (name == "qatlas") != strings.Contains(stderr, legacyNote) {
			t.Errorf("%s mcp: stderr = %q, want the note exactly for the former name", name, stderr)
		}
		for _, want := range []string{`"name":"qconnect"`, `"name":"search"`, `"name":"describe"`, `"name":"invoke"`} {
			if !strings.Contains(stdout, want) {
				t.Errorf("%s mcp: stdout lacks %s:\n%s", name, want, stdout)
			}
		}
		for _, old := range []string{`"qconnect.search"`, `"qconnect.describe"`, `"qconnect.invoke"`, `"qatlas.search"`, `"qatlas.describe"`, `"qatlas.invoke"`, `"name":"qatlas"`} {
			if strings.Contains(stdout, old) {
				t.Errorf("%s mcp: stdout still offers %s", name, old)
			}
		}
	}
}
