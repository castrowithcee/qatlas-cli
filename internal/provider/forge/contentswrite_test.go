package forge

import (
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	writeSHA   = "0123456789abcdef0123456789abcdef01234567"
	writeRoute = "/api/v1/repos/acme/widget/contents"
)

// writtenFixture is one written file as each product answers it.
func writtenFixture(p *product, path string) map[string]any {
	file := map[string]any{"name": filepath.Base(path), "path": path, "sha": writeSHA, "size": 3, "type": "file",
		"content": nil, "encoding": nil, "html_url": "https://forge.example.test/acme/widget/src/" + path,
		"_links": map[string]any{"self": "https://forge.example.test/api/v1/x", "html": "https://forge.example.test/x"}}
	if p == gitea {
		file["lfs_oid"], file["mode"], file["last_committer_date"] = "abc", "100644", "2030-01-01T00:00:00Z"
	}
	return file
}

func fileCommitFixture() map[string]any {
	return map[string]any{"sha": "c0ffee1", "message": "msg", "url": "https://forge.example.test/api/v1/c",
		"html_url": "https://forge.example.test/c", "created": "2030-01-01T00:00:00Z",
		"author":    map[string]any{"name": "Ann", "email": "ann@example.test", "date": "2030-01-01T00:00:00Z"},
		"committer": map[string]any{"name": "Ann", "email": "ann@example.test", "date": "2030-01-01T00:00:00Z"},
		"parents":   []any{map[string]any{"sha": "p1", "url": "https://forge.example.test/api/v1/p"}},
		"tree":      map[string]any{"sha": "t1"}}
}

func fileVerificationFixture() map[string]any {
	return map[string]any{"verified": true, "reason": "ok", "signature": "SIG", "payload": "PAY",
		"signer": map[string]any{"name": "Ann", "username": "ann", "email": "ann@example.test"}}
}

func contentsWriteServer(t *testing.T, p *product) func(call) *http.Response {
	return func(c call) *http.Response {
		switch {
		case c.method == http.MethodPost && c.path == writeRoute:
			return jsonResponse(201, encode(t, map[string]any{"files": []any{writtenFixture(p, "a.md"),
				writtenFixture(p, "b.md")}, "commit": fileCommitFixture(), "verification": fileVerificationFixture()}))
		case c.method == http.MethodPost && c.path == "/api/v1/repos/acme/widget/diffpatch":
			return jsonResponse(200, encode(t, map[string]any{"content": writtenFixture(p, "a.md"),
				"commit": fileCommitFixture(), "verification": fileVerificationFixture()}))
		case c.method == http.MethodDelete:
			return jsonResponse(200, encode(t, map[string]any{"content": nil, "commit": fileCommitFixture(),
				"verification": fileVerificationFixture()}))
		case strings.HasPrefix(c.path, writeRoute+"/"):
			status := 201
			if c.method == http.MethodPut {
				status = 200
			}
			return jsonResponse(status, encode(t, map[string]any{"content": writtenFixture(p, "docs/a b.md"),
				"commit": fileCommitFixture(), "verification": fileVerificationFixture()}))
		}
		return notFoundResponse()
	}
}

// newWriteEnvironment adds a connection that releases a directory for reading and one that lists the delete.
func newWriteEnvironment(t *testing.T, p *product, handler func(call) *http.Response) (*environment, string) {
	t.Helper()
	dir := t.TempDir()
	calls := []call{}
	serve(t, &calls, handler)
	reads := 0
	red := &redact.Redactor{}
	cfg := testConfig(p)
	cfg.Connections["files"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"repos/acme/widget"}, Files: config.Files{Read: []string{dir}}}
	cfg.Connections["contents-delete"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"repos/acme/widget"}, Tools: []string{p.id + ".contents.delete"}}
	return &environment{core: application.New(registry(t), cfg, resolver(red, &reads), red), red: red,
		reads: &reads, calls: &calls, p: p}, dir
}

