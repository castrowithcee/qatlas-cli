//go:build linux || darwin

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func noopHandler(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
	json.RawMessage) (any, error) {
	return map[string]any{}, nil
}

// restartRun is a server whose program reports itself as replaced on demand and whose exec is recorded.
type restartRun struct {
	t        *testing.T
	server   *mcpServer
	in       *io.PipeWriter
	out      *lockedBuffer
	stderr   *lockedBuffer
	done     chan error
	mu       sync.Mutex
	replaced bool
	execs    []execCall
	execErr  error
}

type execCall struct {
	path string
	argv []string
	env  []string
}

// executable is a program file the restart finds at the original path.
func executable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "qatlas")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func startRestartRun(t *testing.T, program string, before ...func(*mcpServer)) *restartRun {
	t.Helper()
	registry, cfg := mcpTestRegistry(t, noopHandler)
	r := &restartRun{t: t, out: &lockedBuffer{}, stderr: &lockedBuffer{}, done: make(chan error, 1),
		execErr: errors.New("exec refused")}
	r.server = newMCPServer(&Options{Config: cfg, Redactor: &redact.Redactor{}}, registry, r.out, r.stderr)
	r.server.version = "1.0.0"
	r.server.rootsWait = time.Minute
	r.server.restart = mcpRestart{
		replaced: func() (string, bool) {
			r.mu.Lock()
			defer r.mu.Unlock()
			return program, r.replaced
		},
		exec: func(path string, argv, env []string) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.execs = append(r.execs, execCall{path, argv, env})
			return r.execErr
		},
	}
	for _, f := range before {
		f(r.server)
	}
	inReader, inWriter := io.Pipe()
	r.in = inWriter
	go func() { r.done <- r.server.serve(context.Background(), inReader) }()
	t.Cleanup(func() {
		_ = inWriter.Close()
		select {
		case <-r.done:
		case <-time.After(5 * time.Second):
			t.Error("the server did not end")
		}
	})
	return r
}

func (r *restartRun) send(line string) {
	r.t.Helper()
	if _, err := io.WriteString(r.in, line); err != nil {
		r.t.Fatal(err)
	}
}

func (r *restartRun) setReplaced() {
	r.mu.Lock()
	r.replaced = true
	r.mu.Unlock()
}

func (r *restartRun) execCalls() []execCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]execCall(nil), r.execs...)
}

