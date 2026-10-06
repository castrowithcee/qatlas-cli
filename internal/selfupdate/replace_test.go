package selfupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// opsRecorder performs the real operations, records them, and fails the ones the test names.
type opsRecorder struct {
	failRename map[string]error // keyed by the source path of the rename
	failRemove map[string]error // keyed by a file name prefix
	calls      []string
}

func (r *opsRecorder) ops() fileOps {
	return fileOps{
		rename: func(from, to string) error {
			r.calls = append(r.calls, "rename "+filepath.Base(from)+" "+filepath.Base(to))
			if err := r.failRename[filepath.Base(from)+">"+filepath.Base(to)]; err != nil {
				return err
			}
			return os.Rename(from, to)
		},
		remove: func(path string) error {
			r.calls = append(r.calls, "remove "+filepath.Base(path))
			for prefix, err := range r.failRemove {
				if strings.HasPrefix(filepath.Base(path), prefix) {
					return err
				}
			}
			return os.Remove(path)
		},
	}
}

func replacementFiles(t *testing.T) (staged, target string) {
	t.Helper()
	dir := t.TempDir()
	staged, target = filepath.Join(dir, "staged"), filepath.Join(dir, "qatlas.exe")
	for path, body := range map[string]string{staged: "new", target: "old"} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return staged, target
}

func TestReplaceRunningSequence(t *testing.T) {
	staged, target := replacementFiles(t)
	if err := os.WriteFile(target+".old", []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &opsRecorder{}
	if err := replaceRunning(r.ops(), staged, target); err != nil {
		t.Fatal(err)
	}
	want := "remove qatlas.exe.old|rename qatlas.exe qatlas.exe.old|rename staged qatlas.exe|remove qatlas.exe.old"
	if got := strings.Join(r.calls, "|"); got != want {
		t.Errorf("calls = %s, want %s", got, want)
	}
	assertFile(t, target, "new")
}

func TestReplaceRunningUsesUniqueNameForStuckLeftover(t *testing.T) {
	staged, target := replacementFiles(t)
	// A leftover that cannot be removed is avoided by a unique name.
	r := &opsRecorder{failRemove: map[string]error{"qatlas.exe.old": errors.New("in use")}}
	if err := replaceRunning(r.ops(), staged, target); err != nil {
		t.Fatal(err)
	}
	assertFile(t, target, "new")
	matches, _ := filepath.Glob(target + ".old-*")
	if len(matches) != 1 {
		t.Fatalf("unique old files = %v", matches)
	}
	assertFile(t, matches[0], "old")
	removeLeftovers(osFileOps, target)
	if matches, _ := filepath.Glob(target + ".old*"); len(matches) != 0 {
		t.Errorf("leftovers remain: %v", matches)
	}
	assertFile(t, target, "new")
}

func TestReplaceRunningRestoresOldFileWhenMoveFails(t *testing.T) {
	staged, target := replacementFiles(t)
	r := &opsRecorder{failRename: map[string]error{"staged>qatlas.exe": errors.New("denied")}}
	err := replaceRunning(r.ops(), staged, target)
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("err = %v, want the move failure", err)
	}
	assertFile(t, target, "old")
	if _, err := os.Stat(target + ".old"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("old file remains: %v", err)
	}
}

func TestReplaceRunningReportsFailedRestore(t *testing.T) {
	staged, target := replacementFiles(t)
	r := &opsRecorder{failRename: map[string]error{
		"staged>qatlas.exe":         errors.New("denied"),
		"qatlas.exe.old>qatlas.exe": errors.New("also denied"),
	}}
	err := replaceRunning(r.ops(), staged, target)
	if err == nil || !strings.Contains(err.Error(), "denied") || !strings.Contains(err.Error(), "also denied") {
		t.Fatalf("err = %v, want both failures", err)
	}
}

func TestReplaceRunningFirstStepFailureChangesNothing(t *testing.T) {
	staged, target := replacementFiles(t)
	r := &opsRecorder{failRename: map[string]error{"qatlas.exe>qatlas.exe.old": errors.New("locked")}}
	if err := replaceRunning(r.ops(), staged, target); err == nil {
		t.Fatal("want error")
	}
	assertFile(t, target, "old")
	assertFile(t, staged, "new")
}

