package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCountProcessesCountsOwnProgramProcessesOnly(t *testing.T) {
	root := t.TempDir()
	uid := os.Getuid()
	for pid, comm := range map[string]string{"10": "qatlas\n", "11": "qatlas\n", "12": "bash\n", "13": "qatlas\n", "14": "qconnect\n", "15": "qconnectx\n"} {
		if err := os.MkdirAll(filepath.Join(root, pid), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, pid, "comm"), []byte(comm), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "version"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := countProcesses(root, 13, uid); got != 3 {
		t.Errorf("countProcesses() = %d, want 3 (both names counted, own process and other commands left out)", got)
	}
	if got := countProcesses(root, 13, uid+1); got != 0 {
		t.Errorf("another user's processes counted: %d", got)
	}
	if got := countProcesses(filepath.Join(root, "missing"), 1, uid); got != -1 {
		t.Errorf("an unreadable /proc = %d, want -1", got)
	}
}
