package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// mcpSession drives a server over pipes, so a test can answer the requests the server sends.
type mcpSession struct {
	t        *testing.T
	in       *io.PipeWriter
	messages chan map[string]json.RawMessage
	calls    int
}

func startMCPSession(t *testing.T, cfg string, wait time.Duration) *mcpSession {
	t.Helper()
	registry, _ := mcpTestRegistry(t, func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
		json.RawMessage) (any, error) {
		return map[string]any{}, nil
	})
	outReader, outWriter := io.Pipe()
	inReader, inWriter := io.Pipe()
	server := newMCPServer(&Options{Config: cfg, Redactor: &redact.Redactor{}}, registry, outWriter, io.Discard)
	server.rootsWait = wait
	session := &mcpSession{t: t, in: inWriter, messages: make(chan map[string]json.RawMessage, 32)}
	go func() {
		scanner := bufio.NewScanner(outReader)
		for scanner.Scan() {
			var message map[string]json.RawMessage
			if json.Unmarshal(scanner.Bytes(), &message) == nil {
				session.messages <- message
			}
		}
		close(session.messages)
	}()
	done := make(chan error, 1)
	go func() {
		done <- server.serve(context.Background(), inReader)
		_ = outWriter.Close()
	}()
	t.Cleanup(func() {
		_ = inWriter.Close()
		if err := <-done; err != nil {
			t.Errorf("serve() = %v", err)
		}
	})
	return session
}

func (s *mcpSession) send(line string) {
	s.t.Helper()
	if _, err := io.WriteString(s.in, line+"\n"); err != nil {
		s.t.Fatal(err)
	}
}

func (s *mcpSession) next() map[string]json.RawMessage {
	s.t.Helper()
	select {
	case message, ok := <-s.messages:
		if !ok {
			s.t.Fatal("server closed its output")
		}
		return message
	case <-time.After(5 * time.Second):
		s.t.Fatal("no message from the server")
	}
	return nil
}

// initialize opens a legacy session whose client declares capabilities.
func (s *mcpSession) initialize(capabilities string) {
	s.t.Helper()
	s.send(`{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":"2025-11-25",` +
		`"capabilities":` + capabilities + `,"clientInfo":{"name":"c","version":"1"}}}`)
	if response := s.next(); string(response["id"]) != `"init"` || response["result"] == nil {
		s.t.Fatalf("initialize = %v", response)
	}
	s.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
}

// rootsRequest reads the roots/list request the server sends next and returns its ID.
func (s *mcpSession) rootsRequest() json.RawMessage {
	s.t.Helper()
	request := s.next()
	var method string
	_ = json.Unmarshal(request["method"], &method)
	if method != "roots/list" || len(request["id"]) == 0 || request["result"] != nil {
		s.t.Fatalf("message = %v, want a roots/list request", request)
	}
	return request["id"]
}

// describe asks for the bound connection and returns the code of the refusal, or "" where it is offered.
// meta, where not empty, declares a per-request protocol version.
func (s *mcpSession) describe(meta string) output.Code {
	s.t.Helper()
	s.calls++
	id := fmt.Sprintf(`"call-%d"`, s.calls)
	params := `"name":"qatlas.describe","arguments":{"operation":"fake.pages.get","connection":"customer"}`
	if meta != "" {
		params = meta + "," + params
	}
	s.send(`{"jsonrpc":"2.0","id":` + id + `,"method":"tools/call","params":{` + params + `}}`)
	response := s.next()
	if string(response["id"]) != id {
		s.t.Fatalf("message = %v, want the response to %s", response, id)
	}
	result := toolResultFrom(s.t, decodedMCPResponse{ID: response["id"], Result: response["result"]})
	if !result.IsError {
		return ""
	}
	var detail struct {
		Code output.Code `json:"code"`
	}
	decodeRaw(s.t, result.Structured, &detail)
	return detail.Code
}

func rootsReply(id json.RawMessage, uris ...string) string {
	roots := make([]map[string]string, 0, len(uris))
	for _, uri := range uris {
		roots = append(roots, map[string]string{"uri": uri, "name": "root"})
	}
	encoded, _ := json.Marshal(map[string]any{"roots": roots})
	return `{"jsonrpc":"2.0","id":` + string(id) + `,"result":` + string(encoded) + `}`
}

