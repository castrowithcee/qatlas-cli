//go:build linux

package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

var ansiSequence = regexp.MustCompile(`\x1b(\[[0-9;?]*[ -/]*[@-~]|\][^\x07]*\x07)`)

// TestTUIRestartsIntoTheUpdateAndOpensTheUnlockDialog updates a running editor with an unlocked vault to a
// release served by the test: the editor replaces itself with the new program at the same path, tells
// which versions it updated between, and opens the unlock dialog; esc leaves the vault locked.
func TestTUIRestartsIntoTheUpdateAndOpensTheUnlockDialog(t *testing.T) {
	if testing.Short() {
		t.Skip("the acceptance run starts the editor on a pseudo-terminal")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// A short directory: the top line gives the path room first and the banner what is left.
	dir, err := os.MkdirTemp("/tmp", "qt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	bin := filepath.Join(dir, "prefix", "bin", "qatlas")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, body, 0o755); err != nil {
		t.Fatal(err)
	}

	// The release: a program that starts this test binary again, as the newer version.
	const latest = "v2.0.0"
	program := fmt.Sprintf("#!/bin/sh\nexport %s=v2.0.0\nexec %q \"$@\"\n", tuiVersionEnv, self)
	archiveName := fmt.Sprintf("qatlas_%s_linux_%s.tar.gz", latest, runtime.GOARCH)
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	for name, content := range map[string]string{"bin/qatlas": program, "share/man/man1/qatlas.1": ".TH QATLAS 1\n"} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(tw, content)
	}
	if err := tw.Close(); err != nil || gz.Close() != nil {
		t.Fatal("building the archive")
	}
	sum := sha256.Sum256(archive.Bytes())
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": latest, "assets": []map[string]string{
				{"name": archiveName, "browser_download_url": server.URL + "/" + archiveName},
				{"name": "checksums.txt", "browser_download_url": server.URL + "/checksums.txt"},
			}})
		case "/checksums.txt":
			_, _ = fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), archiveName)
		case "/" + archiveName:
			_, _ = w.Write(archive.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	// An encrypted vault beside the configuration.
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := vault.New(dir).Set("seed", "role", "canary-seed",
		func(string) (string, error) { return tuiPassphraseIn, nil }); err != nil {
		t.Fatalf("seeding the vault: %v", err)
	}
	runtimeDir := shortRuntimeDir(t)
	t.Cleanup(func() { killProcessesIn(runtimeDir) })

	master, slave := openTestPTY(t)
	defer master.Close()
	size := struct{ rows, cols, x, y uint16 }{30, 120, 0, 0}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSWINSZ,
		uintptr(unsafe.Pointer(&size))); errno != 0 {
		t.Fatalf("setting the terminal size: %v", errno)
	}
	cmd := exec.Command(bin, "tui")
	cmd.Env = []string{"HOME=" + dir, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color",
		"QATLAS_CONFIG=" + configPath, "XDG_RUNTIME_DIR=" + runtimeDir, secret.StoreSelector + "=none",
		runAsTUIEnv + "=1", tuiVersionEnv + "=v1.0.0", tuiReleasesEnv + "=" + server.URL}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	var mu sync.Mutex
	var raw strings.Builder
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			mu.Lock()
			raw.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	screen := func() string {
		mu.Lock()
		defer mu.Unlock()
		return ansiSequence.ReplaceAllString(raw.String(), "")
	}
	// wait looks for text written after the first from bytes of the screen.
	wait := func(from int, text string) int {
		t.Helper()
		for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			if s := screen(); from <= len(s) && strings.Contains(s[from:], text) {
				return len(s)
			}
			if time.Now().After(deadline) {
				t.Fatalf("no %q on the terminal; it shows:\n%s", text, screen())
			}
		}
	}
	typeKeys := func(s string) {
		t.Helper()
		if _, err := io.WriteString(master, s); err != nil {
			t.Fatal(err)
		}
	}

	at := wait(0, "Update available v1.0.0 → v2.0.0")
	typeKeys("\x0c") // ctrl+l unlocks the vault
	at = wait(at, "Unlock vault")
	for deadline := time.Now().Add(10 * time.Second); !echoDisabled(master); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the editor did not read the passphrase masked")
		}
	}
	typeKeys(tuiPassphraseIn + "\r")
	at = wait(at, "The vault is unlocked")

	// The editor refuses the question while the vault process is still starting; u asks again once that
	// refusal is the last thing shown, never blind. The screen is redrawn as a whole now and then, so an
	// earlier refusal may show again after the question: the last of the two decides.
	for attempt := 0; ; attempt++ {
		typeKeys("u")
		var question, refused bool
		for deadline := time.Now().Add(30 * time.Second); !question && !refused && time.Now().Before(deadline); {
			time.Sleep(20 * time.Millisecond)
			s := screen()
			if at > len(s) {
				continue
			}
			asked, wait := strings.LastIndex(s[at:], "Update qatlas to v2.0.0?"), strings.LastIndex(s[at:], "Wait until")
			question, refused = asked >= 0 && asked > wait, wait >= 0 && wait > asked
		}
		at = len(screen())
		if question {
			break
		}
		if !refused || attempt >= 50 {
			t.Fatalf("u neither asked nor was refused; the terminal shows:\n%s", screen())
		}
		time.Sleep(200 * time.Millisecond)
	}
	typeKeys("y")
	wait(at, "qatlas updated from v1.0.0 to v2.0.0")
	at = wait(at, "Unlock vault")
	if content, err := os.ReadFile(bin); err != nil || string(content) != program {
		t.Errorf("the installed program is not the release: %v", err)
	}
	typeKeys("\x1b") // esc leaves the vault locked
	wait(at, "Cancelled")
	typeKeys("q")
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the restarted editor ended with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("the restarted editor did not end on q:\n%s", screen())
	}
	if strings.Contains(screen(), tuiPassphraseIn) {
		t.Error("the passphrase reached the terminal")
	}
}

func echoDisabled(master *os.File) bool {
	var termios syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TCGETS,
		uintptr(unsafe.Pointer(&termios))); errno != 0 {
		return false
	}
	return termios.Lflag&syscall.ECHO == 0
}

func openTestPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	var unlock int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK,
		uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		_ = master.Close()
		t.Skipf("cannot unlock the pseudo-terminal: %v", errno)
	}
	var number uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN,
		uintptr(unsafe.Pointer(&number))); errno != 0 {
		_ = master.Close()
		t.Skipf("cannot name the pseudo-terminal: %v", errno)
	}
	slave, err = os.OpenFile("/dev/pts/"+strconv.FormatUint(uint64(number), 10), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		t.Skipf("cannot open the pseudo-terminal: %v", err)
	}
	return master, slave
}

// killProcessesIn ends every process whose environment names runtimeDir as its runtime directory: the
// vault process the editor started, which outlives it.
func killProcessesIn(runtimeDir string) {
	entries, _ := filepath.Glob("/proc/[0-9]*/environ")
	for _, entry := range entries {
		content, err := os.ReadFile(entry)
		if err != nil || !bytes.Contains(content, []byte("XDG_RUNTIME_DIR="+runtimeDir+"\x00")) {
			continue
		}
		if pid, err := strconv.Atoi(strings.Split(entry, "/")[2]); err == nil && pid != os.Getpid() {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}