func TestContentsCreateSendsOneCommitWithBase64AndTheNamedIdentity(t *testing.T) {
	for _, p := range products {
		e, _ := newWriteEnvironment(t, p, contentsWriteServer(t, p))
		arguments := `{"path":"docs/a b.md","content":"# Notes\n","branch":"main","new_branch":"docs/x",` +
			`"message":"docs: add\n\nbody","author":{"name":"Ann","email":"ann@example.test"},` +
			`"committer":{"name":"Bob","email":"bob@example.test"},` +
			`"dates":{"author":"2030-01-02T03:04:05+02:00"},"signoff":true}`
		if _, err := e.invokeAs(t, contentsCreate, "files", arguments, false); err == nil || len(*e.calls) != 0 {
			t.Fatalf("%s: unconfirmed create: %v", p.id, err)
		}
		result, err := e.invoke(t, contentsCreate, "files", arguments)
		if err != nil || len(*e.calls) != 1 {
			t.Fatalf("%s: %v, calls %d", p.id, err, len(*e.calls))
		}
		call := (*e.calls)[0]
		if call.method != http.MethodPost || call.escaped != writeRoute+"/docs/a%20b.md" {
			t.Errorf("%s call = %+v", p.id, call)
		}
		want := map[string]any{"content": base64.StdEncoding.EncodeToString([]byte("# Notes\n")), "branch": "main",
			"new_branch": "docs/x", "message": "docs: add\n\nbody", "signoff": true,
			"author":    map[string]any{"name": "Ann", "email": "ann@example.test"},
			"committer": map[string]any{"name": "Bob", "email": "bob@example.test"},
			"dates":     map[string]any{"author": "2030-01-02T03:04:05+02:00"}}
		if got := bodyOf(t, call); !reflect.DeepEqual(got, want) {
			t.Errorf("%s body = %v", p.id, got)
		}
		commit, _ := result["commit"].(map[string]any)
		content, _ := result["content"].(map[string]any)
		verification, _ := result["verification"].(map[string]any)
		if result["repository"] != "acme/widget" || commit["sha"] != "c0ffee1" || content["sha"] != writeSHA ||
			verification["verified"] != true || verification["signature"] != nil || verification["payload"] != nil {
			t.Errorf("%s result = %v", p.id, result)
		}
		if _, ok := content["lfs_oid"]; ok != (p == gitea) {
			t.Errorf("%s content = %v", p.id, content)
		}
		if _, ok := content["mode"]; ok != (p == gitea) {
			t.Errorf("%s content mode = %v", p.id, content)
		}
	}
}

func TestContentsCreateDefaultsToTheIdentityOfTheToken(t *testing.T) {
	for _, p := range products {
		e, _ := newWriteEnvironment(t, p, contentsWriteServer(t, p))
		if _, err := e.invoke(t, contentsCreate, "files", `{"path":"a.md","content":""}`); err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		if got := bodyOf(t, (*e.calls)[0]); !reflect.DeepEqual(got, map[string]any{"content": ""}) {
			t.Errorf("%s body = %v", p.id, got)
		}
	}
}

