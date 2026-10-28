//go:build unix

package selfupdate

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// scriptRelease is a prepared release whose program is a shell script.
func scriptRelease(script string, timeout time.Duration) *Prepared {
	return &Prepared{goos: "linux", payload: payload{executable: []byte("#!/bin/sh\n" + script)}, timeout: timeout}
}

func TestReadsConfigFollowsTheExitStatus(t *testing.T) {
	for name, tt := range map[string]struct {
		script string
		want   bool
		fails  bool
	}{
		"exit 0":        {"exit 0", true, false},
		"exit 1":        {"exit 1", false, false},
		"exit 3":        {"exit 3", false, false},
		"killed":        {"kill -9 $$", false, true},
		"not a program": {"", false, true},
	} {
		t.Run(name, func(t *testing.T) {
			p := scriptRelease(tt.script, 0)
			if name == "not a program" {
				p.payload.executable = []byte("no shebang, not a program")
			}
			got, err := p.ReadsConfig(context.Background(), []byte("version: 4\n"))
			if got != tt.want || (err != nil) != tt.fails {
				t.Fatalf("ReadsConfig() = %v, %v; want %v, error %v", got, err, tt.want, tt.fails)
			}
		})
	}
}

// The program gets a copy in a private directory, an environment without the caller's configuration, vault,
// or home, and the directory is gone afterwards.
func TestReadsConfigRunsOnACopyInAPrivateEmptyEnvironment(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "/real/config.yaml")
	t.Setenv("QATLAS_CLI_HOME", "/real/home")
	t.Setenv("GITHUB_TOKEN", "canary-token")
	record := filepath.Join(t.TempDir(), "record")
	script := `{ echo "args=$*"; echo "cwd=$(pwd)"; echo "mode=$(stat -c %a . 2>/dev/null || stat -f %Lp .)"; echo "copy=$(cat "$2")"; env; } > ` + record + "\nexit 0\n"
	secretConfig := "version: 4\n# canary-config-value\n"
	if ok, err := scriptRelease(script, 0).ReadsConfig(context.Background(), []byte(secretConfig)); !ok || err != nil {
		t.Fatalf("ReadsConfig() = %v, %v", ok, err)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, bad := range []string{"QATLAS_CONFIG", "QATLAS_CLI_HOME", "canary-token", "/real/"} {
		if strings.Contains(out, bad) {
			t.Errorf("the program saw %q:\n%s", bad, out)
		}
	}
	var dir, cwd string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "args=--config "):
			dir = filepath.Dir(strings.TrimSuffix(strings.TrimPrefix(line, "args=--config "), " config validate"))
		case strings.HasPrefix(line, "cwd="):
			cwd = strings.TrimPrefix(line, "cwd=")
		}
	}
	// On macOS the temporary directory lies behind the /var symlink, so pwd names it by another path. The
	// directory itself is gone by now, so only its parent can be resolved.
	if parent, err := filepath.EvalSymlinks(filepath.Dir(dir)); err != nil || filepath.Join(parent, filepath.Base(dir)) != cwd {
		t.Errorf("the copy lies in %s (%v), the program ran in %s", dir, err, cwd)
	}
	for _, want := range []string{"args=--config " + dir + "/config.yaml config validate", "mode=700", "canary-config-value", "HOME=" + dir} {
		if !strings.Contains(out, want) {
			t.Errorf("the record lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the check directory %s still exists: %v", dir, err)
	}
}

func TestReadsConfigEndsAProgramThatRunsTooLong(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	p := scriptRelease("echo $$ > "+pidFile+"\nexec sleep 30\n", 300*time.Millisecond)
	start := time.Now()
	ok, err := p.ReadsConfig(context.Background(), []byte("version: 4\n"))
	if ok || err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("ReadsConfig() = %v, %v", ok, err)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("took %s", time.Since(start))
	}
	raw, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatal(rerr)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		t.Errorf("process %d still runs", pid)
	}
}
