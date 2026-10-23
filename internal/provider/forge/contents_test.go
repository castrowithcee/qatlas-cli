package forge

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
)

const contentBase = "/api/v1/repos/acme/widget"

// contentRow is one contents entry as the product answers it.
func contentRow(p *product, name, kind string, content []byte) map[string]any {
	row := map[string]any{"name": name, "path": "docs/" + name, "sha": "abc123", "type": kind, "size": len(content),
		"url":             "https://" + apiHost + "/api/v1/repos/acme/widget/contents/docs/" + name,
		"html_url":        "https://" + apiHost + "/acme/widget/src/branch/main/docs/" + name,
		"git_url":         "https://" + apiHost + "/api/v1/repos/acme/widget/git/blobs/abc123",
		"download_url":    "https://" + apiHost + "/acme/widget/raw/branch/main/docs/" + name,
		"_links":          map[string]any{"self": "https://" + apiHost + "/self", "git": "https://" + apiHost + "/git", "html": "https://" + apiHost + "/html"},
		"last_commit_sha": "c0ffee1"}
	if kind == "file" {
		row["content"], row["encoding"] = base64.StdEncoding.EncodeToString(content), "base64"
	}
	if p == forgejo {
		row["last_commit_when"] = "2030-01-02T03:04:05Z"
	} else {
		row["last_author_date"], row["last_committer_date"] = "2030-01-02T03:04:05Z", "2030-01-02T03:04:06Z"
		row["last_commit_message"], row["lfs_oid"], row["lfs_size"], row["mode"] = "fix", "oid1", 7, "100644"
	}
	return row
}

func contentServer(t *testing.T, p *product, files map[string][]byte) func(call) *http.Response {
	return func(c call) *http.Response {
		switch {
		case c.method == http.MethodGet && c.path == contentBase+"/contents":
			return jsonResponse(200, encode(t, []any{contentRow(p, "a.md", "file", []byte("a")),
				contentRow(p, "sub", "dir", nil)}))
		case c.method == http.MethodGet && strings.HasPrefix(c.path, contentBase+"/contents/"):
			name := strings.TrimPrefix(c.path, contentBase+"/contents/")
			if name == "docs" {
				return jsonResponse(200, encode(t, []any{contentRow(p, "a.md", "file", []byte("a"))}))
			}
			if data, ok := files[name]; ok {
				return jsonResponse(200, encode(t, contentRow(p, filepath.Base(name), "file", data)))
			}
		case c.method == http.MethodGet && strings.HasPrefix(c.path, contentBase+"/contents-ext/"):
			name := strings.TrimPrefix(c.path, contentBase+"/contents-ext/")
			if name == "docs" {
				return jsonResponse(200, encode(t, map[string]any{"dir_contents": []any{contentRow(p, "a.md", "file", nil)}}))
			}
			return jsonResponse(200, encode(t, map[string]any{"file_contents": contentRow(p, filepath.Base(name), "file", files[name])}))
		case c.method == http.MethodPost && c.path == contentBase+"/file-contents":
			var body struct{ Files []string }
			_ = json.Unmarshal([]byte(c.body), &body)
			rows := []any{}
			for _, name := range body.Files {
				rows = append(rows, contentRow(p, filepath.Base(name), "file", files[name]))
			}
			return jsonResponse(200, encode(t, rows))
		case c.method == http.MethodGet && strings.HasPrefix(c.path, contentBase+"/raw/"),
			c.method == http.MethodGet && strings.HasPrefix(c.path, contentBase+"/media/"):
			name := c.path[strings.Index(c.path[len(contentBase)+1:], "/")+len(contentBase)+2:]
			if data, ok := files[name]; ok {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data))),
					Header: http.Header{"Content-Length": {strconv.Itoa(len(data))}}, ContentLength: int64(len(data))}
			}
		case c.method == http.MethodGet && strings.HasPrefix(c.path, contentBase+"/archive/"):
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ZIPDATA")), ContentLength: 7}
		case c.method == http.MethodGet && strings.HasPrefix(c.path, contentBase+"/editorconfig/"):
			if p == gitea {
				return jsonResponse(200, "")
			}
			return jsonResponse(200, `{"indent_style":"tab","indent_size":"4"}`)
		}
		return notFoundResponse()
	}
}