func TestContentsWriteReadsBinaryFilesFromTheReleasedDirectory(t *testing.T) {
	for _, p := range products {
		e, dir := newWriteEnvironment(t, p, contentsWriteServer(t, p))
		data := []byte{0, 1, 2, 0xff, 0xfe, 'x'}
		file := filepath.Join(dir, "blob.bin")
		if err := os.WriteFile(file, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := e.invoke(t, contentsCreate, "files", `{"path":"blob.bin","local_path":`+quote(file)+`}`); err != nil {
			t.Fatalf("%s create: %v", p.id, err)
		}
		if got := bodyOf(t, (*e.calls)[0]); got["content"] != base64.StdEncoding.EncodeToString(data) {
			t.Errorf("%s body = %v", p.id, got)
		}
		arguments := `{"path":"blob.bin","sha":"` + writeSHA + `","local_path":` + quote(file) + `}`
		if _, err := e.invoke(t, contentsUpdate, "files", arguments); err != nil {
			t.Fatalf("%s update: %v", p.id, err)
		}
		if (*e.calls)[1].method != http.MethodPut {
			t.Errorf("%s update call = %+v", p.id, (*e.calls)[1])
		}
		// A connection without a released directory does not offer the tool.
		if _, err := e.invoke(t, contentsCreate, "repo", `{"path":"a","content":"x"}`); err == nil {
			t.Errorf("%s: create without a released directory", p.id)
		}
	}
}

func TestContentsWriteRefusesBeforeTheSecret(t *testing.T) {
	previous := maxWriteBytes
	maxWriteBytes = 8
	t.Cleanup(func() { maxWriteBytes = previous })
	for _, p := range products {
		e, dir := newWriteEnvironment(t, p, contentsWriteServer(t, p))
		big := filepath.Join(dir, "big.bin")
		if err := os.WriteFile(big, []byte("123456789"), 0o600); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "out.bin")
		if err := os.WriteFile(outside, []byte("1"), 0o600); err != nil {
			t.Fatal(err)
		}
		cases := map[string]struct {
			tool      *tool
			arguments string
		}{
			"file over the cap":     {contentsCreate, `{"path":"a","local_path":` + quote(big) + `}`},
			"text over the cap":     {contentsCreate, `{"path":"a","content":"123456789"}`},
			"outside":               {contentsCreate, `{"path":"a","local_path":` + quote(outside) + `}`},
			"both sources":          {contentsCreate, `{"path":"a","content":"x","local_path":` + quote(big) + `}`},
			"no source":             {contentsCreate, `{"path":"a"}`},
			"batch over the cap":    {contentsChangeFiles, `{"files":[{"operation":"create","path":"a","content":"12345"},{"operation":"create","path":"b","content":"12345"}]}`},
			"update without sha":    {contentsUpdate, `{"path":"a","content":"x"}`},
			"update with bad sha":   {contentsUpdate, `{"path":"a","content":"x","sha":"zz"}`},
			"delete without sha":    {contentsDelete, `{"path":"a"}`},
			"path up":               {contentsCreate, `{"path":"a/../b","content":"x"}`},
			"path absolute":         {contentsCreate, `{"path":"/a","content":"x"}`},
			"path backslash":        {contentsCreate, `{"path":"a\\b","content":"x"}`},
			"path empty segment":    {contentsCreate, `{"path":"a//b","content":"x"}`},
			"bad branch":            {contentsCreate, `{"path":"a","content":"x","branch":"a..b"}`},
			"bad new branch":        {contentsCreate, `{"path":"a","content":"x","new_branch":"-x"}`},
			"bad email":             {contentsCreate, `{"path":"a","content":"x","author":{"name":"A","email":"not mail"}}`},
			"bad date":              {contentsCreate, `{"path":"a","content":"x","dates":{"author":"yesterday at noon"}}`},
			"foreign repository":    {contentsCreate, `{"repository":"secret-owner/hidden","path":"a","content":"x"}`},
			"empty patch":           {contentsApplyDiff, `{"content":""}`},
			"batch deletes":         {contentsChangeFiles, `{"files":[{"operation":"delete","path":"a","sha":"` + writeSHA + `"}]}`},
			"batch update no sha":   {contentsChangeFiles, `{"files":[{"operation":"update","path":"a","content":"x"}]}`},
			"batch repeated path":   {contentsChangeFiles, `{"files":[{"operation":"create","path":"a","content":"x"},{"operation":"create","path":"a","content":"y"}]}`},
			"batch create sha":      {contentsChangeFiles, `{"files":[{"operation":"create","path":"a","content":"x","sha":"` + writeSHA + `"}]}`},
			"batch from_path":       {contentsChangeFiles, `{"files":[{"operation":"create","path":"a","content":"x","from_path":"b"}]}`},
			"batch no files":        {contentsChangeFiles, `{"files":[]}`},
			"batch bad path":        {contentsChangeFiles, `{"files":[{"operation":"create","path":"../a","content":"x"}]}`},
			"unknown operation":     {contentsChangeFiles, `{"files":[{"operation":"move","path":"a","content":"x"}]}`},
			"batch no content":      {contentsChangeFiles, `{"files":[{"operation":"create","path":"a"}]}`},
			"force push":            {contentsCreate, `{"path":"a","content":"x","force_push":true}`},
			"force overwrite":       {contentsCreate, `{"path":"a","content":"x","force_overwrite_new_branch":true}`},
			"update force push":     {contentsUpdate, `{"path":"a","content":"x","sha":"` + writeSHA + `","force_push":true}`},
			"batch force push":      {contentsChangeFiles, `{"files":[{"operation":"create","path":"a","content":"x"}],"force_push":true}`},
			"patch force push":      {contentsApplyDiff, `{"content":"x","force_push":true}`},
			"patch force overwrite": {contentsApplyDiff, `{"content":"x","force_overwrite_new_branch":true}`},
			"delete force push":     {contentsDelete, `{"path":"a","sha":"` + writeSHA + `","force_push":true}`},
		}
		for name, c := range cases {
			connection := "files"
			if c.tool == contentsDelete {
				connection = "contents-delete"
			}
			_, err := e.invoke(t, c.tool, connection, c.arguments)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), dir) || strings.Contains(err.Error(), "secret-owner") ||
				strings.Contains(err.Error(), outside) {
				t.Errorf("%s %s: %v", p.id, name, err)
			}
		}
		if *e.reads != 0 || len(*e.calls) != 0 {
			t.Errorf("%s: reads %d, calls %d", p.id, *e.reads, len(*e.calls))
		}
	}
}