// pathsLayout creates a bound project and a directory outside it and a configuration whose connection
// customer is bound to the project.
func pathsLayout(t *testing.T) (project, outside, cfg string) {
	t.Helper()
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	project = filepath.Join(t.TempDir(), "kunde a")
	outside = filepath.Join(t.TempDir(), "kunde-b")
	for _, dir := range []string{filepath.Join(project, "src"), outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg = writeConfig(t, fmt.Sprintf(`version: 1
services:
  fake:
    provider: fake
    base_url: https://example.invalid
credentials:
  reader:
    type: keyring
connections:
  customer:
    service: fake
    credential: reader
    paths: [%q]
defaults: {}
`, project))
	return project, outside, cfg
}

func fileURI(dir string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(dir)}).String()
}

// A session whose client offers roots runs in the projects they name, follows every change the client
// announces, and counts only the answer to the latest request.
func TestMCPRootsNameTheProjectsOfTheSession(t *testing.T) {
	project, outside, cfg := pathsLayout(t)
	t.Chdir(outside)
	session := startMCPSession(t, cfg, 5*time.Second)
	session.initialize(`{"roots":{"listChanged":true}}`)

	first := session.rootsRequest()
	projectURI := fileURI(filepath.Join(project, "src"))
	if !strings.Contains(projectURI, "%20") {
		t.Fatalf("URI %s does not exercise percent-encoding", projectURI)
	}
	session.send(rootsReply(first, "https://example.invalid/repo", projectURI))
	if code := session.describe(""); code != "" {
		t.Fatalf("describe with the project as root = %s, want the connection", code)
	}

	session.send(`{"jsonrpc":"2.0","method":"notifications/roots/list_changed"}`)
	second := session.rootsRequest()
	if string(second) == string(first) {
		t.Fatalf("second roots/list request reuses ID %s", first)
	}
	// The stale answer to the first request is dropped without a response; only the latest one counts.
	session.send(rootsReply(first, projectURI))
	session.send(rootsReply(second, fileURI(outside)))
	if code := session.describe(""); code != output.CodeUnknownConnection {
		t.Fatalf("describe with roots outside = %q, want %s", code, output.CodeUnknownConnection)
	}
}

// Wherever the client names no usable root, the session runs in the working directory: here the project.
func TestMCPWithoutRootsUsesTheWorkingDirectory(t *testing.T) {
	for _, tt := range []struct {
		name         string
		capabilities string
		answer       func(id json.RawMessage) string
	}{
		{"not offered", `{}`, nil},
		{"not an object", `{"roots":true}`, nil},
		{"empty list", `{"roots":{}}`, func(id json.RawMessage) string { return rootsReply(id) }},
		{"only other schemes", `{"roots":{}}`, func(id json.RawMessage) string {
			return rootsReply(id, "https://example.invalid/x", "file://server/share", "file:relative")
		}},
		{"error", `{"roots":{}}`, func(id json.RawMessage) string {
			return `{"jsonrpc":"2.0","id":` + string(id) + `,"error":{"code":-32601,"message":"Roots not supported"}}`
		}},
		{"malformed result", `{"roots":{}}`, func(id json.RawMessage) string {
			return `{"jsonrpc":"2.0","id":` + string(id) + `,"result":{"roots":"x"}}`
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			project, _, cfg := pathsLayout(t)
			t.Chdir(project)
			session := startMCPSession(t, cfg, 5*time.Second)
			session.initialize(tt.capabilities)
			if tt.answer != nil {
				session.send(tt.answer(session.rootsRequest()))
			}
			// describe reads the next message itself, so a roots/list request sent without an offer fails it.
			if code := session.describe(""); code != "" {
				t.Fatalf("describe = %s, want the connection of the working directory", code)
			}
		})
	}
}

