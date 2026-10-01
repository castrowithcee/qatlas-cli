//go:build unix

package localfile

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

func TestSpecialFiles(t *testing.T) {
	tr := newTree(t)
	fifo := filepath.Join(tr.read, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("FIFOs are not available: %v", err)
	}
	_, err := OpenForUpload(context.Background(), tr.resolved(), fifo)
	checkPathError(t, err, reasonNotRegular)

	symlink(t, "fifo", filepath.Join(tr.read, "fifo-link"))
	_, err = OpenForUpload(context.Background(), tr.resolved(), filepath.Join(tr.read, "fifo-link"))
	checkPathError(t, err, reasonNotRegular)

	target := filepath.Join(tr.write, "fifo")
	if err := syscall.Mkfifo(target, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = CreateForDownload(capability.WithConfirmed(context.Background()), tr.resolved(), target)
	checkPathError(t, err, reasonNotRegular)
}

// TestUploadSwappedForFIFO swaps the file for a FIFO between its check and its open: the open does not
// block, and the check of the open handle refuses it.
func TestUploadSwappedForFIFO(t *testing.T) {
	tr := newTree(t)
	name := filepath.Join(tr.read, "file.txt")
	beforeOpen = func() {
		if err := os.Remove(name); err != nil {
			t.Error(err)
		}
		if err := syscall.Mkfifo(name, 0o600); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { beforeOpen = nil })
	_, err := OpenForUpload(context.Background(), tr.resolved(), name)
	checkPathError(t, err, reasonNotRegular)
}