func TestContentsReadShowsEveryMemberAndDecodesText(t *testing.T) {
	files := map[string][]byte{"docs/my file.md": []byte("héllo"), "bin.dat": {0, 1, 2, 0xff}}
	for _, p := range products {
		e := newEnvironment(t, p, contentServer(t, p, files))
		result, err := e.invoke(t, contentsGet, "repo", `{"path":"docs/my file.md","ref":"feature/x"}`)
		entry, _ := result["entry"].(map[string]any)
		if err != nil || entry["content"] != "héllo" || entry["encoding"] != "base64" || entry["sha"] != "abc123" ||
			entry["type"] != "file" || result["repository"] != "acme/widget" || result["path"] != "docs/my file.md" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		call := (*e.calls)[0]
		if call.escaped != contentBase+"/contents/docs/my%20file.md" || call.query != "ref=feature%2Fx" {
			t.Errorf("%s call = %+v", p.id, call)
		}
		if _, ok := entry["_links"].(map[string]any)["self"]; !ok {
			t.Errorf("%s: links missing: %v", p.id, entry)
		}
		_, forgejoOnly := entry["last_commit_when"]
		_, giteaOnlyDate := entry["last_committer_date"]
		_, lfs := entry["lfs_oid"]
		_, mode := entry["mode"]
		if forgejoOnly != (p == forgejo) || giteaOnlyDate != (p == gitea) || lfs != (p == gitea) || mode != (p == gitea) {
			t.Errorf("%s members = %v", p.id, entry)
		}
		result, err = e.invoke(t, contentsGet, "repo", `{"path":"bin.dat"}`)
		entry, _ = result["entry"].(map[string]any)
		if err != nil || entry["binary"] != true || entry["size"] != float64(4) || entry["sha"] != "abc123" {
			t.Fatalf("%s binary: %v, %v", p.id, result, err)
		}
		if _, ok := entry["content"]; ok {
			t.Errorf("%s: binary content returned", p.id)
		}
		result, err = e.invoke(t, contentsGet, "repo", `{"path":"docs"}`)
		if entries, _ := result["entries"].([]any); err != nil || len(entries) != 1 || result["total_entries"] != float64(1) {
			t.Fatalf("%s dir: %v, %v", p.id, result, err)
		}
		result, err = e.invoke(t, contentsList, "repo", `{}`)
		if entries, _ := result["entries"].([]any); err != nil || len(entries) != 2 {
			t.Fatalf("%s root: %v, %v", p.id, result, err)
		}
	}
}

func TestContentsAreCapped(t *testing.T) {
	big := []byte(strings.Repeat("é", maxContentBytes))
	for _, p := range products {
		many := []any{}
		for i := 0; i < maxEntries+5; i++ {
			many = append(many, contentRow(p, "f"+strconv.Itoa(i), "file", []byte("x")))
		}
		e := newEnvironment(t, p, func(c call) *http.Response {
			if c.path == contentBase+"/contents" {
				return jsonResponse(200, encode(t, many))
			}
			return contentServer(t, p, map[string][]byte{"big.txt": big})(c)
		})
		result, err := e.invoke(t, contentsGet, "repo", `{"path":"big.txt"}`)
		entry, _ := result["entry"].(map[string]any)
		content, _ := entry["content"].(string)
		if err != nil || entry["content_truncated"] != true || len(content) > maxContentBytes || len(content) < maxContentBytes-3 {
			t.Fatalf("%s: %v (%d)", p.id, err, len(content))
		}
		result, err = e.invoke(t, contentsList, "repo", `{}`)
		if entries, _ := result["entries"].([]any); err != nil || len(entries) != maxEntries || result["truncated"] != true ||
			result["total_entries"] != float64(maxEntries+5) {
			t.Fatalf("%s list: %v", p.id, err)
		}
	}
}

func TestContentPathsAreCheckedBeforeAnyRequest(t *testing.T) {
	bad := []string{"", "../x", "a/../b", "/abs", "a//b", "a/", "a/./b", `a\b`, "a\u0000b", "a\nb", "."}
	for _, p := range products {
		e, dirs := newAssetEnvironment(t, p, contentServer(t, p, nil))
		out := filepath.Join(dirs.write, "out")
		for _, path := range bad {
			quoted := quote(path)
			cases := map[*tool]string{
				contentsGet:     `{"path":` + quoted + `}`,
				contentsGetRaw:  `{"path":` + quoted + `,"local_path":` + quote(out) + `}`,
				editorconfigGet: `{"path":` + quoted + `}`,
			}
			if p == gitea {
				cases[contentsGetExt] = `{"path":` + quoted + `}`
				cases[contentsGetMany] = `{"files":["ok",` + quoted + `]}`
				cases[archivesGet] = `{"ref":"main","paths":[` + quoted + `],"local_path":` + quote(out) + `}`
			}
			for tl, arguments := range cases {
				if _, err := e.invoke(t, tl, "files", arguments); err == nil {
					t.Errorf("%s %s accepted %q", p.id, tl.action, path)
				}
			}
		}
		for _, ref := range []string{"a..b", "-x", "a b", "x~1"} {
			if _, err := e.invoke(t, contentsGet, "files", `{"path":"a","ref":`+quote(ref)+`}`); !isInvalidRequest(err) {
				t.Errorf("%s ref %q: %v", p.id, ref, err)
			}
		}
		if *e.reads != 0 || len(*e.calls) != 0 {
			t.Errorf("%s: reads %d, calls %d", p.id, *e.reads, len(*e.calls))
		}
	}
}

