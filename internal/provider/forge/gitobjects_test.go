package forge

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func blobFixture(p *product, sha string, data []byte) string {
	fields := `"sha":"` + sha + `","size":` + itoa(len(data)) + `,"url":"u","encoding":"base64","content":"` +
		base64.StdEncoding.EncodeToString(data) + `"`
	if p == gitea {
		fields += `,"lfs_oid":"oid1","lfs_size":7`
	}
	return `{` + fields + `}`
}

func gitServer(p *product, blobs map[string][]byte, extra func(call) *http.Response) func(call) *http.Response {
	return func(c call) *http.Response {
		const base = "/api/v1/repos/acme/widget/git"
		if extra != nil {
			if r := extra(c); r != nil {
				return r
			}
		}
		switch {
		case c.path == base+"/blobs":
			var rows []string
			for _, sha := range strings.Split(mustQuery(c.query).Get("shas"), ",") {
				rows = append(rows, blobFixture(p, sha, blobs[sha]))
			}
			return jsonResponse(200, `[`+strings.Join(rows, ",")+`]`)
		case strings.HasPrefix(c.path, base+"/blobs/"):
			return jsonResponse(200, blobFixture(p, strings.TrimPrefix(c.path, base+"/blobs/"), blobs[strings.TrimPrefix(c.path, base+"/blobs/")]))
		case strings.HasPrefix(c.path, base+"/trees/"):
			return jsonResponse(200, `{"sha":"t1","url":"u","page":2,"total_count":3,"truncated":true,"tree":[`+
				`{"path":"a.go","mode":"100644","type":"blob","size":5,"sha":"b1","url":"u"},`+
				`{"path":"dir","mode":"040000","type":"tree","sha":"t2","url":"u"}]}`)
		case c.path == base+"/refs":
			return jsonResponse(200, `[{"ref":"refs/heads/main","url":"u","object":{"sha":"c1","type":"commit","url":"u"}},`+
				`{"ref":"refs/tags/v1","url":"u","object":{"sha":"c2","type":"tag","url":"u"}}]`)
		case strings.HasPrefix(c.path, base+"/refs/"):
			return jsonResponse(200, `{"ref":"refs/heads/main","url":"u","object":{"sha":"c1","type":"commit","url":"u"}}`)
		case strings.HasPrefix(c.path, base+"/tags/"):
			tag := `{"tag":"v1","sha":"c2","url":"u","message":"msg","tagger":{"name":"Al","email":"a@example.test","date":"d"},` +
				`"object":{"sha":"c1","type":"commit","url":"u"},` +
				`"verification":{"verified":false,"reason":"gpg.error.no_gpg_keys_found","payload":"RAW","signature":"RAW"}`
			if p == forgejo {
				tag += `,"archive_download_count":{"zip":1,"tar_gz":2}`
			}
			return jsonResponse(200, tag+`}`)
		case strings.HasPrefix(c.path, base+"/notes/") && c.method == http.MethodDelete:
			return jsonResponse(204, "")
		case strings.HasPrefix(c.path, base+"/notes/"):
			return jsonResponse(200, `{"message":"a note","commit":`+commitFixture("c0ffee1")+`}`)
		}
		return notFoundResponse()
	}
}

func mustQuery(raw string) url.Values {
	q, _ := url.ParseQuery(raw)
	return q
}