func (r *restartRun) waitFor(what string, ok func() bool) {
	r.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); {
		if time.Now().After(deadline) {
			r.t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func handoffOf(t *testing.T, call execCall) string {
	t.Helper()
	for _, entry := range call.env {
		if value, ok := strings.CutPrefix(entry, mcpHandoffEnv+"="); ok {
			return value
		}
	}
	t.Fatal("exec env carries no handoff")
	return ""
}

const (
	initLine = `{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":"2025-11-25",` +
		`"capabilities":{"roots":{"listChanged":true}},"clientInfo":{"name":"client","version":"7"}}}` + "\n"
	listLine = `{"jsonrpc":"2.0","id":"l","method":"tools/list","params":{}}` + "\n"
)

func TestMCPRestartHandsTheSessionOver(t *testing.T) {
	program := executable(t)
	r := startRestartRun(t, program)
	r.send(initLine)
	r.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")
	r.waitFor("roots/list", func() bool { return strings.Contains(r.out.String(), "roots/list") })
	r.send(`{"jsonrpc":"2.0","id":"qatlas-roots-1","result":{"roots":[{"uri":"file:///tmp/a"},{"uri":"file:///tmp/b"}]}}` + "\n")
	r.waitFor("roots answer", func() bool {
		r.server.rootsMu.Lock()
		defer r.server.rootsMu.Unlock()
		return len(r.server.roots.dirs) == 2
	})

	r.setReplaced()
	// The second line is read together with the first and is still unhandled when the server restarts.
	r.send(listLine + `{"jsonrpc":"2.0","id":"two","method":"ping"}` + "\n")
	r.waitFor("exec", func() bool { return len(r.execCalls()) == 1 })

	call := r.execCalls()[0]
	if call.path != program || len(call.argv) == 0 {
		t.Fatalf("exec = %q %v, want the original path with the arguments", call.path, call.argv)
	}
	value := handoffOf(t, call)
	handoff := decodeMCPHandoff(value)
	if handoff == nil {
		t.Fatalf("handoff %s was not accepted", value)
	}
	if handoff.Protocol != "2025-11-25" || handoff.Client == nil || *handoff.Client != (invokelog.ClientInfo{Name: "client", Version: "7"}) {
		t.Errorf("handoff = %+v, want protocol and client", handoff)
	}
	if !handoff.Roots.Offered || !handoff.Roots.ListChanged || handoff.Roots.Ask || len(handoff.Roots.Dirs) != 2 {
		t.Errorf("roots = %+v", handoff.Roots)
	}
	if !strings.HasPrefix(string(handoff.Input), `{"jsonrpc":"2.0","id":"l"`) ||
		!strings.HasSuffix(string(handoff.Input), `"method":"ping"}`+"\n") {
		t.Errorf("input = %q, want both unhandled lines", handoff.Input)
	}
	if !strings.Contains(r.stderr.String(), "restarting from version 1.0.0") {
		t.Errorf("stderr = %q", r.stderr.String())
	}

	// The successor takes it over and continues the session.
	registry, cfg := mcpTestRegistry(t, noopHandler)
	next := newMCPServer(&Options{Config: cfg, Redactor: &redact.Redactor{}}, registry, io.Discard, &bytes.Buffer{})
	next.adopt(handoff)
	if next.legacy != "2025-11-25" || *next.legacyClient != *handoff.Client || next.roots.sequence != 1 ||
		!next.roots.offered || !next.roots.listChanged || len(next.sessionProjects(context.Background())) != 2 {
		t.Errorf("adopted = %+v", next)
	}
}

func TestMCPRestartAsksOpenRootsAgain(t *testing.T) {
	r := startRestartRun(t, executable(t))
	r.send(initLine)
	r.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")
	r.waitFor("roots/list", func() bool { return strings.Contains(r.out.String(), "roots/list") })
	r.setReplaced()
	r.send(listLine)
	r.waitFor("exec", func() bool { return len(r.execCalls()) == 1 })
	handoff := decodeMCPHandoff(handoffOf(t, r.execCalls()[0]))
	if handoff == nil || !handoff.Roots.Ask || len(handoff.Roots.Dirs) != 0 {
		t.Fatalf("roots = %+v, want them asked again", handoff)
	}

	var out lockedBuffer
	var stderr lockedBuffer
	registry, cfg := mcpTestRegistry(t, noopHandler)
	next := newMCPServer(&Options{Config: cfg, Redactor: &redact.Redactor{}}, registry, &out, &stderr)
	next.version = "2.0.0"
	next.adopt(handoff)
	next.resume()
	if !strings.Contains(out.String(), `"method":"roots/list"`) || !strings.Contains(out.String(), "qatlas-roots-2") {
		t.Errorf("out = %q, want a fresh roots/list request", out.String())
	}
	if !strings.Contains(stderr.String(), "version 1.0.0 to 2.0.0") {
		t.Errorf("stderr = %q, want both versions", stderr.String())
	}
}

func TestMCPHandoffIsValidated(t *testing.T) {
	good := `{"v":1,"from":"1","protocol":"2025-11-25","client":{"name":"c","version":"1"},"roots":{"dirs":["/a"]}}`
	if decodeMCPHandoff(good) == nil {
		t.Fatal("a valid handoff was refused")
	}
	long := strings.Repeat("x", maxMCPRootURIBytes+1)
	manyDirs, _ := json.Marshal(make([]string, maxMCPRoots+1))
	for name, value := range map[string]string{
		"empty":           "",
		"not json":        "{",
		"unknown version": `{"v":2,"from":"1"}`,
		"no version":      `{"from":"1"}`,
		"protocol":        `{"v":1,"protocol":"1999-01-01"}`,
		"protocol type":   `{"v":1,"protocol":5}`,
		"client":          `{"v":1,"client":{"name":"","version":"1"}}`,
		"client type":     `{"v":1,"client":"c"}`,
		"sequence":        `{"v":1,"roots":{"sequence":-1}}`,
		"long dir":        `{"v":1,"roots":{"dirs":["` + long + `"]}}`,
		"many dirs":       `{"v":1,"roots":{"dirs":` + string(manyDirs) + `}}`,
		"relative dir":    `{"v":1,"roots":{"dirs":["relative/dir"]}}`,
		"dotdot dir":      `{"v":1,"roots":{"dirs":["/a/../b"]}}`,
		"unclean dir":     `{"v":1,"roots":{"dirs":["/a//b/"]}}`,
		"nul dir":         `{"v":1,"roots":{"dirs":["/a\u0000b"]}}`,
		"empty dir":       `{"v":1,"roots":{"dirs":[""]}}`,
		"control in from": `{"v":1,"from":"1\n2"}`,
		"escape in from":  `{"v":1,"from":"1\u001b[2J"}`,
		"input type":      `{"v":1,"input":5}`,
		"oversized":       `{"v":1,"from":"` + strings.Repeat("x", maxMCPHandoffBytes) + `"}`,
	} {
		if decodeMCPHandoff(value) != nil {
			t.Errorf("%s: handoff was accepted", name)
		}
	}
}

func TestTakeMCPHandoffRemovesItFromTheEnvironment(t *testing.T) {
	t.Setenv(mcpHandoffEnv, `{"v":1,"from":"1"}`)
	if takeMCPHandoff() == nil {
		t.Fatal("handoff not read")
	}
	if _, ok := os.LookupEnv(mcpHandoffEnv); ok {
		t.Error("the handoff is still in the environment")
	}
	t.Setenv(mcpHandoffEnv, `{"v":9}`)
	if takeMCPHandoff() != nil {
		t.Error("an unknown version was accepted")
	}
	if _, ok := os.LookupEnv(mcpHandoffEnv); ok {
		t.Error("the refused handoff is still in the environment")
	}
}

func TestMCPNoRestartWhileTheProgramIsNotReplaced(t *testing.T) {
	r := startRestartRun(t, executable(t))
	r.send(initLine + listLine)
	r.waitFor("both answers", func() bool { return strings.Count(r.out.String(), "\n") == 2 })
	if calls := r.execCalls(); len(calls) != 0 {
		t.Fatalf("exec called %d times", len(calls))
	}
}

func TestMCPRestartWaitsForRunningCalls(t *testing.T) {
	r := startRestartRun(t, executable(t), func(s *mcpServer) {
		s.wg.Add(1) // a tools/call that is still running
	})
	r.setReplaced()
	r.send(listLine)
	time.Sleep(100 * time.Millisecond)
	if calls := r.execCalls(); len(calls) != 0 {
		t.Fatal("exec ran while a call was still running")
	}
	if strings.Contains(r.out.String(), `"id":"l"`) {
		t.Fatal("a new message was handled while a call was still running")
	}
	r.server.wg.Done()
	r.waitFor("exec", func() bool { return len(r.execCalls()) == 1 })
}

func TestMCPFailedExecKeepsServingAndIsNotRepeated(t *testing.T) {
	r := startRestartRun(t, executable(t))
	r.setReplaced()
	r.send(listLine)
	r.waitFor("the answer", func() bool { return strings.Contains(r.out.String(), `"id":"l"`) })
	r.send(listLine)
	r.waitFor("the second answer", func() bool { return strings.Count(r.out.String(), `"id":"l"`) == 2 })
	if calls := r.execCalls(); len(calls) != 1 {
		t.Fatalf("exec called %d times, want one attempt", len(calls))
	}
	if !strings.Contains(r.stderr.String(), "restart failed, running on") {
		t.Errorf("stderr = %q", r.stderr.String())
	}
}

func TestMCPNoRestartWithoutProgramAtTheOriginalPath(t *testing.T) {
	r := startRestartRun(t, filepath.Join(t.TempDir(), "missing"))
	r.setReplaced()
	r.send(listLine)
	r.waitFor("the answer", func() bool { return strings.Contains(r.out.String(), `"id":"l"`) })
	if calls := r.execCalls(); len(calls) != 0 {
		t.Fatal("exec was tried without a program")
	}
	if !strings.Contains(r.stderr.String(), "not restarting") {
		t.Errorf("stderr = %q", r.stderr.String())
	}
}

func TestMCPRestartedServerDoesNotRestartAgainAtOnce(t *testing.T) {
	r := startRestartRun(t, executable(t), func(s *mcpServer) {
		s.adopt(&mcpHandoff{Version: mcpHandoffVersion, From: "0"})
	})
	r.setReplaced()
	r.send(listLine)
	r.waitFor("the answer", func() bool { return strings.Contains(r.out.String(), `"id":"l"`) })
	if calls := r.execCalls(); len(calls) != 0 {
		t.Fatal("a restarted server restarted again with its first message")
	}
	r.send(listLine)
	r.waitFor("exec", func() bool { return len(r.execCalls()) == 1 })
}
