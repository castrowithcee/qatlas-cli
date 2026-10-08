package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const redirectTarget = "/index.php/apps/files/outside-canary"

// redirectCase is one request path of the provider. A redirect answers the request of method at, every
// other request is answered as a healthy server would.
type redirectCase struct {
	name   string
	at     string
	exists bool // the target file exists for the stat before a write
	want   string
	run    func(t *testing.T, resolved *config.Resolved, red *redact.Redactor, dir string) error
}

func redirectCases() []redirectCase {
	invoke := func(fn func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error),
		args func(dir string) string, write bool) func(*testing.T, *config.Resolved, *redact.Redactor, string) error {
		return func(t *testing.T, resolved *config.Resolved, red *redact.Redactor, dir string) error {
			ctx := context.Background()
			if write {
				ctx = capability.WithConfirmed(ctx)
			}
			_, err := fn(ctx, resolved, resolver(red), red, json.RawMessage(args(dir)))
			return err
		}
	}
	source := func(dir string) string { return strconv.Quote(filepath.Join(dir, "in.bin")) }
	return []redirectCase{
		{"list", methodPropfind, true, "PROPFIND", invoke(invokeFilesList, func(string) string { return `{"path":""}` }, false)},
		{"stat", methodPropfind, true, "PROPFIND", invoke(invokeFilesStat, func(string) string { return `{"path":"note.txt"}` }, false)},
		{"get inline", http.MethodGet, true, "GET", invoke(invokeFilesGet, func(string) string { return `{"path":"note.txt"}` }, false)},
		{"get local_path", http.MethodGet, true, "PROPFIND GET", invoke(invokeFilesGet, func(dir string) string {
			return `{"path":"note.txt","local_path":` + strconv.Quote(filepath.Join(dir, "out.txt")) + `}`
		}, true)},
		{"create", http.MethodPut, false, "PUT", invoke(invokeFilesCreate, func(string) string { return `{"path":"note.txt","content_base64":"aGk="}` }, true)},
		{"update", http.MethodPut, true, "PUT", invoke(invokeFilesUpdate, func(string) string {
			return `{"path":"note.txt","content_base64":"aGk=","etag":"` + fileETag + `"}`
		}, true)},
		{"create from local_path", http.MethodPut, false, "PUT", invoke(invokeFilesCreate, func(dir string) string {
			return `{"path":"note.txt","local_path":` + source(dir) + `}`
		}, true)},
		{"update from local_path", http.MethodPut, true, "PUT", invoke(invokeFilesUpdate, func(dir string) string {
			return `{"path":"note.txt","local_path":` + source(dir) + `,"etag":"` + fileETag + `"}`
		}, true)},
		{"delete", http.MethodDelete, true, "PROPFIND DELETE", invoke(invokeFilesDelete, func(string) string {
			return `{"path":"note.txt","etag":"` + fileETag + `"}`
		}, true)},
		{"chunked MKCOL", "MKCOL", false, "PROPFIND MKCOL DELETE", chunked(true, source)},
		{"chunked PUT", http.MethodPut, false, "PROPFIND MKCOL PUT DELETE", chunked(true, source)},
		{"chunked MOVE", "MOVE", false, "PROPFIND MKCOL PUT PUT PUT PROPFIND MOVE DELETE", chunked(true, source)},
		{"chunked update MOVE", "MOVE", true, "PROPFIND MKCOL PUT PUT PUT PROPFIND MOVE DELETE", chunked(false, source)},
		{"connection test", methodPropfind, true, "PROPFIND", func(t *testing.T, resolved *config.Resolved, red *redact.Redactor, _ string) error {
			class, err := TestConnection(context.Background(), resolved, resolver(red), red)
			if err == nil && class != "" {
				err = &provider.Error{Class: class, Op: "test connection", Message: "class " + string(class)}
			}
			return err
		}},
	}
}

func chunked(create bool, source func(string) string) func(*testing.T, *config.Resolved, *redact.Redactor, string) error {
	return func(t *testing.T, resolved *config.Resolved, red *redact.Redactor, dir string) error {
		prevChunk, prevMax := chunkSize, maxPathUploadBytes
		chunkSize, maxPathUploadBytes = 4, 5
		t.Cleanup(func() { chunkSize, maxPathUploadBytes = prevChunk, prevMax })
		args := `{"path":"note.txt","local_path":` + source(dir)
		fn := invokeFilesCreate
		if !create {
			fn = invokeFilesUpdate
			args += `,"etag":"` + fileETag + `"`
		}
		_, err := fn(capability.WithConfirmed(context.Background()), resolved, resolver(red), red, json.RawMessage(args+`}`))
		return err
	}
}

// No request follows a redirect, whatever its status, method or step: the server is asked exactly once at
// the redirecting step, nothing is sent to the target, and the failure says neither where the redirect
// led nor that a write may have happened. The cases stand for a redirect of a read, a download, every
// write, a delete, each step of a chunked upload, and the connection test, including a redirect to a file
// outside the root.
func TestRedirectsAreNeverFollowed(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, tt := range redirectCases() {
			t.Run(strconv.Itoa(status)+" "+tt.name, func(t *testing.T) {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "in.bin"), []byte("0123456789"), 0o600); err != nil {
					t.Fatal(err)
				}
				var methods []string
				calls := serve(t, func(request *http.Request) (*http.Response, error) {
					methods = append(methods, request.Method)
					body := func(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }
					if strings.Contains(request.URL.Path, "outside-canary") {
						t.Errorf("the redirect was followed: %s %s", request.Method, request.URL)
					}
					if request.Method == tt.at {
						return &http.Response{StatusCode: status, Header: http.Header{"Location": []string{mainInstance + redirectTarget}},
							Body: body(bodyCanary)}, nil
					}
					switch request.Method {
					case methodPropfind:
						if !tt.exists {
							return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: body("")}, nil
						}
						return xmlResponse(http.StatusMultiStatus, multistatus(fileXML(aliceRoot+"/note.txt", "note.txt", "1003", "5"))), nil
					case http.MethodGet:
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: body("hello")}, nil
					}
					return &http.Response{StatusCode: http.StatusCreated, Header: http.Header{}, Body: body("")}, nil
				})
				red := &redact.Redactor{}
				resolved := localConnection(dir, dir)

				err := tt.run(t, resolved, red, dir)
				if err == nil {
					t.Fatal("the redirect was accepted")
				}
				if got := strings.Join(methods, " "); got != tt.want {
					t.Errorf("requests = %q, want %q", got, tt.want)
				}
				if classOf(err) != "provider-error" {
					t.Errorf("class = %q", classOf(err))
				}
				for _, bad := range []string{"redirect", "does not follow"} {
					if tt.name != "connection test" && !strings.Contains(err.Error(), bad) {
						t.Errorf("error %q lacks %q", err, bad)
					}
				}
				for _, canary := range []string{aliceToken, redirectTarget, "outside-canary", "cloud.example.invalid", aliceUser, bodyCanary, "stat the file", "may have been"} {
					if strings.Contains(red.Error(err), canary) {
						t.Errorf("the error carries %q: %v", canary, err)
					}
				}
				for _, c := range *calls {
					if c.url.Path == redirectTarget {
						t.Errorf("request to the redirect target: %s", c.method)
					}
				}
				if _, statErr := os.Stat(filepath.Join(dir, "out.txt")); !errors.Is(statErr, os.ErrNotExist) {
					t.Errorf("a local file exists after the redirect: %v", statErr)
				}
			})
		}
	}
}