func TestRemoveLeftoversTouchesOnlyOwnOldFiles(t *testing.T) {
	_, target := replacementFiles(t)
	dir := filepath.Dir(target)
	for _, name := range []string{"qatlas.exe.old", "qatlas.exe.old-1x", "other.exe.old", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	removeLeftovers(osFileOps, target)
	entries, _ := os.ReadDir(dir)
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if got := strings.Join(left, ","); got != "notes.txt,other.exe.old,qatlas.exe,staged" {
		t.Errorf("left = %s", got)
	}
}

// A Windows installation is replaced without a manpage, and no manpage or share directory appears.
func TestInstallOnWindowsLayoutHasNoManpage(t *testing.T) {
	prefix := t.TempDir()
	executable := filepath.Join(prefix, "bin", "qatlas.exe")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable+".old", []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &Client{Executable: executable}
	calls := 0
	c.BeforeReplace = func(context.Context, Release) error { calls++; return nil }
	if err := c.install(context.Background(), payload{executable: []byte("new-binary")}, "windows", Release{}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, executable, "new-binary")
	if calls != 1 {
		t.Errorf("BeforeReplace ran %d times", calls)
	}
	if _, err := os.Stat(filepath.Join(prefix, "share")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("share directory exists: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(executable))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".qatlas-") {
			t.Errorf("staged file remains: %s", e.Name())
		}
	}
}

func TestUpdateNoLongerRefusesWindows(t *testing.T) {
	archive := releaseArchive(t, []byte("x"), []byte("y"))
	server := releaseServer(t, "v1.1.0", archive, "")
	defer server.Close()
	_, executable := installedPrefix(t)
	c := &Client{BaseURL: server.URL, HTTPClient: server.Client(), Version: "v1.0.0", GOOS: "windows", GOARCH: "amd64", Executable: executable}
	_, err := c.Update(context.Background())
	var unsupported *UnsupportedInstallationError
	if errors.As(err, &unsupported) && strings.Contains(unsupported.Reason, "not supported yet") {
		t.Fatalf("Windows is still refused: %v", err)
	}
}

func TestConsequencesTextOnWindowsNamesRunningProcesses(t *testing.T) {
	text := ConsequencesText("windows", 2)
	for _, want := range []string{"qatlas mcp", "qatlas tui", "qatlas web", "old version", "2 other qatlas processes",
		"vault process is locked first", "no handover"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %q", want, text)
		}
	}
}

var errSharing = errors.New("sharing violation")

func sharingOps(r *opsRecorder, failures int, slept *[]time.Duration) fileOps {
	ops := r.ops()
	real := ops.rename
	n := 0
	ops.rename = func(from, to string) error {
		if filepath.Base(from) == "qatlas.exe" && n < failures {
			n++
			return errSharing
		}
		return real(from, to)
	}
	ops.transient = func(err error) bool { return errors.Is(err, errSharing) }
	ops.sleep = func(d time.Duration) { *slept = append(*slept, d) }
	return ops
}

func TestReplaceRunningRetriesTransientLock(t *testing.T) {
	staged, target := replacementFiles(t)
	var slept []time.Duration
	if err := replaceRunning(sharingOps(&opsRecorder{}, 3, &slept), staged, target); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 3 || slept[0] >= slept[2] {
		t.Errorf("waits = %v, want three growing ones", slept)
	}
	assertFile(t, target, "new")
}

func TestReplaceRunningGivesUpOnPermanentLock(t *testing.T) {
	staged, target := replacementFiles(t)
	var slept []time.Duration
	err := replaceRunning(sharingOps(&opsRecorder{}, 1<<30, &slept), staged, target)
	if !errors.Is(err, errSharing) {
		t.Fatalf("err = %v, want the sharing violation", err)
	}
	var total time.Duration
	for _, d := range slept {
		total += d
	}
	if len(slept) != len(renameDelays) || total > 3*time.Second {
		t.Errorf("waits = %v (%v)", slept, total)
	}
	assertFile(t, target, "old")
	assertFile(t, staged, "new")
}

func TestReplaceRunningDoesNotRetryOtherErrors(t *testing.T) {
	staged, target := replacementFiles(t)
	var slept []time.Duration
	r := &opsRecorder{failRename: map[string]error{"qatlas.exe>qatlas.exe.old": errors.New("other")}}
	ops := r.ops()
	ops.transient = func(err error) bool { return errors.Is(err, errSharing) }
	ops.sleep = func(d time.Duration) { slept = append(slept, d) }
	if err := replaceRunning(ops, staged, target); err == nil {
		t.Fatal("want error")
	}
	if len(slept) != 0 {
		t.Errorf("retried: %v", slept)
	}
	assertFile(t, target, "old")
}