func TestContentToolsRefuseARepositoryOutsideTheTargetsBeforeTheSecret(t *testing.T) {
	for _, p := range products {
		e, dirs := newAssetEnvironment(t, p, contentServer(t, p, nil))
		out := filepath.Join(dirs.write, "out")
		cases := map[*tool]string{
			contentsList:    `{}`,
			contentsGet:     `{"path":"a"}`,
			contentsGetRaw:  `{"path":"a"}`,
			editorconfigGet: `{"path":"a"}`,
			archivesGet:     `{"ref":"main","local_path":` + quote(out) + `}`,
		}
		if p == gitea {
			cases[contentsGetExt] = `{"path":"a"}`
			cases[contentsGetMany] = `{"files":["a"]}`
		}
		for tl, arguments := range cases {
			_, err := e.invoke(t, tl, "files", `{"repository":"secret-owner/hidden",`+arguments[1:])
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "secret-owner") {
				t.Errorf("%s %s: %v", p.id, tl.action, err)
			}
		}
		if *e.reads != 0 || len(*e.calls) != 0 {
			t.Errorf("%s: reads %d, calls %d", p.id, *e.reads, len(*e.calls))
		}
	}
}

func TestContentsGetExtAndGetManyAreGiteaOnlyReads(t *testing.T) {
	files := map[string][]byte{"a.txt": []byte("one"), "b.txt": []byte("two")}
	e := newEnvironment(t, gitea, contentServer(t, gitea, files))
	result, err := e.invoke(t, contentsGetExt, "repo", `{"path":"a.txt","includes":["file_content","lfs_metadata"]}`)
	entry, _ := result["entry"].(map[string]any)
	if err != nil || entry["content"] != "one" || (*e.calls)[0].query != "includes=file_content%2Clfs_metadata" {
		t.Fatalf("get-ext: %v, %v, %+v", result, err, *e.calls)
	}
	result, err = e.invoke(t, contentsGetExt, "repo", `{"path":"docs"}`)
	if entries, _ := result["entries"].([]any); err != nil || len(entries) != 1 {
		t.Fatalf("get-ext dir: %v, %v", result, err)
	}
	if _, err := e.invoke(t, contentsGetExt, "repo", `{"path":"a","includes":["nope"]}`); err == nil {
		t.Error("an unknown include was accepted")
	}
	result, err = e.invoke(t, contentsGetMany, "repo", `{"files":["a.txt","b.txt"],"ref":"main"}`)
	entries, _ := result["entries"].([]any)
	last := (*e.calls)[len(*e.calls)-1]
	if err != nil || len(entries) != 2 || entries[1].(map[string]any)["content"] != "two" || last.method != http.MethodPost ||
		last.body != `{"files":["a.txt","b.txt"]}` || last.query != "ref=main" {
		t.Fatalf("get-many: %v, %v, %+v", result, err, last)
	}
	if _, err := e.invoke(t, contentsGetMany, "repo", `{"files":[]}`); err == nil {
		t.Error("no files was accepted")
	}
	files11 := strings.Repeat(`"a",`, maxManyFiles) + `"b"`
	if _, err := e.invoke(t, contentsGetMany, "repo", `{"files":[`+files11+`]}`); err == nil {
		t.Error("too many files were accepted")
	}
	reg := registry(t)
	for _, tl := range []*tool{contentsGetExt, contentsGetMany} {
		d, _, ok := reg.Lookup(gitea.toolID(tl))
		if !ok || d.Risk.Effect != "read" {
			t.Errorf("%s: %+v", tl.action, d.Risk)
		}
		if _, _, ok := reg.Lookup(forgejo.toolID(tl)); ok {
			t.Errorf("forgejo registers %s", tl.action)
		}
	}
}

