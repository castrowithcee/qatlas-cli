package nextcloud

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// failingBody ends a 2xx answer with a read error.
type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("unexpected EOF") }
func (failingBody) Close() error             { return nil }

const (
	hintStored  = "may have been stored"
	hintDeleted = "may have been deleted"
)

// A mutation with an unclear outcome names its effect, is sent exactly once and is never repeated; a clear
// refusal or a redirect carries no hint. MKCOL and chunk PUT only touch the private upload folder: their
// failure is clear and cleans the folder up.
func TestUncertainMutationOutcomes(t *testing.T) {
	type tc struct {
		name, at string
		exists   bool
		hint     string // expected hint for an unclear outcome; "" for none
		unclear  string // methods when the outcome is unclear
		clear    string // methods when the refusal is clear
	}
	cases := []tc{
		{"create", http.MethodPut, false, hintStored, "PUT", "PUT"},
		{"update", http.MethodPut, true, hintStored, "PUT", "PUT"},
		{"create from local_path", http.MethodPut, false, hintStored, "PUT", "PUT"},
		{"update from local_path", http.MethodPut, true, hintStored, "PUT", "PUT"},
		{"delete", http.MethodDelete, true, hintDeleted, "PROPFIND DELETE", "PROPFIND DELETE"},
		{"chunked MKCOL", "MKCOL", false, "", "PROPFIND MKCOL DELETE", "PROPFIND MKCOL DELETE"},
		{"chunked PUT", http.MethodPut, false, "", "PROPFIND MKCOL PUT DELETE", "PROPFIND MKCOL PUT DELETE"},
		{"chunked MOVE", "MOVE", false, hintStored, "PROPFIND MKCOL PUT PUT PUT PROPFIND MOVE", "PROPFIND MKCOL PUT PUT PUT PROPFIND MOVE DELETE"},
		{"chunked update MOVE", "MOVE", true, hintStored, "PROPFIND MKCOL PUT PUT PUT PROPFIND MOVE", "PROPFIND MKCOL PUT PUT PUT PROPFIND MOVE DELETE"},
	}
	byName := map[string]redirectCase{}
	for _, c := range redirectCases() {
		byName[c.name] = c
	}
	unclear := map[string]func() (*http.Response, error){
		"500":        func() (*http.Response, error) { return status(500), nil },
		"503":        func() (*http.Response, error) { return status(503), nil },
		"504":        func() (*http.Response, error) { return status(504), nil },
		"connection": func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"timeout":    func() (*http.Response, error) { return nil, timeoutError{} },
	}
	clear := map[string]func() (*http.Response, error){}
	for _, code := range []int{400, 403, 404, 409, 412, 301, 302, 307, 308} {
		clear[strconv.Itoa(code)] = func() (*http.Response, error) { return status(code), nil }
	}
	check := func(t *testing.T, c tc, answer func() (*http.Response, error), wantHint string, want string) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "in.bin"), []byte("0123456789"), 0o600); err != nil {
			t.Fatal(err)
		}
		var methods []string
		serve(t, func(request *http.Request) (*http.Response, error) {
			methods = append(methods, request.Method)
			if request.Method == c.at {
				return answer()
			}
			if request.Method == methodPropfind {
				if !c.exists {
					return status(404), nil
				}
				return xmlResponse(http.StatusMultiStatus, multistatus(fileXML(aliceRoot+"/note.txt", "note.txt", "1003", "5"))), nil
			}
			return status(201), nil
		})
		red := &redact.Redactor{}
		err := byName[c.name].run(t, localConnection(dir, dir), red, dir)
		if err == nil {
			t.Fatal("no error")
		}
		if got := strings.Join(methods, " "); got != want {
			t.Errorf("requests = %q, want %q", got, want)
		}
		text := err.Error()
		if strings.Contains(text, bodyCanary) {
			t.Errorf("provider text leaked: %v", err)
		}
		for _, hint := range []string{hintStored, hintDeleted} {
			if has := strings.Contains(text, hint); has != (hint == wantHint) {
				t.Errorf("hint %q present = %v in %q", hint, has, text)
			}
		}
	}
	for _, c := range cases {
		for name, answer := range unclear {
			t.Run(c.name+" unclear "+name, func(t *testing.T) { check(t, c, answer, c.hint, c.unclear) })
		}
		for name, answer := range clear {
			t.Run(c.name+" clear "+name, func(t *testing.T) { check(t, c, answer, "", c.clear) })
		}
	}
}

func status(code int) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(bodyCanary))}
}

// A 2xx answer whose body cannot be read is a success: the adapter does not read the body of a PUT, a
// MOVE or a DELETE, so nothing after the mutation can fail.
func TestUnreadableSuccessBodyIsSuccess(t *testing.T) {
	for _, name := range []string{"create", "update", "create from local_path", "delete", "chunked MOVE"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "in.bin"), []byte("0123456789"), 0o600); err != nil {
				t.Fatal(err)
			}
			c := map[string]redirectCase{}
			for _, r := range redirectCases() {
				c[r.name] = r
			}
			serve(t, func(request *http.Request) (*http.Response, error) {
				if request.Method == methodPropfind {
					if name == "create" || name == "create from local_path" || name == "chunked MOVE" {
						return status(404), nil
					}
					return xmlResponse(http.StatusMultiStatus, multistatus(fileXML(aliceRoot+"/note.txt", "note.txt", "1003", "5"))), nil
				}
				return &http.Response{StatusCode: 201, Header: http.Header{}, Body: failingBody{}}, nil
			})
			red := &redact.Redactor{}
			if err := c[name].run(t, localConnection(dir, dir), red, dir); err != nil {
				t.Errorf("err = %v", err)
			}
		})
	}
}