func TestGitBlobsAreDecodedCutAndBinaryIsLeftOut(t *testing.T) {
	blobs := map[string][]byte{"aaaaaaa": []byte("héllo\n"), "bbbbbbb": {0x00, 0x01, 0x02},
		"ccccccc": []byte(strings.Repeat("é", maxBlobBytes)), "ddddddd": {0xff, 0xfe, 0xfd}}
	for _, p := range products {
		e := newEnvironment(t, p, gitServer(p, blobs, nil))
		text, err := e.invoke(t, gitBlobsGet, "repo", `{"sha":"aaaaaaa"}`)
		if err != nil || text["content"] != "héllo\n" || text["encoding"] != "utf-8" || text["truncated"] != false ||
			text["binary"] != nil || (text["lfs_oid"] != nil) != (p == gitea) {
			t.Fatalf("%s: %v, %v", p.id, text, err)
		}
		for _, sha := range []string{"bbbbbbb", "ddddddd"} {
			bin, err := e.invoke(t, gitBlobsGet, "repo", `{"sha":"`+sha+`"}`)
			if err != nil || bin["binary"] != true || bin["content"] != nil || bin["size"] != float64(3) {
				t.Errorf("%s %s: %v, %v", p.id, sha, bin, err)
			}
		}
		big, err := e.invoke(t, gitBlobsGet, "repo", `{"sha":"ccccccc"}`)
		content, _ := big["content"].(string)
		if err != nil || big["truncated"] != true || len(content) > maxBlobBytes || len(content) < maxBlobBytes-3 ||
			!strings.HasPrefix(content, "ééé") || big["binary"] != nil {
			t.Errorf("%s: truncated %v, %d bytes, %v", p.id, big["truncated"], len(content), err)
		}
	}
}

func TestGitBlobsListIsForgejoOnlyAndSharesOneBudget(t *testing.T) {
	blobs := map[string][]byte{"aaaaaaa": []byte(strings.Repeat("a", maxBlobBytes-1)), "bbbbbbb": []byte("bbbb")}
	e := newEnvironment(t, forgejo, gitServer(forgejo, blobs, nil))
	result, err := e.invoke(t, gitBlobsList, "repo", `{"shas":["aaaaaaa","bbbbbbb"]}`)
	rows, _ := result["blobs"].([]any)
	if err != nil || len(rows) != 2 || rows[1].(map[string]any)["content"] != "b" || rows[1].(map[string]any)["truncated"] != true {
		t.Fatalf("%v, %v", result, err)
	}
	if got := (*e.calls)[0].query; got != "shas=aaaaaaa%2Cbbbbbbb" {
		t.Errorf("query = %q", got)
	}
	many := make([]string, 21)
	for i := range many {
		many[i] = `"aaaaaaa"`
	}
	for _, bad := range []string{`{"shas":[]}`, `{"shas":["zz"]}`, `{"shas":["aaaaaaa,bbbbbbb"]}`, `{"shas":[` + strings.Join(many, ",") + `]}`} {
		if _, err := e.invoke(t, gitBlobsList, "repo", bad); !isInvalidRequest(err) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	if len(*e.calls) != 1 {
		t.Errorf("calls = %v", methods(e))
	}
	reg := registry(t)
	if _, _, ok := reg.Lookup("gitea.git-blobs.list"); ok {
		t.Error("git-blobs.list is registered for Gitea")
	}
}

func TestGitTreesPageWithPerPage(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, gitServer(p, nil, nil))
		result, err := e.invoke(t, gitTreesGet, "repo", `{"sha":"c0ffee1","recursive":true,"page":2,"per_page":1000}`)
		entries, _ := result["tree"].([]any)
		if err != nil || len(entries) != 2 || result["truncated"] != true || result["total_count"] != float64(3) ||
			result["page"] != float64(2) || entries[0].(map[string]any)["path"] != "a.go" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		q := mustQuery((*e.calls)[0].query)
		if q.Get("per_page") != "1000" || q.Get("page") != "2" || q.Get("recursive") != "true" || q.Get("limit") != "" {
			t.Errorf("%s query = %v", p.id, q)
		}
		for _, bad := range []string{`{"sha":"c0ffee1","per_page":1001}`, `{"sha":"c0ffee1","limit":10}`,
			`{"sha":"c0ffee1","page":-1}`, `{"sha":"main"}`} {
			if _, err := e.invoke(t, gitTreesGet, "repo", bad); !isInvalidRequest(err) {
				t.Errorf("%s %s: %v", p.id, bad, err)
			}
		}
		if len(*e.calls) != 1 {
			t.Errorf("%s calls = %v", p.id, methods(e))
		}
	}
}