func TestContentsGetManySharesOneTextBudget(t *testing.T) {
	big := []byte(strings.Repeat("a", maxEntryBytes+10))
	files := map[string][]byte{}
	names := []string{}
	for i := 0; i < maxManyFiles; i++ {
		name := "f" + strconv.Itoa(i)
		files[name], names = big, append(names, `"`+name+`"`)
	}
	e := newEnvironment(t, gitea, contentServer(t, gitea, files))
	result, err := e.invoke(t, contentsGetMany, "repo", `{"files":[`+strings.Join(names, ",")+`]}`)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, row := range result["entries"].([]any) {
		entry := row.(map[string]any)
		content, _ := entry["content"].(string)
		total += len(content)
		if len(content) > maxEntryBytes || (entry["content_truncated"] != true) {
			t.Errorf("entry content %d, truncated %v", len(content), entry["content_truncated"])
		}
	}
	if total > maxContentBytes {
		t.Errorf("total text = %d", total)
	}
}

func TestContentsGetRawReturnsTextOrSizeOnly(t *testing.T) {
	files := map[string][]byte{"a.txt": []byte("plain"), "b.bin": {0, 1, 2}, "big.txt": []byte(strings.Repeat("x", maxContentBytes+5)),
		"dir/c.txt": []byte("nested")}
	for _, p := range products {
		e, _ := newAssetEnvironment(t, p, contentServer(t, p, files))
		result, err := e.invoke(t, contentsGetRaw, "files", `{"path":"a.txt","ref":"main"}`)
		if err != nil || result["content"] != "plain" || result["size"] != float64(5) || result["truncated"] != false ||
			result["media"] != false || (*e.calls)[0].path != contentBase+"/raw/a.txt" || (*e.calls)[0].query != "ref=main" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		result, err = e.invoke(t, contentsGetRaw, "files", `{"path":"b.bin","media":true}`)
		if _, has := result["content"]; err != nil || result["binary"] != true || result["size"] != float64(3) || has ||
			(*e.calls)[1].path != contentBase+"/media/b.bin" {
			t.Fatalf("%s binary: %v, %v", p.id, result, err)
		}
		result, err = e.invoke(t, contentsGetRaw, "files", `{"path":"big.txt"}`)
		content, _ := result["content"].(string)
		if err != nil || result["truncated"] != true || len(content) != maxContentBytes || result["size"] != float64(maxContentBytes+5) {
			t.Fatalf("%s big: %v", p.id, err)
		}
		if _, err := e.invoke(t, contentsGetRaw, "files", `{"path":"dir/c.txt"}`); err != nil ||
			(*e.calls)[3].escaped != contentBase+"/raw/dir/c.txt" {
			t.Errorf("%s nested: %v", p.id, err)
		}
	}
}

func TestDownloadsStayInsideTheReleasedDirectory(t *testing.T) {
	files := map[string][]byte{"a.txt": []byte("plain")}
	for _, p := range products {
		e, dirs := newAssetEnvironment(t, p, contentServer(t, p, files))
		out := filepath.Join(dirs.write, "out.bin")
		raw := `{"path":"a.txt","local_path":` + quote(out) + `}`
		archive := `{"ref":"v1","local_path":` + quote(out) + `}`
		for name, run := range map[string]struct {
			tl        *tool
			arguments string
			want      string
		}{"raw": {contentsGetRaw, raw, "plain"}, "archive": {archivesGet, archive, "ZIPDATA"}} {
			_ = os.Remove(out)
			result, err := e.invokeAs(t, run.tl, "files", run.arguments, false)
			if err != nil || result["local_path"] != out || result["size"] != float64(len(run.want)) || result["sha256"] == "" {
				t.Fatalf("%s %s: %v, %v", p.id, name, result, err)
			}
			if data, err := os.ReadFile(out); err != nil || string(data) != run.want {
				t.Errorf("%s %s: file = %q, %v", p.id, name, data, err)
			}
			if _, has := result["content"]; has {
				t.Errorf("%s %s: content returned with local_path", p.id, name)
			}
			reads, calls := *e.reads, len(*e.calls)
			_, err = e.invokeAs(t, run.tl, "files", run.arguments, false)
			var confirm *application.ConfirmationRequiredError
			if !errors.As(err, &confirm) || *e.reads != reads || len(*e.calls) != calls {
				t.Errorf("%s %s: overwrite without confirmation: %v", p.id, name, err)
			}
			if _, err := e.invoke(t, run.tl, "files", run.arguments); err != nil {
				t.Errorf("%s %s: confirmed overwrite: %v", p.id, name, err)
			}
			outside := filepath.Join(dirs.outside, "out.bin")
			arguments := strings.Replace(run.arguments, quote(out), quote(outside), 1)
			reads, calls = *e.reads, len(*e.calls)
			_, err = e.invoke(t, run.tl, "files", arguments)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), dirs.outside) || *e.reads != reads || len(*e.calls) != calls {
				t.Errorf("%s %s: outside: %v", p.id, name, err)
			}
		}
		if (*e.calls)[len(*e.calls)-1].auth == "" {
			t.Errorf("%s: download sent no credential", p.id)
		}
	}
}

