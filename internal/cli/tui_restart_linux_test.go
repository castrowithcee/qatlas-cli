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

// mouseOn is the sequence that turns on the cell motion mouse mode of the terminal.
const mouseOn = "\x1b[?1002h"

// The editor asks the terminal for its background colour before it starts, an OSC 11 query followed by a
// primary device attributes query that every terminal answers. A real terminal answers both; left unanswered,
// every start would wait for Lip Gloss's timeout, once on the input and once on the output.
const (
	backgroundQuery = "\x1b]11;?"
	backgroundReply = "\x1b]11;rgb:0000/0000/0000\x1b\\\x1b[?62;c"
)

// updateRig is an editor with an unlocked-able vault running on a pseudo-terminal, beside a release server
// that offers v2.0.0 as the program it is given.
type updateRig struct {
	t       *testing.T
	bin     string
	program string
	master  *os.File
	cmd     *exec.Cmd
	mu      sync.Mutex
	raw     strings.Builder
}

// newUpdateRig starts the editor with the given content of tui.yaml (none where empty). program is the file the
// release installs; empty stands for a program that starts this test binary again, as the newer version.
func newUpdateRig(t *testing.T, program, prefs string) *updateRig {
	t.Helper()
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
	if program == "" {
		program = fmt.Sprintf("#!/bin/sh\nexport %s=v2.0.0\nexec %q \"$@\"\n", tuiVersionEnv, self)
	}
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
	if err := os.WriteFile(configPath, []byte("version: 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if prefs != "" {
		if err := os.WriteFile(filepath.Join(dir, "tui.yaml"), []byte(prefs), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := vault.New(dir).Set("seed", "role", "canary-seed",
		func(string) (string, error) { return tuiPassphraseIn, nil }); err != nil {
		t.Fatalf("seeding the vault: %v", err)
	}
	runtimeDir := shortRuntimeDir(t)
	t.Cleanup(func() { killProcessesIn(runtimeDir) })

	master, slave := openTestPTY(t)
	t.Cleanup(func() { _ = master.Close() })
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

	r := &updateRig{t: t, bin: bin, program: program, master: master, cmd: cmd}
	go func() {
		buf := make([]byte, 4096)
		answered := 0
		for {
			n, err := master.Read(buf)
			r.mu.Lock()
			r.raw.Write(buf[:n])
			queries := strings.Count(r.raw.String(), backgroundQuery)
			r.mu.Unlock()
			for ; answered < queries; answered++ {
				_, _ = io.WriteString(master, backgroundReply)
			}
			if err != nil {
				return
			}
		}
	}()
	return r
}

// bytes is everything the editor wrote to the terminal so far, escape sequences included.
func (r *updateRig) bytes() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.raw.String()
}

func (r *updateRig) screen() string { return ansiSequence.ReplaceAllString(r.bytes(), "") }

// wait looks for text written after the first from bytes of the screen.
func (r *updateRig) wait(from int, text string) int {
	r.t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if s := r.screen(); from <= len(s) && strings.Contains(s[from:], text) {
			return len(s)
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("no %q on the terminal; it shows:\n%s", text, r.screen())
		}
	}
}

func (r *updateRig) typeKeys(s string) {
	r.t.Helper()
	if _, err := io.WriteString(r.master, s); err != nil {
		r.t.Fatal(err)
	}
}

// startUpdate unlocks the vault, asks for the update and answers y, and returns where the screen stood then.
func (r *updateRig) startUpdate() int {
	t := r.t
	t.Helper()
	at := r.wait(0, "Update available v1.0.0 → v2.0.0")
	r.typeKeys("\x0c") // ctrl+l unlocks the vault
	at = r.wait(at, "Unlock vault")
	for deadline := time.Now().Add(10 * time.Second); !echoDisabled(r.master); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the editor did not read the passphrase masked")
		}
	}
	r.typeKeys(tuiPassphraseIn + "\r")
	at = r.wait(at, "The vault is unlocked")

	// The editor refuses the question while the vault process is still starting; u asks again once that
	// refusal is the last thing shown, never blind. The screen is redrawn as a whole now and then, so an
	// earlier refusal may show again after the question: the last of the two decides. The next attempt
	// continues where the deciding look ended, not where the screen stands by then: the answer to the
	// last u may already be in between.
	for attempt := 0; ; attempt++ {
		r.typeKeys("u")
		var question, refused bool
		for deadline := time.Now().Add(30 * time.Second); !question && !refused && time.Now().Before(deadline); {
			time.Sleep(20 * time.Millisecond)
			s := r.screen()
			if at > len(s) {
				continue
			}
			asked, wait := strings.LastIndex(s[at:], "Update qatlas to v2.0.0?"), strings.LastIndex(s[at:], "Wait until")
			question, refused = asked >= 0 && asked > wait, wait >= 0 && wait > asked
			if question || refused {
				at = len(s)
			}
		}
		if question {
			break
		}
		if !refused || attempt >= 50 {
			t.Fatalf("u neither asked nor was refused; the terminal shows:\n%s", r.screen())
		}
		time.Sleep(200 * time.Millisecond)
	}
	r.typeKeys("y")
	return at
}

