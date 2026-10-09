package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

type organiseTool struct {
	name, method, args string
	handler            func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error)
	hint               string
}

func organiseTools() []organiseTool {
	return []organiseTool{
		{"folders.create", "MKCOL", `{"path":"2026/New folder"}`, invokeFoldersCreate, "folder may have been created"},
		{"files.move", "MOVE", `{"path":"2026/a b.txt","destination":"Archive/ä.txt"}`, invokeFilesMove, "source and target"},
		{"files.copy", "COPY", `{"path":"2026/a b.txt","destination":"Archive/ä.txt"}`, invokeFilesCopy, "source and target"},
	}
}

func reportsConnection() *config.Resolved {
	return resolvedConnection("reports", "cloud-reader", aliceUserEnv, aliceTokenEnv, mainInstance, "Reports")
}

func run(tool organiseTool, args string) error {
	red := &redact.Redactor{}
	_, err := tool.handler(capability.WithConfirmed(context.Background()), reportsConnection(), resolver(red), red, json.RawMessage(args))
	return err
}

func TestOrganiseSendsOneFixedRequest(t *testing.T) {
	for _, tool := range organiseTools() {
		t.Run(tool.name, func(t *testing.T) {
			var headers http.Header
			calls := serve(t, func(r *http.Request) (*http.Response, error) {
				headers = r.Header.Clone()
				return status(201), nil
			})
			if err := run(tool, tool.args); err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 1 || (*calls)[0].method != tool.method {
				t.Fatalf("calls = %+v", *calls)
			}
			c := (*calls)[0]
			if c.auth != basicAuth(aliceUser, aliceToken) {
				t.Errorf("auth = %q", c.auth)
			}
			if tool.method == "MKCOL" {
				if c.url.EscapedPath() != aliceRoot+"/2026/New%20folder" || headers.Get("Destination") != "" {
					t.Errorf("url = %s, headers = %v", c.url, headers)
				}
				return
			}
			if c.url.EscapedPath() != aliceRoot+"/2026/a%20b.txt" {
				t.Errorf("source = %s", c.url)
			}
			want := mainInstance + aliceRoot + "/Archive/%C3%A4.txt"
			if headers.Get("Destination") != want || headers.Get("Overwrite") != "F" {
				t.Errorf("Destination = %q, Overwrite = %q, want %q", headers.Get("Destination"), headers.Get("Overwrite"), want)
			}
		})
	}
}

func TestOrganiseRefusesBadPathsBeforeSecretsAndIO(t *testing.T) {
	refuse(t)
	touched := false
	res := secret.NewWith(func(string) string { touched = true; return "x" }, nil, nil, &redact.Redactor{})
	bad := []struct{ name, src, dst string }{
		{"root source", "", "x"}, {"root destination", "x", ""},
		{"traversal source", "../x", "y"}, {"traversal destination", "x", "a/../y"},
		{"absolute source", "/x", "y"}, {"absolute destination", "x", "/y"},
		{"percent", "x", "a%2e"}, {"backslash", "x", `a\b`}, {"url", "x", "https://evil.example.invalid/y"},
		{"same", "a/b", "a/b"}, {"into itself", "a", "a/b"}, {"into a descendant", "a/b", "a/b/c/d"},
	}
	for _, tool := range organiseTools()[1:] {
		for _, b := range bad {
			args, _ := json.Marshal(map[string]string{"path": b.src, "destination": b.dst})
			_, err := tool.handler(context.Background(), reportsConnection(), res, &redact.Redactor{}, args)
			if err == nil {
				t.Errorf("%s %s accepted", tool.name, b.name)
			}
		}
	}
	for _, p := range []string{"", "../x", "/x", "a%2e", `a\b`, "a//b"} {
		args, _ := json.Marshal(map[string]string{"path": p})
		if _, err := invokeFoldersCreate(context.Background(), reportsConnection(), res, &redact.Redactor{}, args); err == nil {
			t.Errorf("folders.create accepted %q", p)
		}
	}
	if touched {
		t.Error("a secret was read")
	}
}

