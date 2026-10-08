//go:build linux || darwin

package cli

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/castrowithcee/qatlas-cli/internal/release"
	"github.com/castrowithcee/qatlas-cli/internal/release/releasetest"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// copiedProgram copies this test binary to bin/qatlas in a directory of the test's own, which plays the
// installed qatlas: a vault process started from it, and every client of that process, runs from that path.
func copiedProgram(t *testing.T) (string, []byte) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(t.TempDir(), "bin", "qatlas")
	if err := os.MkdirAll(filepath.Dir(program), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program, content, 0o700); err != nil {
		t.Fatal(err)
	}
	return program, content
}

// testRelease writes a release whose archive holds program as bin/qatlas, signed by signer.
func testRelease(t *testing.T, program []byte, signer ssh.Signer) vaultproc.ReleaseFiles {
	t.Helper()
	var archive bytes.Buffer
	gz, _ := gzip.NewWriterLevel(&archive, gzip.BestSpeed)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "bin/qatlas", Mode: 0o755, Size: int64(len(program)),
		Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write(program)
	_ = tw.Close()
	_ = gz.Close()
	const name = "qatlas_9.9.9_test.tar.gz"
	sum := sha256.Sum256(archive.Bytes())
	checksums := []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	dir := t.TempDir()
	files := vaultproc.ReleaseFiles{Checksums: filepath.Join(dir, "checksums.txt"),
		Signature: filepath.Join(dir, "checksums.txt.sig"), Archive: filepath.Join(dir, name), ArchiveName: name}
	for path, content := range map[string][]byte{files.Checksums: checksums, files.Archive: archive.Bytes(),
		files.Signature: releasetest.Sign(t, signer, release.Namespace, "sha256", checksums)} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// startVaultServe starts 'vault serve' from program the way 'vault unlock' does, hands it snap, and waits
// until it reports ready. It returns the process and a channel closed once it ended.
func startVaultServe(t *testing.T, program, configPath string, snap vault.Snapshot, env []string) (*exec.Cmd,
	<-chan struct{}) {
	t.Helper()
	handoverRead, handoverWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	reportRead, reportWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(program, "vault", "serve", "--config", configPath)
	cmd.Env = env
	cmd.ExtraFiles = []*os.File{handoverRead, reportWrite}
	err = cmd.Start()
	_ = handoverRead.Close()
	_ = reportWrite.Close()
	if err != nil {
		t.Fatalf("cannot start the vault process: %v", err)
	}
	ended := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(ended)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-ended
	})
	data, _ := json.Marshal(snap)
	_, _ = handoverWrite.Write(data)
	_ = handoverWrite.Close()
	_ = reportRead.SetReadDeadline(time.Now().Add(vaultproc.StartTimeout))
	report, _ := bufio.NewReader(reportRead).ReadString('\n')
	_ = reportRead.Close()
	if strings.TrimSpace(report) != vaultproc.ReportReady {
		t.Fatalf("the vault process reported %q", report)
	}
	return cmd, ended
}

// readLine reads one line a helper process printed.
func readLine(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	text, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("the helper process said %q, then %v", text, err)
	}
	return strings.TrimSpace(text)
}

// vaultClientOutput runs program as a client of the vault process in mode (see runVaultClient) and returns
// the line it printed.
func vaultClientOutput(t *testing.T, program string, env []string, mode string) string {
	t.Helper()
	cmd := exec.Command(program)
	cmd.Env = append(env, vaultClientEnv+"="+mode)
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}