type failingBody struct{ sent bool }

func (b *failingBody) Read(p []byte) (int, error) {
	if b.sent {
		return 0, errors.New("connection reset")
	}
	b.sent = true
	return copy(p, "par"), nil
}

func TestDownloadsRefuseRedirectsAndLeaveNoPartialFile(t *testing.T) {
	cases := map[string]func() *http.Response{
		"redirect": func() *http.Response {
			return &http.Response{StatusCode: http.StatusFound, Body: io.NopCloser(strings.NewReader("")),
				Header: http.Header{"Location": {"https://storage.example.test/signed"}}}
		},
		"announced": func() *http.Response {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ZIP")), ContentLength: 9}
		},
		"broken": func() *http.Response {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(&failingBody{}), ContentLength: -1}
		},
		"refused": notFoundResponse,
	}
	for _, p := range products {
		for name, answer := range cases {
			e, dirs := newAssetEnvironment(t, p, func(c call) *http.Response { return answer() })
			out := filepath.Join(dirs.write, "out.bin")
			for _, tl := range []struct {
				tl        *tool
				arguments string
			}{{contentsGetRaw, `{"path":"a.txt","local_path":` + quote(out) + `}`},
				{archivesGet, `{"ref":"v1","format":"tar.gz","local_path":` + quote(out) + `}`}} {
				_, err := e.invoke(t, tl.tl, "files", tl.arguments)
				if err == nil || strings.Contains(err.Error(), "storage.example") {
					t.Errorf("%s %s %s: %v", p.id, tl.tl.action, name, err)
				}
				if entries, _ := os.ReadDir(dirs.write); len(entries) != 0 {
					t.Errorf("%s %s %s: files left: %v", p.id, tl.tl.action, name, entries)
				}
			}
		}
	}
}

func TestArchiveNamesRefAndFormatAndOffersPathsOnGiteaOnly(t *testing.T) {
	for _, p := range products {
		e, dirs := newAssetEnvironment(t, p, contentServer(t, p, nil))
		out := filepath.Join(dirs.write, "a.tgz")
		arguments := `{"ref":"release/1.0","format":"tar.gz","local_path":` + quote(out) + `}`
		if _, err := e.invoke(t, archivesGet, "files", arguments); err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		if got := (*e.calls)[0].escaped; got != contentBase+"/archive/release%2F1.0.tar.gz" &&
			got != contentBase+"/archive/release/1.0.tar.gz" {
			t.Errorf("%s path = %s", p.id, got)
		}
		for name, bad := range map[string]string{
			"format":   `{"ref":"main","format":"rar","local_path":` + quote(out) + `}`,
			"no path":  `{"ref":"main"}`,
			"no ref":   `{"local_path":` + quote(out) + `}`,
			"bad ref":  `{"ref":"a..b","local_path":` + quote(out) + `}`,
			"no paths": `{"ref":"main","paths":[],"local_path":` + quote(out) + `}`,
		} {
			if _, err := e.invoke(t, archivesGet, "files", bad); err == nil {
				t.Errorf("%s %s accepted", p.id, name)
			}
		}
		_ = os.Remove(out)
		_, err := e.invoke(t, archivesGet, "files", `{"ref":"main","paths":["src","docs/a"],"local_path":`+quote(out)+`}`)
		if (p == gitea) == (err != nil) {
			t.Errorf("%s paths: %v", p.id, err)
		}
		if p == gitea {
			if last := (*e.calls)[len(*e.calls)-1]; last.query != "path=src&path=docs%2Fa" {
				t.Errorf("query = %s", last.query)
			}
		}
	}
}

func TestEditorconfigReadsTheSettingsOfAFile(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, contentServer(t, p, nil))
		result, err := e.invoke(t, editorconfigGet, "repo", `{"path":"src/main.go","ref":"main"}`)
		settings, _ := result["config"].(map[string]any)
		if err != nil || (*e.calls)[0].path != contentBase+"/editorconfig/src/main.go" || (*e.calls)[0].query != "ref=main" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		want := map[string]any{"indent_style": "tab", "indent_size": "4"}
		if p == gitea {
			want = map[string]any{}
		}
		if len(settings) != len(want) || (len(want) > 0 && !reflect.DeepEqual(settings, want)) {
			t.Errorf("%s config = %v", p.id, settings)
		}
	}
}
