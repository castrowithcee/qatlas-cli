package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/web"
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

const webAdminSyntheticPassphrase = "web-admin-synthetic-passphrase-0123"

var csrfPattern = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

// TestWebCommandAdminApprovalBrowserAndTerminal exercises 'qatlas web' end to end against a real encrypted
// synthetic vault: the coupled browser's own overview shows admin approval as not active, the masked
// browser form grants it with the right passphrase, and this process's own terminal, reading a line and
// then the passphrase through the same hidden path every other management command uses, grants it again
// after it lapses.
func TestWebCommandAdminApprovalBrowserAndTerminal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(vaultCredentialConfig), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := vault.New(dir).Encrypt(webAdminSyntheticPassphrase); err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	red := &redact.Redactor{}
	stdinR, stdinW := io.Pipe()
	t.Cleanup(func() { stdinW.Close() })
	opts := &Options{
		Redactor: red,
		Config:   configIn(dir),
		Input:    stdinR,
		Secrets: secret.NewWith(os.Getenv, secret.NewMemoryStore(),
			secret.NewFile(filepath.Join(dir, secret.FileName)), red).WithVault(vault.New(dir), vault.ReadPassphrase),
	}

	stdout := &syncBuffer{}
	var stderr bytes.Buffer
	codeCh := make(chan int, 1)
	go func() {
		codeCh <- run(newRootCommand(opts, defaultRegistry()), opts, []string{"web"}, stdout, &stderr)
	}()
	t.Cleanup(func() {
		process, err := os.FindProcess(os.Getpid())
		if err == nil && runtime.GOOS != "windows" {
			_ = process.Signal(syscall.SIGINT)
		}
		select {
		case <-codeCh:
		case <-time.After(5 * time.Second):
		}
	})

	couplingURL := waitForURL(t, stdout)

	if !strings.Contains(stdout.String(), "press enter here to approve the coupled browser") {
		t.Fatalf("no hint printed for the terminal admin path, stdout: %s", stdout.String())
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &http.Client{Jar: jar}

	resp, err := client.Get(couplingURL)
	if err != nil {
		t.Fatalf("couple: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Admin approval not active") {
		t.Fatalf("overview after coupling = %q, want admin not active", body)
	}
	csrf := csrfMatch(t, body)
	origin := couplingURL[:strings.Index(couplingURL, "/?")]
	adminURL := origin + "/admin"

	// Wrong passphrase over the browser form: generic failure, still not active.
	wrongResp, err := postAdminForm(t, client, adminURL, origin, csrf, "not-the-synthetic-passphrase")
	if err != nil {
		t.Fatalf("wrong passphrase post: %v", err)
	}
	wrongBody, _ := io.ReadAll(wrongResp.Body)
	wrongResp.Body.Close()
	if !strings.Contains(string(wrongBody), "wrong passphrase") {
		t.Fatalf("wrong passphrase body = %q, want the generic failure line", wrongBody)
	}

	// Right passphrase over the browser form grants it.
	rightResp, err := postAdminForm(t, client, adminURL, origin, csrf, webAdminSyntheticPassphrase)
	if err != nil {
		t.Fatalf("right passphrase post: %v", err)
	}
	rightBody, _ := io.ReadAll(rightResp.Body)
	rightResp.Body.Close()
	if !strings.Contains(string(rightBody), "Admin approval active") {
		t.Fatalf("overview after right passphrase = %q, want admin active", rightBody)
	}
	if strings.Contains(string(rightBody), webAdminSyntheticPassphrase) {
		t.Fatalf("overview leaked the passphrase: %s", rightBody)
	}

	// The terminal path: readVaultPassphrase is the same seam every management command already uses.
	withVaultPassphrase(t, sequencedPassphrases(webAdminSyntheticPassphrase))
	if _, err := stdinW.Write([]byte("\n")); err != nil {
		t.Fatalf("write to stdin pipe: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(stdout.String(), "admin approval granted") {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(stdout.String(), "admin approval granted") {
		t.Fatalf("terminal admin path never reported success, stdout: %s", stdout.String())
	}
	if strings.Contains(stdout.String(), webAdminSyntheticPassphrase) || strings.Contains(stderr.String(), webAdminSyntheticPassphrase) {
		t.Fatalf("synthetic passphrase leaked to process output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

// TestRunTerminalAdminWithoutCoupledSession confirms that pressing enter before any browser has coupled
// reports that plainly, without ever prompting for a passphrase: readVaultPassphrase here would panic if
// called at all.
func TestRunTerminalAdminWithoutCoupledSession(t *testing.T) {
	withVaultPassphrase(t, func(string) (string, error) {
		t.Fatal("readVaultPassphrase must not be called without a coupled session")
		return "", nil
	})

	srv, err := web.New(web.Overview{}, nil, 0)
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Run(ctx) }()

	stdinR, stdinW := io.Pipe()
	t.Cleanup(func() { stdinW.Close() })
	stdout := &syncBuffer{}
	go runTerminalAdmin(ctx, stdinR, stdout, srv)

	if _, err := stdinW.Write([]byte("\n")); err != nil {
		t.Fatalf("write to stdin pipe: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(stdout.String(), "no browser is coupled yet") {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(stdout.String(), "no browser is coupled yet") {
		t.Fatalf("terminal admin path never reported the missing session, stdout: %s", stdout.String())
	}
}

// TestRunTerminalAdminWrongPassphrase confirms a wrong passphrase on the terminal path is reported as
// exactly that, distinct from the generic refusal every other failure still gets.
func TestRunTerminalAdminWrongPassphrase(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(vaultCredentialConfig), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := vault.New(dir).Encrypt(webAdminSyntheticPassphrase); err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	srv, err := web.New(web.Overview{}, vault.New(dir), 0)
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Run(ctx) }()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &http.Client{Jar: jar}
	resp, err := client.Get(srv.URL())
	if err != nil {
		t.Fatalf("couple: %v", err)
	}
	resp.Body.Close()

	withVaultPassphrase(t, sequencedPassphrases("not-the-synthetic-passphrase"))

	stdinR, stdinW := io.Pipe()
	t.Cleanup(func() { stdinW.Close() })
	stdout := &syncBuffer{}
	go runTerminalAdmin(ctx, stdinR, stdout, srv)

	if _, err := stdinW.Write([]byte("\n")); err != nil {
		t.Fatalf("write to stdin pipe: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(stdout.String(), "wrong passphrase") {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(stdout.String(), "wrong passphrase") {
		t.Fatalf("terminal admin path never reported the wrong passphrase, stdout: %s", stdout.String())
	}
}

// postAdminForm posts the admin passphrase form the way a real browser would: with the Origin header a
// browser always sends for a cross-document form submission, which net/http's own client never adds on its
// own.
func postAdminForm(t *testing.T, client *http.Client, adminURL, origin, csrf, passphrase string) (*http.Response, error) {
	t.Helper()
	form := url.Values{"csrf": {csrf}, "passphrase": {passphrase}}.Encode()
	req, err := http.NewRequest(http.MethodPost, adminURL, strings.NewReader(form))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", origin)
	return client.Do(req)
}

func waitForURL(t *testing.T, stdout *syncBuffer) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if idx := strings.Index(stdout.String(), "http://127.0.0.1:"); idx >= 0 {
			rest := stdout.String()[idx:]
			end := strings.IndexAny(rest, " \n)")
			if end < 0 {
				end = len(rest)
			}
			return rest[:end]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("'qatlas web' never printed a local coupling URL")
	return ""
}

func csrfMatch(t *testing.T, body []byte) string {
	t.Helper()
	m := csrfPattern.FindSubmatch(body)
	if m == nil {
		t.Fatalf("no csrf hidden field found in body: %s", body)
	}
	return string(m[1])
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
