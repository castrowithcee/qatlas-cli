package forge

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
)

func plainResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)),
		Header: http.Header{"Content-Type": {"text/plain"}}}
}

func TestJobLogIsMaskedCleanedAndCut(t *testing.T) {
	logText := "2030-01-01T00:00:00Z start\n" +
		"\x1b[31mred " + tokenValue + " token\x1b[0m\n" +
		"bell\x07 and \x00 null\n" +
		"Authorization: token " + tokenValue + "\n" +
		"last line\n"
	for _, p := range products {
		e, _ := newActionsEnvironment(t, p, defaultVersion(p), func(c call) *http.Response {
			if c.path == actionsBase+"/jobs/7/logs" {
				return plainResponse(200, logText)
			}
			return notFoundResponse()
		})
		result, err := e.invoke(t, workflowJobLogsGet, "repo", `{"job_id":7}`)
		log, _ := result["log"].(string)
		if err != nil || strings.Contains(log, tokenValue) || !strings.Contains(log, "red [redacted] token") ||
			!strings.Contains(log, "Authorization: [redacted]") || strings.ContainsAny(log, "\x1b\x07\x00") ||
			!strings.Contains(log, "bell and  null") || result["lines"] != float64(5) || result["truncated"] != false {
			t.Fatalf("%s: %q, %v", p.id, result, err)
		}
		// Only the end is kept, from a whole line, and the mask holds across the cut.
		result, err = e.invoke(t, workflowJobLogsGet, "repo", `{"job_id":7,"lines":2}`)
		if err != nil || result["log"] != "Authorization: [redacted]\nlast line" || result["truncated"] != true {
			t.Errorf("%s lines: %v, %v", p.id, result, err)
		}
		long := strings.Repeat("0123456789abcdef\n", 4000)
		e2, _ := newActionsEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return plainResponse(200, long) })
		result, err = e2.invoke(t, workflowJobLogsGet, "repo", `{"job_id":7,"lines":1000,"max_bytes":1024}`)
		log, _ = result["log"].(string)
		if err != nil || len(log) > 1024 || result["truncated"] != true || !strings.HasPrefix(log, "0123456789abcdef") {
			t.Errorf("%s bytes: %d, %v", p.id, len(log), err)
		}
		huge := strings.Repeat("x", maxLogRead+1)
		e3, _ := newActionsEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return plainResponse(200, huge) })
		if _, err := e3.invoke(t, workflowJobLogsGet, "repo", `{"job_id":7}`); classOfError(err) != "invalid-provider-response" {
			t.Errorf("%s huge: %v", p.id, err)
		}
		e4, _ := newActionsEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return notFoundResponse() })
		for _, arguments := range []string{`{"job_id":7,"lines":0,"max_bytes":10}`, `{"job_id":7,"lines":1001}`, `{"job_id":0}`} {
			if _, err := e4.invoke(t, workflowJobLogsGet, "repo", arguments); !isInvalidRequest(err) || len(*e4.calls) != 0 {
				t.Errorf("%s %s: %v", p.id, arguments, err)
			}
		}
	}
}

func TestJobLogFiltersBelongToForgejo(t *testing.T) {
	e, _ := newActionsEnvironment(t, forgejo, "17.0.0", func(call) *http.Response { return plainResponse(200, "a\n") })
	if _, err := e.invoke(t, workflowJobLogsGet, "repo", `{"job_id":7,"attempt":2,"step":3,"q":"err","qi":true,"format":"ndjson"}`); err != nil {
		t.Fatal(err)
	}
	query := queryOf(t, apiCalls(e)[0])
	if query.Get("attempt") != "2" || query.Get("step") != "3" || query.Get("q") != "err" || query.Get("qi") != "true" ||
		query.Get("format") != "ndjson" {
		t.Errorf("query = %v", query)
	}
	for _, arguments := range []string{`{"job_id":7,"format":"xml"}`, `{"job_id":7,"step":-1}`, `{"job_id":7,"q":""}`} {
		if _, err := e.invoke(t, workflowJobLogsGet, "repo", arguments); !isInvalidRequest(err) {
			t.Errorf("%s: %v", arguments, err)
		}
	}
	g, _ := newActionsEnvironment(t, gitea, "28.2.0", func(call) *http.Response { return plainResponse(200, "a\n") })
	for _, arguments := range []string{`{"job_id":7,"attempt":1}`, `{"job_id":7,"step":1}`, `{"job_id":7,"q":"x"}`,
		`{"job_id":7,"qi":true}`, `{"job_id":7,"format":"text"}`} {
		if _, err := g.invoke(t, workflowJobLogsGet, "repo", arguments); !isInvalidRequest(err) || *g.reads != 0 {
			t.Errorf("gitea %s: %v", arguments, err)
		}
	}
}