// A client that does not answer in time holds no call up for longer than the wait: the call runs in the
// working directory, and a late answer applies to the calls that follow. A request that declares its own
// protocol version is served as if no session existed, in the working directory.
func TestMCPRootsThatComeLateOrDoNotApply(t *testing.T) {
	project, outside, cfg := pathsLayout(t)
	t.Chdir(project)
	session := startMCPSession(t, cfg, 50*time.Millisecond)
	session.initialize(`{"roots":{"listChanged":false}}`)
	id := session.rootsRequest()

	started := time.Now()
	if code := session.describe(""); code != "" {
		t.Fatalf("describe before the answer = %s, want the connection of the working directory", code)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("describe waited %s for roots", elapsed)
	}

	session.send(rootsReply(id, fileURI(outside)))
	if code := session.describe(""); code != output.CodeUnknownConnection {
		t.Fatalf("describe after the late answer = %q, want %s", code, output.CodeUnknownConnection)
	}
	if code := session.describe(mcpTestMeta); code != "" {
		t.Fatalf("per-request describe = %s, want the connection of the working directory", code)
	}

	// Without listChanged in the offer, a change notification asks for nothing: describe reads the next
	// message and would fail on a roots/list request.
	session.send(`{"jsonrpc":"2.0","method":"notifications/roots/list_changed"}`)
	if code := session.describe(""); code != output.CodeUnknownConnection {
		t.Fatalf("describe after an unannounced change = %q, want %s", code, output.CodeUnknownConnection)
	}
}

func TestFileURIPath(t *testing.T) {
	long := "file:///" + strings.Repeat("a", maxMCPRootURIBytes)
	for _, tt := range []struct {
		uri     string
		windows bool
		want    string
	}{
		{"file:///home/me/repo", false, "/home/me/repo"},
		{"file://localhost/home/me/a%20b", false, "/home/me/a b"},
		{"FILE://LOCALHOST/home/me/", false, "/home/me"},
		{"file:///home/me/../../../etc/./x", false, "/etc/x"},
		{"file:///C:/Users/me", false, "/C:/Users/me"},
		{"file:///C:/Users/me/repo", true, `C:\Users\me\repo`},
		{"file:///c%3A/Users/x%20y/", true, `C:\Users\x y`},
		{"file://localhost/D:/a/../../b", true, `D:\b`},
		{"file:///C:", true, `C:\`},
		{"file:///C:/", true, `C:\`},
		{"file:///home/me", true, ""},
		{"file:///Cx/a", true, ""},
		{"file:///1:/a", true, ""},
		{"file://server/share", false, ""},
		{"file://server/C:/x", true, ""},
		{"file://user@/home", false, ""},
		{"file:relative/path", false, ""},
		{"file:///a?b=1", false, ""},
		{"file:///a?", false, ""},
		{"file:///a#f", false, ""},
		{"file:///a%00b", false, ""},
		{"file:///a%zz", false, ""},
		{"https://example.invalid/a", false, ""},
		{"/home/me", false, ""},
		{"", false, ""},
		{long, false, ""},
	} {
		got, ok := fileURIPath(tt.uri, tt.windows)
		if got != tt.want || ok != (tt.want != "") {
			t.Errorf("fileURIPath(%q, windows=%v) = %q, %v; want %q", tt.uri, tt.windows, got, ok, tt.want)
		}
	}
}

func TestRootDirsReadsAtMostTheLimit(t *testing.T) {
	entries := []string{`{"uri":1}`, `"file:///x"`, `{"name":"no uri"}`}
	for i := 0; i < maxMCPRoots+5; i++ {
		entries = append(entries, fmt.Sprintf(`{"uri":"file:///r/%d"}`, i))
	}
	dirs := rootDirs(json.RawMessage(`{"roots":[`+strings.Join(entries, ",")+`]}`), false)
	if len(dirs) != maxMCPRoots-3 || dirs[0] != "/r/0" {
		t.Fatalf("dirs = %d starting %v, want %d starting /r/0", len(dirs), dirs[:1], maxMCPRoots-3)
	}
	for _, raw := range []string{`{}`, `{"roots":null}`, `{"roots":{}}`, `[]`, `null`} {
		if dirs := rootDirs(json.RawMessage(raw), false); len(dirs) != 0 {
			t.Errorf("rootDirs(%s) = %v", raw, dirs)
		}
	}
}
