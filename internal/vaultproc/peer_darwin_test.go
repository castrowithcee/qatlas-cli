//go:build darwin

package vaultproc

import (
	"encoding/binary"
	"os"
	"strings"
	"testing"
)

// procargs builds what kern.procargs2 returns for a process started by path with args.
func procargs(path string, args ...string) []byte {
	data := binary.NativeEndian.AppendUint32(nil, uint32(len(args)))
	data = append(data, path...)
	data = append(data, 0, 0, 0)
	for _, arg := range args {
		data = append(data, arg...)
		data = append(data, 0)
	}
	return data
}

func TestExecPath(t *testing.T) {
	if got, err := execPath(procargs("/usr/local/bin/qatlas", "qatlas", "vault", "serve")); err != nil ||
		got != "/usr/local/bin/qatlas" {
		t.Fatalf("execPath() of an absolute path = %q, %v", got, err)
	}
	for name, args := range map[string][]byte{
		"relative":  procargs("./qatlas", "./qatlas"),
		"bare name": procargs("qatlas", "qatlas"),
		"empty":     procargs("", "qatlas"),
		"no end":    append(binary.NativeEndian.AppendUint32(nil, 1), "/usr/local/bin/qatlas"...),
		"too short": {1, 0},
	} {
		if got, err := execPath(args); err == nil {
			t.Errorf("execPath() of %s = %q, want an error", name, got)
		}
	}
}

// The kernel reports this very process the way VerifyProgram reads its peers: by an absolute exec path
// that resolves to the program os.Executable names.
func TestProgramOfThisProcess(t *testing.T) {
	if ownProgramErr != nil {
		t.Fatalf("this program cannot be read: %v", ownProgramErr)
	}
	peer, err := programOf(os.Getpid())
	if err != nil {
		t.Fatalf("programOf(this process) error = %v", err)
	}
	if peer.path != ownProgram.path || !os.SameFile(peer.info, ownProgram.info) {
		t.Fatalf("programOf(this process) = %s, want %s", peer.path, ownProgram.path)
	}
	if strings.Contains(peer.path, "..") {
		t.Fatalf("programOf(this process) = %s, want a resolved path", peer.path)
	}
}
