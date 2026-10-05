//go:build windows

package selfupdate

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("QATLAS_SELFUPDATE_HELPER") == "sleep" {
		fmt.Println("ready")
		time.Sleep(time.Minute)
		return
	}
	os.Exit(m.Run())
}

// runningInstallation copies this test program to <prefix>\bin\qatlas.exe and starts it, so the file is a
// running executable the way an installed qatlas is during an update.
func runningInstallation(t *testing.T) (executable string, stop func()) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	executable = filepath.Join(t.TempDir(), "bin", "qatlas.exe")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, body, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable)
	cmd.Env = append(os.Environ(), "QATLAS_SELFUPDATE_HELPER=sleep")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stop = func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }
	t.Cleanup(stop)
	// Wait until the copy really runs, so the tests do not depend on timing.
	ready := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(out).ReadString('\n')
		if err == nil && strings.TrimSpace(line) != "ready" {
			err = fmt.Errorf("unexpected output %q", line)
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("the running copy did not report: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the running copy did not start")
	}
	return executable, stop
}

func TestInstallReplacesRunningExecutable(t *testing.T) {
	executable, _ := runningInstallation(t)
	// A running file cannot be overwritten; this is the premise of the rename.
	if err := os.WriteFile(executable, []byte("x"), 0o755); err == nil {
		t.Fatal("overwriting a running executable succeeded")
	}
	c := &Client{Executable: executable}
	if err := c.install(context.Background(), payload{executable: []byte("new-binary")}, "windows", Release{}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, executable, "new-binary")
	if _, err := os.Stat(executable + ".old"); err != nil {
		t.Errorf("the running file is not kept as .old: %v", err)
	}
}

func TestInstallOnWindowsKeepsRunningExecutableWhenMoveFails(t *testing.T) {
	executable, _ := runningInstallation(t)
	before, _ := os.ReadFile(executable)
	r := &opsRecorder{failRename: map[string]error{}}
	staged := filepath.Join(filepath.Dir(executable), "staged")
	if err := os.WriteFile(staged, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.failRename["staged>qatlas.exe"] = os.ErrPermission
	if err := replaceRunning(r.ops(), staged, executable); err == nil {
		t.Fatal("want error")
	}
	after, _ := os.ReadFile(executable)
	if string(before) != string(after) {
		t.Error("installation changed after a failed move")
	}
}

func TestLeftoverOfRunningProgramIsRemovedAfterItEnds(t *testing.T) {
	executable, stop := runningInstallation(t)
	c := &Client{Executable: executable}
	if err := c.install(context.Background(), payload{executable: []byte("v2")}, "windows", Release{}); err != nil {
		t.Fatal(err)
	}
	// While the old program runs, its file cannot be removed.
	removeLeftovers(osFileOps, executable)
	if _, err := os.Stat(executable + ".old"); err != nil {
		t.Fatalf(".old of a running program is gone: %v", err)
	}
	stop()
	removeLeftovers(osFileOps, executable)
	if _, err := os.Stat(executable + ".old"); !os.IsNotExist(err) {
		t.Errorf(".old remains after the program ended: %v", err)
	}
	assertFile(t, executable, "v2")
}

func TestNextUpdateRemovesLeftover(t *testing.T) {
	executable, stop := runningInstallation(t)
	c := &Client{Executable: executable}
	if err := c.install(context.Background(), payload{executable: []byte("v2")}, "windows", Release{}); err != nil {
		t.Fatal(err)
	}
	stop()
	if err := c.install(context.Background(), payload{executable: []byte("v3")}, "windows", Release{}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, executable, "v3")
	// Neither the v1 leftover of the first update nor the v2 file, which no longer runs, remains.
	if _, err := os.Stat(executable + ".old"); !os.IsNotExist(err) {
		t.Errorf(".old remains although no replaced program runs: %v", err)
	}
	if matches, _ := filepath.Glob(executable + ".old-*"); len(matches) != 0 {
		t.Errorf("unique leftovers: %v", matches)
	}
}