// TestTUIRestartsIntoTheUpdateAndOpensTheUnlockDialog updates a running editor with an unlocked vault to a
// release served by the test: the editor replaces itself with the new program at the same path, tells
// which versions it updated between, and opens the unlock dialog; esc leaves the vault locked.
func TestTUIRestartsIntoTheUpdateAndOpensTheUnlockDialog(t *testing.T) {
	if testing.Short() {
		t.Skip("the acceptance run starts the editor on a pseudo-terminal")
	}
	r := newUpdateRig(t, "", "")
	at := r.startUpdate()
	r.wait(at, "qatlas updated from v1.0.0 to v2.0.0")
	at = r.wait(at, "Unlock vault")
	if content, err := os.ReadFile(r.bin); err != nil || string(content) != r.program {
		t.Errorf("the installed program is not the release: %v", err)
	}
	r.typeKeys("\x1b") // esc leaves the vault locked
	r.wait(at, "Cancelled")
	r.typeKeys("q")
	done := make(chan error, 1)
	go func() { done <- r.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the restarted editor ended with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("the restarted editor did not end on q:\n%s", r.screen())
	}
	if strings.Contains(r.screen(), tuiPassphraseIn) {
		t.Error("the passphrase reached the terminal")
	}
	// Both runs of the editor took the mouse, as the default says.
	if got := strings.Count(r.bytes(), mouseOn); got < 2 {
		t.Errorf("the mouse mode was turned on %d times, want once per run", got)
	}
}

// The mouse mode is on by default and off where tui.yaml says so: the terminal is told only what the
// preference allows.
func TestTUITakesTheMouseOnlyWhenThePreferenceAllowsIt(t *testing.T) {
	if testing.Short() {
		t.Skip("the acceptance run starts the editor on a pseudo-terminal")
	}
	for _, c := range []struct {
		name, prefs string
		want        bool
	}{
		{"default", "", true},
		{"on", "mouse: true\n", true},
		{"off", "mouse: false\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newUpdateRig(t, "", c.prefs)
			r.wait(0, "Update available v1.0.0 → v2.0.0")
			if got := strings.Contains(r.bytes(), mouseOn); got != c.want {
				t.Errorf("mouse mode sequence present = %v, want %v", got, c.want)
			}
			r.typeKeys("q")
			done := make(chan error, 1)
			go func() { done <- r.cmd.Wait() }()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatalf("the editor did not end on q:\n%s", r.screen())
			}
		})
	}
}

// A restart that fails leaves the editor running on the terminal it had released; restoring it, Bubble Tea
// turns the mouse mode of the last view on again.
func TestTUIKeepsTheMouseAfterAFailedRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("the acceptance run starts the editor on a pseudo-terminal")
	}
	for _, c := range []struct {
		name, prefs string
		want        int
	}{
		{"mouse on", "", 2},
		{"mouse off", "mouse: false\n", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			// A program whose interpreter does not exist: it is a regular executable file, so the editor tries
			// the replacement, and the replacement fails.
			r := newUpdateRig(t, "#!/nonexistent/qatlas-interpreter\n", c.prefs)
			at := r.startUpdate()
			r.wait(at, "restart failed")
			// The failure is shown once the terminal is the editor's again; the mouse follows with it.
			deadline := time.Now().Add(5 * time.Second)
			for strings.Count(r.bytes(), mouseOn) < c.want && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if got := strings.Count(r.bytes(), mouseOn); got != c.want {
				t.Errorf("the mouse mode was turned on %d times, want %d", got, c.want)
			}
			r.typeKeys("q")
		})
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
