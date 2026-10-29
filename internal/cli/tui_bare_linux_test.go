//go:build linux

package cli

import (
	"strings"
	"testing"
	"time"
)

// A bare qatlas on a pseudo-terminal opens the editor, and q ends it.
func TestBareQatlasOpensTheEditorOnAPseudoTerminal(t *testing.T) {
	if testing.Short() {
		t.Skip("the acceptance run starts the editor on a pseudo-terminal")
	}
	r := newUpdateRigArgs(t, "", "")
	r.wait(0, "Update available v1.0.0 → v2.0.0")
	if got := altScreenOn; !strings.Contains(r.bytes(), got) {
		t.Error("the editor did not enter the alternate screen")
	}
	r.typeKeys("q")
	done := make(chan error, 1)
	go func() { done <- r.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the editor ended with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("the editor did not end on q:\n%s", r.screen())
	}
}