func TestOrganiseSiblingNamesAreNotDescendants(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) { return status(201), nil })
	if err := run(organiseTools()[1], `{"path":"a","destination":"ab/c"}`); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestOrganiseRefusalsAreClearAndOutcomesUncertain(t *testing.T) {
	for _, tool := range organiseTools() {
		for _, code := range []int{404, 405, 409, 412, 301, 307} {
			t.Run(tool.name+" clear "+http.StatusText(code), func(t *testing.T) {
				calls := serve(t, func(*http.Request) (*http.Response, error) { return status(code), nil })
				err := run(tool, tool.args)
				if err == nil || strings.Contains(err.Error(), tool.hint) || strings.Contains(err.Error(), "Archive") ||
					strings.Contains(err.Error(), bodyCanary) || len(*calls) != 1 {
					t.Errorf("err = %v, calls = %d", err, len(*calls))
				}
			})
		}
		answers := map[string]func() (*http.Response, error){
			"500":     func() (*http.Response, error) { return status(500), nil },
			"503":     func() (*http.Response, error) { return status(503), nil },
			"timeout": func() (*http.Response, error) { return nil, timeoutError{} },
			"reset":   func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		}
		for name, answer := range answers {
			t.Run(tool.name+" unclear "+name, func(t *testing.T) {
				calls := serve(t, func(*http.Request) (*http.Response, error) { return answer() })
				err := run(tool, tool.args)
				if err == nil || !strings.Contains(err.Error(), tool.hint) || len(*calls) != 1 || strings.Contains(err.Error(), bodyCanary) {
					t.Errorf("err = %v, calls = %d", err, len(*calls))
				}
			})
		}
	}
}

func TestOrganiseRefusesWithoutAFolderBeforeSecretsAndIO(t *testing.T) {
	refuse(t)
	touched := false
	res := secret.NewWith(func(string) string { touched = true; return "x" }, nil, nil, &redact.Redactor{})
	for _, tool := range organiseTools() {
		_, err := folderBound(tool.handler)(context.Background(), listed("calendar"), res, &redact.Redactor{}, json.RawMessage(tool.args))
		if err == nil || classOf(err) != provider.ClassPermission {
			t.Errorf("%s = %v, want a permission refusal", tool.name, err)
		}
	}
	if touched {
		t.Error("a secret was read")
	}
}

func TestOrganiseRisksProfileAndPermissions(t *testing.T) {
	reg := registry(t)
	want := map[string]capability.Effect{
		"nextcloud.folders.create": capability.EffectCreate, "nextcloud.files.copy": capability.EffectCreate,
		"nextcloud.files.move": capability.EffectUpdate,
	}
	for _, d := range reg.Provider(Provider) {
		effect, ok := want[d.ID]
		if !ok {
			continue
		}
		delete(want, d.ID)
		r := d.Risk
		if r.Effect != effect || r.Idempotency != capability.IdempotencyNonIdempotent || !r.OpenWorld ||
			r.Confirmation != capability.ConfirmationRequired || r.DataSensitivity != dataSensitivity ||
			d.Group != "files" || d.RequiresToolAllowList {
			t.Errorf("%s risk = %+v", d.ID, d)
		}
	}
	if len(want) != 0 {
		t.Errorf("not registered: %v", want)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	var write []string
	for _, p := range metadata.Profiles {
		if p.ID == "write" {
			write = p.Tools
		}
		if p.ID == "write" && p.Recommended {
			t.Error("write must not be recommended")
		}
	}
	exp := "nextcloud.files.list nextcloud.files.stat nextcloud.files.get nextcloud.shares.list nextcloud.shares.get nextcloud.folders.create nextcloud.files.move nextcloud.files.copy"
	if strings.Join(write, " ") != exp {
		t.Errorf("write profile = %v", write)
	}

	// Without create, no folder and no copy; without update, no move.
	cases := []struct {
		perms []config.Permission
		op    string
		args  string
	}{
		{[]config.Permission{config.PermissionRead, config.PermissionUpdate}, "nextcloud.folders.create", `{"path":"x"}`},
		{[]config.Permission{config.PermissionRead, config.PermissionUpdate}, "nextcloud.files.copy", `{"path":"x","destination":"y"}`},
		{[]config.Permission{config.PermissionRead, config.PermissionCreate}, "nextcloud.files.move", `{"path":"x","destination":"y"}`},
	}
	for _, c := range cases {
		refuse(t)
		cfg := coreConfig()
		conn := cfg.Connections["reports"]
		conn.Permissions = c.perms
		cfg.Connections["reports"] = conn
		red := &redact.Redactor{}
		core := application.New(reg, cfg, resolver(red), red)
		if _, err := core.Invoke(context.Background(), application.InvokeRequest{
			Operation: c.op, Connection: "reports", Confirmed: true, Arguments: json.RawMessage(c.args)}); err == nil {
			t.Errorf("%s was accepted without its permission", c.op)
		}
	}
}