func TestGitRefsTagsAndNotesRead(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, gitServer(p, nil, nil))
		all, err := e.invoke(t, gitRefsList, "repo", `{}`)
		if err != nil || len(all["refs"].([]any)) != 2 || all["truncated"] != nil {
			t.Fatalf("%s: %v, %v", p.id, all, err)
		}
		one, err := e.invoke(t, gitRefsList, "repo", `{"ref":"heads/main"}`)
		if err != nil || len(one["refs"].([]any)) != 1 || one["refs"].([]any)[0].(map[string]any)["object"].(map[string]any)["sha"] != "c1" {
			t.Fatalf("%s: %v, %v", p.id, one, err)
		}
		if _, err := e.invoke(t, gitRefsList, "repo", `{"ref":"tags/"}`); err != nil {
			t.Fatal(err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/git/refs",
			"GET /api/v1/repos/acme/widget/git/refs/heads/main", "GET /api/v1/repos/acme/widget/git/refs/tags/"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
		tag, err := e.invoke(t, gitTagsGet, "repo", `{"sha":"c2c2c2c"}`)
		_, hasCount := tag["archive_download_count"]
		if err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		verification := tag["verification"].(map[string]any)
		if err != nil || tag["tag"] != "v1" || hasCount != (p == forgejo) || verification["payload"] != nil ||
			tag["tagger"].(map[string]any)["name"] != "Al" {
			t.Fatalf("%s: %v, %v", p.id, tag, err)
		}
		note, err := e.invoke(t, gitNotesGet, "repo", `{"sha":"c0ffee1","files":false}`)
		if err != nil || note["message"] != "a note" || note["commit"].(map[string]any)["sha"] != "c0ffee1" {
			t.Fatalf("%s: %v, %v", p.id, note, err)
		}
		if got := (*e.calls)[len(*e.calls)-1].query; got != "files=false" {
			t.Errorf("%s query = %q", p.id, got)
		}
	}
}

func TestGitRefsListCutsAtTheLimit(t *testing.T) {
	row := `{"ref":"refs/tags/v","url":"u","object":{"sha":"c1","type":"commit","url":"u"}}`
	e := newEnvironment(t, gitea, func(call) *http.Response {
		return jsonResponse(200, `[`+strings.Repeat(row+",", maxRefRows)+row+`]`)
	})
	result, err := e.invoke(t, gitRefsList, "repo", `{}`)
	if err != nil || result["truncated"] != true || len(result["refs"].([]any)) != maxRefRows {
		t.Fatalf("%v, %v", result, err)
	}
}

