//go:build linux || darwin

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	qconfig "github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider/bookstack"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// TestVaultProcessEndToEnd unlocks an encrypted vault at a pseudo-terminal the way a person does, and then
// reads its secrets from the vault process in runs that have no terminal at all: an invoke of the CLI and
// an MCP broker that was started while the vault was still locked. Locking the vault again brings back
// vault-locked, and the connection listing of both says so whenever it is locked.
func TestVaultProcessEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("the acceptance run builds the binary")
	}

	const (
		storedID     = "canary-process-id-2d71e9"
		storedSecret = "canary-process-secret-9b04c3"
		passphrase   = "canary-passphrase-58fa1e"
	)

	dir := t.TempDir()
	bin := buildBinary(t, dir)
	server := mock(t, "Token "+storedID+":"+storedSecret, []map[string]any{page(1, "Vault Runbook")}, "<p>x</p>")

	configPath := filepath.Join(dir, "config.yaml")
	config := fmt.Sprintf(`version: 1
services:
  wiki:
    provider: bookstack
    base_url: %s
credentials:
  vault-reader:
    type: vault
connections:
  wiki:
    service: wiki
    credential: vault-reader
defaults:
  connections:
    bookstack: wiki
`, server.URL)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}
	// What 'qatlas credential set' would have stored from a terminal: an encrypted vault.
	v := vault.New(dir)
	offer := func(string) (string, error) { return passphrase, nil }
	if err := v.Set("vault-reader", "token-id", storedID, offer); err != nil {
		t.Fatalf("seed token-id: %v", err)
	}
	if err := v.Set("vault-reader", "token-secret", storedSecret, offer); err != nil {
		t.Fatalf("seed token-secret: %v", err)
	}
	// What saving the connection with an unlocked vault would have approved: the connection as it is now.
	reg := capability.NewRegistry()
	if err := bookstack.Register(reg); err != nil {
		t.Fatal(err)
	}
	cfg, err := qconfig.Load(configPath, reg)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	if approved, _, err := approval.Approve(context.Background(), cfg, v, nil); err != nil || len(approved) != 1 {
		t.Fatalf("approving the connection: %v, %v", approved, err)
	}

	// The socket path must stay within what a socket address holds, which a test's own temporary
	// directory does not promise.
	runtimeDir, err := os.MkdirTemp("", "qv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })

	var seen strings.Builder
	c := &runner{bin: bin, seen: &seen, env: []string{
		"HOME=" + dir,
		"PATH=" + os.Getenv("PATH"),
		"QATLAS_CONFIG=" + configPath,
		"XDG_RUNTIME_DIR=" + runtimeDir,
		secret.StoreSelector + "=none",
	}}
	// Whatever the test did, no vault process outlives it.
	var processID int
	t.Cleanup(func() {
		_, _, _ = c.run(t, "vault", "lock")
		if processID == 0 {
			return
		}
		for deadline := time.Now().Add(5 * time.Second); syscall.Kill(processID, 0) == nil; {
			if time.Now().After(deadline) {
				_ = syscall.Kill(processID, syscall.SIGKILL)
				t.Errorf("the vault process %d did not end when it was locked", processID)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})

	broker := startBroker(t, c)

	assertLocked := func(t *testing.T) {
		t.Helper()
		code, stdout, stderr := c.run(t, "invoke", "bookstack.pages.list")
		if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "qatlas: vault-locked: ") ||
			!strings.Contains(stderr, "run 'qatlas vault unlock' in a terminal") {
			t.Errorf("invoke: exit %d, stdout %q, stderr %q; want vault-locked with the next step", code, stdout, stderr)
		}
		code, stdout, stderr = c.run(t, "connections", "--output", "json")
		if code != 0 || !strings.Contains(stdout, `"unusable":"vault-locked"`) {
			t.Errorf("connections: exit %d, stdout %q, stderr %q; want the connection unusable", code, stdout, stderr)
		}
		list := broker.call(t, `{"list":"connections"}`, "qatlas.search")
		if list.Result.IsError || !strings.Contains(string(list.Result.Structured), `"unusable":"vault-locked"`) {
			t.Errorf("MCP list = %+v, want the connection unusable", list.Result)
		}
		invoke := broker.call(t, `{"operation":"bookstack.pages.list","connection":"wiki"}`, "qatlas.invoke")
		if !invoke.Result.IsError || !strings.Contains(string(invoke.Result.Structured), `"code":"vault-locked"`) ||
			!strings.Contains(string(invoke.Result.Structured), "qatlas vault unlock") {
			t.Errorf("MCP invoke = %s, want vault-locked naming 'qatlas vault unlock'", invoke.Result.Structured)
		}
	}

	t.Run("a locked vault is reported by the CLI and the broker", assertLocked)

	t.Run("vault unlock at a terminal starts the vault process", func(t *testing.T) {
		out, code := runAtTerminal(t, c, passphrase+"\n", "vault", "unlock")
		match := regexp.MustCompile(`unlocked in a vault process \(pid (\d+)\)`).FindStringSubmatch(out)
		if code != 0 || match == nil {
			t.Fatalf("unlock: exit %d, output %q", code, out)
		}
		processID, _ = strconv.Atoi(match[1])
		if strings.Contains(out, passphrase) {
			t.Errorf("the passphrase was echoed: %q", out)
		}
	})

	t.Run("runs without a terminal read from the vault process", func(t *testing.T) {
		code, stdout, stderr := c.run(t, "invoke", "bookstack.pages.list")
		if code != 0 || !strings.Contains(stdout, "Vault Runbook") {
			t.Fatalf("invoke: exit %d, stdout %q, stderr %q", code, stdout, stderr)
		}
		code, stdout, stderr = c.run(t, "connections", "--output", "json")
		if code != 0 || strings.Contains(stdout, "unusable") {
			t.Errorf("connections: exit %d, stdout %q, stderr %q; want no unusable column", code, stdout, stderr)
		}
		// The broker was started while the vault was locked, and uses the vault process all the same.
		invoke := broker.call(t, `{"operation":"bookstack.pages.list","connection":"wiki"}`, "qatlas.invoke")
		if invoke.Result.IsError || !strings.Contains(string(invoke.Result.Structured), "Vault Runbook") {
			t.Errorf("MCP invoke = %s, want the page", invoke.Result.Structured)
		}
		list := broker.call(t, `{"list":"connections"}`, "qatlas.search")
		if list.Result.IsError || strings.Contains(string(list.Result.Structured), "unusable") {
			t.Errorf("MCP list = %s, want no unusable column", list.Result.Structured)
		}
	})

	// A copy of the binary at another path is another program: the vault process and the copy refuse each
	// other, so the copy gets no secret, and it names the vault process to end.
	t.Run("another program is refused by the vault process", func(t *testing.T) {
		data, err := os.ReadFile(bin)
		if err != nil {
			t.Fatal(err)
		}
		other := filepath.Join(dir, "other", binaryName())
		if err := os.MkdirAll(filepath.Dir(other), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(other, data, 0o700); err != nil {
			t.Fatal(err)
		}
		copied := &runner{bin: other, seen: &seen, env: c.env}
		code, stdout, stderr := copied.run(t, "invoke", "bookstack.pages.list")
		if code == 0 || strings.Contains(stdout, "Vault Runbook") ||
			!strings.Contains(stderr, "is not this qatlas of this user") ||
			!strings.Contains(stderr, fmt.Sprintf("(process %d)", processID)) {
			t.Errorf("invoke by another program: exit %d, stdout %q, stderr %q; want a refusal naming process %d",
				code, stdout, stderr, processID)
		}
	})

	// A connection changed by hand, here pointed at another server, no longer gets the secret: nothing is
	// sent there, and the CLI and the broker both say that a person has to approve the change first.
	var reached atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(elsewhere.Close)
	for _, change := range []struct{ name, from, to string }{
		{"base_url", "base_url: " + server.URL, "base_url: " + elsewhere.URL},
		{"permissions", "    credential: vault-reader\n", "    credential: vault-reader\n    permissions: [read, create]\n"},
	} {
		t.Run("a connection whose "+change.name+" was changed by hand needs approval", func(t *testing.T) {
			edited := strings.Replace(config, change.from, change.to, 1)
			if edited == config {
				t.Fatalf("the change of %s did not apply", change.name)
			}
			if err := os.WriteFile(configPath, []byte(edited), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.WriteFile(configPath, []byte(config), 0o600) })

			code, stdout, stderr := c.run(t, "invoke", "bookstack.pages.list")
			if code != 2 || stdout != "" || !strings.HasPrefix(stderr, "qatlas: approval-required: connection wiki ") ||
				!strings.Contains(stderr, "'qatlas vault approve'") {
				t.Errorf("invoke: exit %d, stdout %q, stderr %q; want approval-required naming the way out",
					code, stdout, stderr)
			}
			code, _, stderr = c.run(t, "invoke", "bookstack.pages.list", "--agent")
			if code != 2 || !strings.Contains(stderr, "an agent cannot approve it, ask the user") {
				t.Errorf("invoke --agent: exit %d, stderr %q; want the step for an agent", code, stderr)
			}
			code, stdout, stderr = c.run(t, "connections", "--output", "json")
			if code != 0 || !strings.Contains(stdout, `"unusable":"approval-required"`) {
				t.Errorf("connections: exit %d, stdout %q, stderr %q; want the connection unusable", code, stdout, stderr)
			}
			invoke := broker.call(t, `{"operation":"bookstack.pages.list","connection":"wiki"}`, "qatlas.invoke")
			if !invoke.Result.IsError || !strings.Contains(string(invoke.Result.Structured), `"code":"approval-required"`) ||
				!strings.Contains(string(invoke.Result.Structured), "an agent cannot approve it") {
				t.Errorf("MCP invoke = %s, want approval-required for an agent", invoke.Result.Structured)
			}
			list := broker.call(t, `{"list":"connections"}`, "qatlas.search")
			if list.Result.IsError || !strings.Contains(string(list.Result.Structured), `"unusable":"approval-required"`) {
				t.Errorf("MCP list = %s, want the connection unusable", list.Result.Structured)
			}
			if n := reached.Load(); n != 0 {
				t.Errorf("the changed endpoint was reached %d times", n)
			}
		})
	}

	t.Run("the approved connection works again once the change is undone", func(t *testing.T) {
		code, stdout, stderr := c.run(t, "invoke", "bookstack.pages.list")
		if code != 0 || !strings.Contains(stdout, "Vault Runbook") {
			t.Fatalf("invoke: exit %d, stdout %q, stderr %q", code, stdout, stderr)
		}
	})

	// vault approve keeps its own change in place, unlike the loop above: it approves the connection as
	// changed, not as it was before, so this runs last and does not undo the change again afterwards.
	t.Run("vault approve at a terminal releases a connection changed by hand", func(t *testing.T) {
		edited := strings.Replace(config, "    credential: vault-reader\n",
			"    credential: vault-reader\n    permissions: [read, create]\n", 1)
		if edited == config {
			t.Fatal("the permissions change did not apply")
		}
		if err := os.WriteFile(configPath, []byte(edited), 0o600); err != nil {
			t.Fatal(err)
		}

		code, stdout, stderr := c.run(t, "invoke", "bookstack.pages.list")
		if code != 2 || stdout != "" || !strings.HasPrefix(stderr, "qatlas: approval-required: connection wiki ") {
			t.Fatalf("invoke before approve: exit %d, stdout %q, stderr %q", code, stdout, stderr)
		}

		out, code := runAtTerminal(t, c, passphrase+"\n", "vault", "approve")
		if code != 0 || !strings.Contains(out, "permissions") || !strings.Contains(out, "approved 1 connection: wiki") {
			t.Fatalf("vault approve: exit %d, output %q", code, out)
		}
		if strings.Contains(out, passphrase) {
			t.Errorf("the passphrase was echoed: %q", out)
		}

		code, stdout, stderr = c.run(t, "invoke", "bookstack.pages.list")
		if code != 0 || !strings.Contains(stdout, "Vault Runbook") {
			t.Fatalf("invoke after approve: exit %d, stdout %q, stderr %q", code, stdout, stderr)
		}
		code, stdout, stderr = c.run(t, "connections", "--output", "json")
		if code != 0 || strings.Contains(stdout, "unusable") {
			t.Errorf("connections: exit %d, stdout %q, stderr %q; want no unusable column", code, stdout, stderr)
		}
	})

	t.Run("vault lock locks the vault again", func(t *testing.T) {
		code, stdout, stderr := c.run(t, "vault", "lock")
		if code != 0 || !strings.Contains(stdout, "the vault is locked") {
			t.Fatalf("lock: exit %d, stdout %q, stderr %q", code, stdout, stderr)
		}
		assertLocked(t)
	})

	broker.stop(t)
	for _, canary := range []string{storedID, storedSecret, passphrase} {
		if strings.Contains(seen.String(), canary) {
			t.Errorf("a secret reached the output: %s", canary)
		}
	}
}

// broker is a running 'qatlas mcp' that answers one request at a time.
type broker struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan string
	seen   *strings.Builder
	nextID int
}

