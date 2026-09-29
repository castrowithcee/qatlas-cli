package cli

import (
	"bytes"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const webSyntheticSecret = "WEB_TEST_SYNTHETIC_SECRET_TOKEN"

const webTestConfig = `
version: 1
services:
  wiki:
    provider: bookstack
    base_url: https://wiki.example.invalid
credentials:
  reader:
    type: env
    values:
      token-id: WEB_TEST_SYNTHETIC_SECRET_TOKEN
connections:
  wiki:
    service: wiki
    credential: reader
    description: read-only account on the team wiki
defaults:
  connections: {}
`

// syncBuffer is an io.Writer a background goroutine writes to while the test thread polls it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestWebCommandNoArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	opts := &Options{Redactor: &redact.Redactor{}}
	code := run(newRootCommand(opts, defaultRegistry()), opts, []string{"web", "extra"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitUsage, stderr.String())
	}
}

// TestWebCommandStartsCouplesAndStopsOnSignal exercises the command as a person would run it: it prints
// the local coupling URL, calls the injected opener with it, never leaks the synthetic secret the fixture
// configuration names, and a SIGINT (the same signal ctrl+c sends) stops it cleanly.
func TestWebCommandStartsCouplesAndStopsOnSignal(t *testing.T) {
	path := writeWebConfig(t)

	var openedMu sync.Mutex
	var openedURL string
	opts := &Options{Redactor: &redact.Redactor{}, Config: path}
	opts.Opener = func(url string) error {
		openedMu.Lock()
		openedURL = url
		openedMu.Unlock()
		return nil
	}

	stdout := &syncBuffer{}
	var stderr bytes.Buffer
	codeCh := make(chan int, 1)
	go func() {
		codeCh <- run(newRootCommand(opts, defaultRegistry()), opts, []string{"web"}, stdout, &stderr)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(stdout.String(), "http://127.0.0.1:") {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(stdout.String(), "http://127.0.0.1:") {
		t.Fatalf("'qatlas web' never printed a local coupling URL, stdout: %s", stdout.String())
	}

	if runtime.GOOS == "windows" {
		// os.Process.Signal(syscall.SIGINT) is not supported on Windows, so the signal-triggered stop
		// this test otherwise exercises cannot be sent here; the context-cancel stop path it shares
		// with a real SIGINT is covered platform-neutrally in internal/web/server_test.go.
		t.Skip("sending SIGINT to the current process is not supported on windows")
	}

	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("find own process: %v", err)
	}
	if err := process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}

	select {
	case code := <-codeCh:
		if code != exitOK {
			t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("'qatlas web' did not stop after SIGINT")
	}

	if strings.Contains(stdout.String(), webSyntheticSecret) || strings.Contains(stderr.String(), webSyntheticSecret) {
		t.Fatalf("synthetic secret leaked to process output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	openedMu.Lock()
	defer openedMu.Unlock()
	if !strings.Contains(openedURL, "token=") {
		t.Fatalf("opener was not called with a one-time coupling URL: %q", openedURL)
	}
}

// TestDisplayBaseURLStripsAccessValues confirms that a service base_url carrying userinfo or a query
// string never reaches the browser overview with those values intact.
func TestDisplayBaseURLStripsAccessValues(t *testing.T) {
	got := displayBaseURL("https://synthetic-user:synthetic-pass@wiki.example.invalid/path?token=synthetic-token#frag")
	if strings.Contains(got, "synthetic-user") || strings.Contains(got, "synthetic-pass") {
		t.Fatalf("displayBaseURL leaked userinfo: %q", got)
	}
	if strings.Contains(got, "synthetic-token") || strings.Contains(got, "?") {
		t.Fatalf("displayBaseURL leaked query: %q", got)
	}
	if strings.Contains(got, "#") {
		t.Fatalf("displayBaseURL leaked fragment: %q", got)
	}
	want := "https://wiki.example.invalid/path"
	if got != want {
		t.Fatalf("displayBaseURL = %q, want %q", got, want)
	}
}

// writeWebConfig writes a minimal, valid configuration whose only secret-shaped value is a synthetic
// environment variable *name*, never a secret value, and returns its path.
func writeWebConfig(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/config.yaml"
	if err := os.WriteFile(path, []byte(webTestConfig), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}