func TestGitNotesSetAndDelete(t *testing.T) {
	e := newEnvironment(t, forgejo, gitServer(forgejo, nil, nil))
	args := `{"sha":"c0ffee1","message":"a note"}`
	if _, err := e.invokeAs(t, gitNotesSet, "repo", args, false); err == nil || len(*e.calls) != 0 {
		t.Fatalf("unconfirmed set: %v", err)
	}
	result, err := e.invoke(t, gitNotesSet, "repo", args)
	if err != nil || result["message"] != "a note" || len(*e.calls) != 1 {
		t.Fatalf("%v, %v, %v", result, err, methods(e))
	}
	if c := (*e.calls)[0]; c.method != "POST" || c.path != "/api/v1/repos/acme/widget/git/notes/c0ffee1" ||
		!reflect.DeepEqual(bodyOf(t, c), map[string]any{"message": "a note"}) {
		t.Errorf("call = %+v", c)
	}
	for _, bad := range []string{`{"sha":"c0ffee1","message":""}`, `{"sha":"c0ffee1"}`, `{"sha":"main","message":"x"}`,
		`{"sha":"c0ffee1","message":"` + strings.Repeat("a", maxNoteBytes+1) + `"}`} {
		if _, err := e.invoke(t, gitNotesSet, "repo", bad); err == nil {
			t.Errorf("%s accepted", bad[:20])
		}
	}
	failing := newEnvironment(t, forgejo, func(call) *http.Response { return jsonResponse(502, `{"message":"`+bodyCanary+`"}`) })
	if _, err := failing.invoke(t, gitNotesSet, "repo", args); err == nil || len(*failing.calls) != 1 ||
		!strings.Contains(err.Error(), "may have been applied") {
		t.Errorf("%v, %d calls", err, len(*failing.calls))
	}

	// Deleting is offered only to a connection whose tools list names it.
	d := newEnvironment(t, forgejo, gitServer(forgejo, nil, nil))
	if _, err := d.invoke(t, gitNotesDelete, "repo", `{"sha":"c0ffee1"}`); err == nil || len(*d.calls) != 0 {
		t.Fatalf("delete without the tools list: %v", err)
	}
	if _, err := d.invokeAs(t, gitNotesDelete, "notes-delete", `{"sha":"c0ffee1"}`, false); err == nil || len(*d.calls) != 0 {
		t.Fatalf("unconfirmed delete: %v", err)
	}
	out, err := d.invoke(t, gitNotesDelete, "notes-delete", `{"sha":"c0ffee1"}`)
	if err != nil || out["deleted"] != true || !reflect.DeepEqual(methods(d), []string{"DELETE /api/v1/repos/acme/widget/git/notes/c0ffee1"}) {
		t.Fatalf("%v, %v, %v", out, err, methods(d))
	}
}

func TestForgejoOnlyGitToolsAreNotRegisteredForGitea(t *testing.T) {
	reg := registry(t)
	for _, id := range []string{"git-blobs.list", "git-notes.set", "git-notes.delete"} {
		if _, _, ok := reg.Lookup("forgejo." + id); !ok {
			t.Errorf("forgejo.%s is missing", id)
		}
		if _, _, ok := reg.Lookup("gitea." + id); ok {
			t.Errorf("gitea.%s is registered", id)
		}
	}
	d, _, _ := reg.Lookup("forgejo.git-notes.delete")
	if !d.RequiresToolAllowList {
		t.Error("git-notes.delete is not listed-only")
	}
	for _, profile := range metadata(forgejo).Profiles {
		for _, id := range profile.Tools {
			if strings.Contains(id, "git-notes") {
				t.Errorf("a profile holds %s", id)
			}
		}
	}
}

func TestGitArgumentsAreRefusedBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		tool *tool
		args string
	}{
		{gitBlobsGet, `{"sha":"zz"}`}, {gitBlobsGet, `{"sha":"../x"}`}, {gitTagsGet, `{"sha":"v1"}`},
		{gitNotesGet, `{"sha":"main"}`}, {gitRefsList, `{"ref":"a..b"}`}, {gitRefsList, `{"ref":"heads/x y"}`},
		{gitTreesGet, `{"sha":"nothex!"}`}, {gitRefsList, `{"repository":"other/place"}`},
		{gitBlobsGet, `{"repository":"other/place","sha":"aaaaaaa"}`}, {gitTreesGet, `{"repository":"other/place","sha":"aaaaaaa"}`},
	}
	for _, p := range products {
		e := newEnvironment(t, p, gitServer(p, nil, nil))
		for _, c := range cases {
			if _, err := e.invoke(t, c.tool, "repo", c.args); !isInvalidRequest(err) {
				t.Errorf("%s %s %s: %v", p.id, c.tool.action, c.args, err)
			}
		}
		if len(*e.calls) != 0 || *e.reads != 0 {
			t.Errorf("%s: calls %v, reads %d", p.id, methods(e), *e.reads)
		}
	}
}