func startBroker(t *testing.T, c *runner) *broker {
	t.Helper()
	cmd := exec.Command(c.bin, "mcp")
	cmd.Env = c.env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the broker: %v", err)
	}
	b := &broker{cmd: cmd, stdin: stdin, lines: make(chan string, 16), seen: c.seen}
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			b.lines <- scanner.Text()
		}
		close(b.lines)
	}()
	t.Cleanup(func() {
		_ = stdin.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		c.seen.WriteString(stderr.String())
	})
	return b
}

// call sends one tools/call request and returns its answer.
func (b *broker) call(t *testing.T, arguments, tool string) mcpResponse {
	t.Helper()
	b.nextID++
	request := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{%s,"name":%q,"arguments":%s}}`,
		b.nextID, mcpMeta, tool, arguments)
	if _, err := io.WriteString(b.stdin, request+"\n"); err != nil {
		t.Fatalf("writing to the broker: %v", err)
	}
	select {
	case line, ok := <-b.lines:
		if !ok {
			t.Fatalf("the broker ended")
		}
		b.seen.WriteString(line)
		var response mcpResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatalf("decoding the broker's answer %q: %v", line, err)
		}
		return response
	case <-time.After(30 * time.Second):
		t.Fatalf("the broker did not answer %s", tool)
	}
	return mcpResponse{}
}

// stop ends the broker by closing its input, the way a client does.
func (b *broker) stop(t *testing.T) {
	t.Helper()
	_ = b.stdin.Close()
	if err := b.cmd.Wait(); err != nil {
		t.Errorf("the broker ended with %v", err)
	}
}

// runAtTerminal runs the binary with a pseudo-terminal as its controlling terminal and standard streams,
// types input once the passphrase prompt appeared, and returns everything it wrote there and its exit code.
func runAtTerminal(t *testing.T, c *runner, input string, args ...string) (string, int) {
	t.Helper()
	master, slave := openPTY(t)
	defer master.Close()
	cmd := exec.Command(c.bin, args...)
	cmd.Env = c.env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting %v: %v", args, err)
	}
	_ = slave.Close()

	var mu sync.Mutex
	var out strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			mu.Lock()
			out.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				// EIO once the last process holding the terminal ended.
				return
			}
		}
	}()
	text := func() string { mu.Lock(); defer mu.Unlock(); return out.String() }

	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(text(), "passphrase: "); {
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("no passphrase prompt appeared: %q", text())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := io.WriteString(master, input); err != nil {
		t.Fatalf("typing at the terminal: %v", err)
	}
	err := cmd.Wait()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("running %v: %v", args, err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	c.seen.WriteString(text())
	return text(), code
}