// An update hands the vault over: the vault process, started from the installed program, verifies the
// release itself, starts 'vault serve --successor' from the program the update put at the same path, and
// hands it the vault and the socket. The successor serves every client of the new program without a
// passphrase, until the same time; the old process ends; the handover is in the signed log.
func TestVaultProcessHandsOverToTheUpdatedProgram(t *testing.T) {
	dir := encryptedVaultFixture(t, "")
	runtimeDir := shortRuntimeDir(t)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	program, content := copiedProgram(t)
	signer := releasetest.NewSigner(t)
	files := testRelease(t, content, signer)

	scope, _ := json.Marshal(connectionScope(t, dir, "wiki"))
	filesJSON, _ := json.Marshal(files)
	base := []string{"XDG_RUNTIME_DIR=" + runtimeDir, "HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH")}
	qatlasEnv := append(append([]string{}, base...), runAsQatlasEnv+"=1",
		testReleaseKeyEnv+"="+strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))))
	clientEnv := append(append([]string{}, base...), vaultClientEnv+"_DIR="+dir,
		vaultClientEnv+"_SCOPE="+string(scope), vaultClientEnv+`_GET=["wiki-vault","token-id"]`,
		vaultClientEnv+"_RELEASE="+string(filesJSON))

	v := vault.New(dir)
	if _, err := v.Unlock("s3cret-phrase"); err != nil {
		t.Fatal(err)
	}
	snap, err := v.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	old, oldEnded := startVaultServe(t, program, configIn(dir), snap, qatlasEnv)

	before := vaultClientOutput(t, program, clientEnv, "status")
	if !strings.HasPrefix(before, "pid "+strconv.Itoa(old.Process.Pid)+" ") {
		t.Fatalf("status before the handover = %q, want process %d", before, old.Process.Pid)
	}

	// The update's side: prepare, replace the program at its path, commit.
	update := exec.Command(program)
	update.Env = append(append([]string{}, clientEnv...), vaultClientEnv+"=handover")
	stdin, err := update.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := update.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := update.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = update.Process.Kill()
		_ = update.Wait()
	})
	out := bufio.NewReader(stdout)
	if got := readLine(t, out); got != "prepared handover" {
		t.Fatalf("prepare = %q", got)
	}
	if err := os.WriteFile(program+".new", content, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(program+".new", program); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(stdin, "commit\n"); err != nil {
		t.Fatal(err)
	}
	committed := readLine(t, out)
	pidText, ok := strings.CutPrefix(committed, "committed ")
	successor, _ := strconv.Atoi(pidText)
	if !ok || successor <= 0 || successor == old.Process.Pid {
		t.Fatalf("commit = %q, want the successor's process id", committed)
	}
	t.Cleanup(func() { _ = syscall.Kill(successor, syscall.SIGKILL) })
	select {
	case <-oldEnded:
	case <-time.After(10 * time.Second):
		t.Fatalf("the old vault process still runs after the handover")
	}

	// Clients of the new program reach the successor, without a passphrase, until the old time.
	after := vaultClientOutput(t, program, clientEnv, "status")
	if want := "pid " + strconv.Itoa(successor) + " " + strings.SplitN(before, " ", 3)[2]; after != want {
		t.Fatalf("status after the handover = %q, want %q", after, want)
	}
	if got := vaultClientOutput(t, program, clientEnv, "get"); got != "value "+canaryVault {
		t.Fatalf("get from the successor = %q", got)
	}
	socket, err := vaultproc.SocketPath(v.Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{socket, socket + ".lock"} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("%s is missing after the handover: %v", filepath.Base(path), err)
		}
	}
	for _, path := range []string{vaultproc.NextPath(socket), vaultproc.NextPath(socket) + ".lock"} {
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s is left after the handover: %v", filepath.Base(path), err)
		}
	}
	if _, err := vaultproc.Listen(socket); !errors.Is(err, vaultproc.ErrRunning) {
		t.Fatalf("Listen() at the handed over socket = %v, want ErrRunning", err)
	}

	// The handover is in the log, signed, and 'vault logs verify' of the new program, which has the
	// successor check every check value, finds the log intact.
	entries, _ := logEntries(t, dir)
	handovers := 0
	for _, entry := range entries {
		if entry.Operation == "vault.handover" {
			handovers++
			if entry.Result != "success" || entry.MAC == "" {
				t.Fatalf("the handover entry = %+v, want a signed success", entry)
			}
		}
	}
	if handovers != 1 {
		t.Fatalf("the log holds %d handover entries, want 1", handovers)
	}
	verify := exec.Command(program, "vault", "logs", "verify", "--config", configIn(dir), "--output", "json")
	verify.Env = qatlasEnv
	report, err := verify.Output()
	var doc logsVerifyDocument
	if err != nil || json.Unmarshal(report, &doc) != nil || doc.Broken || doc.MACCheck != macChecked ||
		len(doc.Days) != 1 || doc.Days[0].Unchecked != 0 {
		t.Fatalf("vault logs verify = %s, %v, want an intact log with every check value checked", report, err)
	}

	lock := exec.Command(program, "vault", "lock", "--config", configIn(dir))
	lock.Env = qatlasEnv
	if out, err := lock.CombinedOutput(); err != nil {
		t.Fatalf("vault lock = %s, %v", out, err)
	}
	for deadline := time.Now().Add(5 * time.Second); syscall.Kill(successor, 0) == nil; {
		if time.Now().After(deadline) {
			t.Fatalf("the successor %d did not end when it was locked", successor)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Lstat(socket); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the socket is still there after the successor locked: %v", err)
	}
}