func TestContentsUpdateSendsTheShaAndReportsAStaleOneWithoutProviderText(t *testing.T) {
	for _, p := range products {
		for _, status := range []int{409, 422} {
			e, _ := newWriteEnvironment(t, p, func(c call) *http.Response {
				return jsonResponse(status, `{"message":"`+bodyCanary+`"}`)
			})
			_, err := e.invoke(t, contentsUpdate, "files", `{"path":"docs/a.md","sha":"`+writeSHA+`","content":"x"}`)
			if err == nil || !strings.Contains(err.Error(), "sha may be missing or stale") ||
				strings.Contains(err.Error(), "may have been applied") || len(*e.calls) != 1 {
				t.Errorf("%s %d: %v, calls %d", p.id, status, err, len(*e.calls))
			}
			call := (*e.calls)[0]
			if call.method != http.MethodPut || bodyOf(t, call)["sha"] != writeSHA {
				t.Errorf("%s call = %+v", p.id, call)
			}
		}
	}
}

func TestContentsDeleteIsListedOnlyAndSendsTheShaInTheBody(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		for _, tl := range []*tool{contentsCreate, contentsUpdate, contentsChangeFiles, contentsApplyDiff, contentsDelete} {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if !ok || d.Risk.Confirmation != "required" || d.Risk.Idempotency == "" || d.Risk.DataSensitivity == "" ||
				d.RequiresToolAllowList != (tl == contentsDelete) {
				t.Fatalf("%s %s descriptor = %+v", p.id, tl.action, d)
			}
		}
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if strings.HasPrefix(id, p.id+".contents.") && id != p.id+".contents.list" {
					t.Errorf("%s: profile %s holds %s", p.id, profile.ID, id)
				}
			}
		}
		e, _ := newWriteEnvironment(t, p, contentsWriteServer(t, p))
		arguments := `{"path":"docs/old.md","sha":"` + writeSHA + `","message":"docs: remove","branch":"main"}`
		if _, err := e.invoke(t, contentsDelete, "files", arguments); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: delete without a tools list: %v", p.id, err)
		}
		if _, err := e.invokeAs(t, contentsDelete, "contents-delete", arguments, false); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed delete: %v", p.id, err)
		}
		result, err := e.invoke(t, contentsDelete, "contents-delete", arguments)
		if err != nil || result["deleted"] != true || result["path"] != "docs/old.md" || len(*e.calls) != 1 {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		call := (*e.calls)[0]
		if call.method != http.MethodDelete || call.escaped != writeRoute+"/docs/old.md" ||
			!reflect.DeepEqual(bodyOf(t, call), map[string]any{"sha": writeSHA, "message": "docs: remove", "branch": "main"}) {
			t.Errorf("%s call = %+v", p.id, call)
		}
	}
}