func TestRunLogsDownloadWritesAZipWithinTheBounds(t *testing.T) {
	const archive = "PK-archive-bytes"
	previous := maxRunLogsBytes
	t.Cleanup(func() { maxRunLogsBytes = previous })
	for _, p := range products {
		version := map[string]string{forgejo.id: "16.0.6", gitea.id: "28.2.0"}[p.id]
		e, dirs := newActionsEnvironment(t, p, version, func(c call) *http.Response {
			if c.path == actionsBase+"/runs/42/logs" {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(archive)), ContentLength: -1,
					Header: http.Header{"Content-Type": {"application/zip"}}}
			}
			return notFoundResponse()
		})
		out := filepath.Join(dirs.write, "logs.zip")
		arguments := `{"run_id":42,"local_path":` + quote(out) + `}`
		result, err := e.invokeAs(t, workflowRunLogsGet, "files", arguments, false)
		sum := sha256.Sum256([]byte(archive))
		if err != nil || result["size"] != float64(len(archive)) || result["sha256"] != hex.EncodeToString(sum[:]) ||
			result["run_id"] != float64(42) || result["local_path"] != out {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if data, err := os.ReadFile(out); err != nil || string(data) != archive {
			t.Errorf("%s: file = %q, %v", p.id, data, err)
		}
		if calls := apiCalls(e); len(calls) != 1 || calls[0].auth != "token "+tokenValue {
			t.Errorf("%s calls = %v", p.id, apiMethods(e))
		}
		// An existing file is replaced only with confirmation, which is asked for before the secret.
		reads, calls := *e.reads, len(*e.calls)
		var confirm *application.ConfirmationRequiredError
		if _, err := e.invokeAs(t, workflowRunLogsGet, "files", arguments, false); !errors.As(err, &confirm) ||
			*e.reads != reads || len(*e.calls) != calls {
			t.Errorf("%s: overwrite without confirmation: %v", p.id, err)
		}
		if _, err := e.invoke(t, workflowRunLogsGet, "files", arguments); err != nil {
			t.Errorf("%s: confirmed overwrite: %v", p.id, err)
		}
		reads, calls = *e.reads, len(*e.calls)
		outside := filepath.Join(dirs.outside, "logs.zip")
		if _, err := e.invoke(t, workflowRunLogsGet, "files", `{"run_id":42,"local_path":`+quote(outside)+`}`); !isInvalidRequest(err) ||
			strings.Contains(err.Error(), dirs.outside) || *e.reads != reads || len(*e.calls) != calls {
			t.Errorf("%s: outside: %v", p.id, err)
		}
		if _, err := e.invoke(t, workflowRunLogsGet, "files", `{"run_id":42,"repository":"acme/other","local_path":`+
			quote(filepath.Join(dirs.write, "b.zip"))+`}`); !isInvalidRequest(err) || *e.reads != reads {
			t.Errorf("%s: repository outside the targets: %v", p.id, err)
		}
	}
	// A failed transfer leaves no file: redirect, refusal, server error, and a size above the limit.
	cases := map[string]func() *http.Response{
		"redirect": func() *http.Response {
			return &http.Response{StatusCode: http.StatusFound, Body: io.NopCloser(strings.NewReader("")),
				Header: http.Header{"Location": {"https://storage.example.test/signed"}}}
		},
		"refused": notFoundResponse,
		"server":  func() *http.Response { return jsonResponse(500, `{"message":"`+bodyCanary+`"}`) },
		"limit":   func() *http.Response { return plainResponse(200, strings.Repeat("z", 100)) },
	}
	for _, p := range products {
		version := map[string]string{forgejo.id: "16.0.6", gitea.id: "28.2.0"}[p.id]
		for name, answer := range cases {
			maxRunLogsBytes = previous
			if name == "limit" {
				maxRunLogsBytes = 10
			}
			e, dirs := newActionsEnvironment(t, p, version, func(c call) *http.Response {
				if strings.HasSuffix(c.path, "/logs") {
					return answer()
				}
				return notFoundResponse()
			})
			_, err := e.invoke(t, workflowRunLogsGet, "files", `{"run_id":42,"local_path":`+quote(filepath.Join(dirs.write, "l.zip"))+`}`)
			if err == nil || strings.Contains(err.Error(), "storage.example") {
				t.Errorf("%s %s: %v", p.id, name, err)
			}
			if entries, _ := os.ReadDir(dirs.write); len(entries) != 0 {
				t.Errorf("%s %s: files left: %v", p.id, name, entries)
			}
			if len(apiCalls(e)) != 1 {
				t.Errorf("%s %s: calls %v", p.id, name, apiMethods(e))
			}
		}
	}
}