func TestContentsChangeFilesSendsOneCommitAndKnowsGiteaOperations(t *testing.T) {
	for _, p := range products {
		e, dir := newWriteEnvironment(t, p, contentsWriteServer(t, p))
		file := filepath.Join(dir, "blob.bin")
		if err := os.WriteFile(file, []byte{0, 1}, 0o600); err != nil {
			t.Fatal(err)
		}
		arguments := `{"message":"batch","files":[{"operation":"create","path":"a.md","content":"A"},` +
			`{"operation":"update","path":"b.md","sha":"` + writeSHA + `","local_path":` + quote(file) + `}]}`
		result, err := e.invoke(t, contentsChangeFiles, "files", arguments)
		files, _ := result["files"].([]any)
		if err != nil || len(files) != 2 || len(*e.calls) != 1 {
			t.Fatalf("%s: %v, %v, calls %d", p.id, result, err, len(*e.calls))
		}
		call := (*e.calls)[0]
		want := map[string]any{"message": "batch", "files": []any{
			map[string]any{"operation": "create", "path": "a.md", "content": base64.StdEncoding.EncodeToString([]byte("A"))},
			map[string]any{"operation": "update", "path": "b.md", "sha": writeSHA, "content": "AAE="}}}
		if call.method != http.MethodPost || call.path != writeRoute || !reflect.DeepEqual(bodyOf(t, call), want) {
			t.Errorf("%s call = %+v", p.id, call)
		}
		// Ordered so that the rename request is the last call checked below.
		giteaOnly := []struct{ name, arguments string }{
			{"upload", `{"files":[{"operation":"upload","path":"a.bin","content":"A"}]}`},
			{"rename", `{"files":[{"operation":"rename","path":"b.md","from_path":"a.md"}]}`},
		}
		for _, c := range giteaOnly {
			name, arguments := c.name, c.arguments
			n := len(*e.calls)
			_, err := e.invoke(t, contentsChangeFiles, "files", arguments)
			if p == gitea {
				if err != nil || len(*e.calls) != n+1 {
					t.Errorf("gitea %s: %v", name, err)
				}
			} else if !isInvalidRequest(err) || len(*e.calls) != n {
				t.Errorf("forgejo %s: %v", name, err)
			}
		}
		if p == gitea {
			got := bodyOf(t, (*e.calls)[len(*e.calls)-1])["files"].([]any)[0].(map[string]any)
			if got["operation"] != "rename" || got["from_path"] != "a.md" || got["path"] != "b.md" {
				t.Errorf("rename entry = %v", got)
			}
			if _, ok := got["content"]; ok {
				t.Errorf("rename entry carries content: %v", got)
			}
			for _, bad := range []string{`{"operation":"rename","path":"b","from_path":"b"}`,
				`{"operation":"rename","path":"b"}`, `{"operation":"rename","path":"b","from_path":"../a"}`} {
				if _, err := e.invoke(t, contentsChangeFiles, "files", `{"files":[`+bad+`]}`); !isInvalidRequest(err) {
					t.Errorf("gitea rename %s: %v", bad, err)
				}
			}
		}
	}
}

func TestContentsApplyDiffSendsThePatchAsOneCommit(t *testing.T) {
	patch := "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-a\n+b\n"
	for _, p := range products {
		e, _ := newWriteEnvironment(t, p, contentsWriteServer(t, p))
		result, err := e.invoke(t, contentsApplyDiff, "repo", `{"content":`+quote(patch)+`,"new_branch":"fix/a"}`)
		if err != nil || len(*e.calls) != 1 || result["repository"] != "acme/widget" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		call := (*e.calls)[0]
		if call.method != http.MethodPost || call.path != "/api/v1/repos/acme/widget/diffpatch" ||
			!reflect.DeepEqual(bodyOf(t, call), map[string]any{"content": patch, "new_branch": "fix/a"}) {
			t.Errorf("%s call = %+v", p.id, call)
		}
	}
}

func TestContentsWriteWithAnOpenOutcomeIsNeverRepeated(t *testing.T) {
	for _, p := range products {
		for _, tl := range []*tool{contentsCreate, contentsUpdate, contentsChangeFiles, contentsApplyDiff, contentsDelete} {
			e, _ := newWriteEnvironment(t, p, func(c call) *http.Response { return jsonResponse(502, `{"message":"`+bodyCanary+`"}`) })
			arguments := map[*tool]string{
				contentsCreate:      `{"path":"a","content":"x"}`,
				contentsUpdate:      `{"path":"a","content":"x","sha":"` + writeSHA + `"}`,
				contentsChangeFiles: `{"files":[{"operation":"create","path":"a","content":"x"}]}`,
				contentsApplyDiff:   `{"content":"x"}`,
				contentsDelete:      `{"path":"a","sha":"` + writeSHA + `"}`,
			}[tl]
			connection := "files"
			if tl == contentsDelete {
				connection = "contents-delete"
			}
			_, err := e.invoke(t, tl, connection, arguments)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*e.calls) != 1 {
				t.Errorf("%s %s: %v, calls %d", p.id, tl.action, err, len(*e.calls))
			}
		}
	}
}

func TestContentsWriteSchemasOfferNeitherForceNorBatchDelete(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		for _, tl := range []*tool{contentsCreate, contentsUpdate, contentsChangeFiles, contentsApplyDiff, contentsDelete} {
			d, _, _ := reg.Lookup(p.toolID(tl))
			schema := string(d.InputSchema)
			if strings.Contains(schema, "force") {
				t.Errorf("%s %s offers a force field", p.id, tl.action)
			}
			if tl == contentsChangeFiles && (strings.Contains(schema, `"delete"`) ||
				strings.Contains(schema, `"upload"`) != (p == gitea) || strings.Contains(schema, `"rename"`) != (p == gitea)) {
				t.Errorf("%s change-files operations = %s", p.id, schema)
			}
		}
	}
}
