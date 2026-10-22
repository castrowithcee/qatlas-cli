package forge

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// tagFixture is the answer of one product for a tag. Forgejo adds the archive download counts.
func tagFixture(p *product, name string) string {
	fields := `"name":"` + name + `","id":"abc123","message":"release","commit":{"sha":"c0ffee","created":"2030-01-02T03:04:05Z",` +
		`"url":"https://forge.example.test/api/v1/repos/acme/widget/git/commits/c0ffee"},` +
		`"zipball_url":"https://forge.example.test/acme/widget/archive/` + name + `.zip",` +
		`"tarball_url":"https://` + "user:pw" + `@forge.example.test/acme/widget/archive/` + name + `.tar.gz"`
	if p == forgejo {
		fields += `,"archive_download_count":{"zip":3,"tar_gz":4}`
	}
	return `{` + fields + `}`
}

func tagServer(p *product) func(call) *http.Response {
	return func(c call) *http.Response {
		const base = "/api/v1/repos/acme/widget/tags"
		switch {
		case c.path == base && c.method == http.MethodGet:
			return jsonResponse(200, `[`+tagFixture(p, "v1.0.0")+`]`, "Link", `<x?page=2>; rel="next"`)
		case c.path == base && c.method == http.MethodPost:
			return jsonResponse(map[bool]int{true: 201, false: 200}[p == forgejo], tagFixture(p, "v2.0.0"))
		case strings.HasPrefix(c.path, base+"/") && c.method == http.MethodGet:
			return jsonResponse(200, tagFixture(p, strings.TrimPrefix(c.path, base+"/")))
		case strings.HasPrefix(c.path, base+"/") && c.method == http.MethodDelete:
			return jsonResponse(204, "")
		}
		return notFoundResponse()
	}
}

func TestTagListAndGetShowProductMembersAndDropAddressesWithUserInfo(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, tagServer(p))
		result, err := e.invoke(t, tagsList, "repo", `{"limit":10}`)
		if err != nil || result["has_more"] != true || result["next_cursor"] == nil || result["repository"] != "acme/widget" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if (*e.calls)[0].query != "limit=10&page=1" {
			t.Errorf("%s query = %q", p.id, (*e.calls)[0].query)
		}
		tag := result["tags"].([]any)[0].(map[string]any)
		_, hasCount := tag["archive_download_count"]
		if tag["name"] != "v1.0.0" || tag["zipball_url"] == nil || tag["tarball_url"] != nil || hasCount != (p == forgejo) {
			t.Errorf("%s tag = %v", p.id, tag)
		}
		got, err := e.invoke(t, tagsGet, "repo", `{"tag":"v1.0.0"}`)
		if err != nil || got["name"] != "v1.0.0" || got["commit"].(map[string]any)["sha"] != "c0ffee" {
			t.Errorf("%s get = %v, %v", p.id, got, err)
		}
		if last := (*e.calls)[len(*e.calls)-1]; last.method != http.MethodGet || last.path != "/api/v1/repos/acme/widget/tags/v1.0.0" {
			t.Errorf("%s last call = %+v", p.id, last)
		}
	}
}

func TestTagOutsideTheTargetsIsRefusedBeforeTheSecret(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, tagServer(p))
		for _, tl := range []*tool{tagsList, tagsGet, tagsCreate} {
			args := map[*tool]string{tagsList: `{"repository":"secret-owner/hidden"}`,
				tagsGet:    `{"repository":"secret-owner/hidden","tag":"v1"}`,
				tagsCreate: `{"repository":"secret-owner/hidden","tag_name":"v1"}`}[tl]
			_, err := e.invoke(t, tl, "repo", args)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "secret-owner") || *e.reads != 0 || len(*e.calls) != 0 {
				t.Errorf("%s %s: %v, reads %d, calls %d", p.id, tl.action, err, *e.reads, len(*e.calls))
			}
		}
		if _, err := e.invoke(t, tagsList, "open", `{}`); !isInvalidRequest(err) {
			t.Errorf("%s: a repository is required without a single target: %v", p.id, err)
		}
	}
}

func TestTagNamesAreValidated(t *testing.T) {
	bad := []string{"a b", "a..b", "-a", ".a", "a.", "a.lock", "a~b", "a^b", "a:b", "a?b", "a*b", "a[b", `a\b`, "a/b",
		"a\nb", strings.Repeat("a", 256), "a@{b"}
	for _, p := range products {
		e := newEnvironment(t, p, tagServer(p))
		for _, name := range bad {
			arg, _ := jsonString(name)
			if _, err := e.invoke(t, tagsGet, "repo", `{"tag":`+arg+`}`); !isInvalidRequest(err) {
				t.Errorf("%s: tag %q accepted: %v", p.id, name, err)
			}
		}
		for _, name := range bad[:len(bad)-1] {
			if name == "a/b" {
				continue
			}
			arg, _ := jsonString(name)
			if _, err := e.invoke(t, tagsCreate, "repo", `{"tag_name":`+arg+`}`); !isInvalidRequest(err) {
				t.Errorf("%s: tag_name %q accepted: %v", p.id, name, err)
			}
		}
		if _, err := e.invoke(t, tagsCreate, "repo", `{"tag_name":"rel/v1","target":"-x"}`); !isInvalidRequest(err) {
			t.Errorf("%s: target accepted: %v", p.id, err)
		}
		if len(*e.calls) != 0 || *e.reads != 0 {
			t.Errorf("%s: a refused name reached the instance", p.id)
		}
	}
}

func TestTagCreateSendsOneRequestWithValidatedMembers(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, tagServer(p))
		if _, err := e.invokeAs(t, tagsCreate, "repo", `{"tag_name":"v2.0.0"}`, false); err == nil || len(*e.calls) != 0 {
			t.Fatalf("%s: unconfirmed create: %v", p.id, err)
		}
		result, err := e.invoke(t, tagsCreate, "repo", `{"tag_name":"rel/v2.0.0","target":"main","message":"hello"}`)
		if err != nil || result["name"] != "v2.0.0" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"POST /api/v1/repos/acme/widget/tags"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
		if body := bodyOf(t, (*e.calls)[0]); !reflect.DeepEqual(body, map[string]any{"tag_name": "rel/v2.0.0",
			"target": "main", "message": "hello"}) {
			t.Errorf("%s body = %v", p.id, body)
		}
	}
}

func TestTagDeleteIsListedOnlyAndInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		d, _, ok := reg.Lookup(p.toolID(tagsDelete))
		if !ok || !d.RequiresToolAllowList || d.Risk.Confirmation != "required" {
			t.Fatalf("%s descriptor = %+v", p.id, d)
		}
		for _, other := range reg.Provider(p.id) {
			if other.ID == p.toolID(tagsList) || other.ID == p.toolID(tagsGet) || other.ID == p.toolID(tagsCreate) {
				if other.RequiresToolAllowList {
					t.Errorf("%s is listed-only", other.ID)
				}
			}
		}
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if strings.HasPrefix(id, p.id+".tags.") && id != "" {
					t.Errorf("%s: profile %s holds %s", p.id, profile.ID, id)
				}
			}
		}
		e := newEnvironment(t, p, tagServer(p))
		if _, err := e.invoke(t, tagsDelete, "repo", `{"tag":"v1"}`); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: delete without a tools list: %v", p.id, err)
		}
		cfg := testConfig(p)
		connection := cfg.Connections["delete"]
		connection.Tools = []string{p.id + ".tags.delete"}
		cfg.Connections["delete"] = connection
		red := &redact.Redactor{}
		e.core = application.New(reg, cfg, resolver(red, e.reads), red)
		e.red = red
		if _, err := e.invokeAs(t, tagsDelete, "delete", `{"tag":"v1"}`, false); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed delete: %v", p.id, err)
		}
		result, err := e.invoke(t, tagsDelete, "delete", `{"tag":"v1"}`)
		if err != nil || result["deleted"] != true || result["tag"] != "v1" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"DELETE /api/v1/repos/acme/widget/tags/v1"}) ||
			(*e.calls)[0].body != "" {
			t.Errorf("%s calls = %v", p.id, got)
		}
	}
}

func jsonString(s string) (string, error) {
	out, err := json.Marshal(s)
	return string(out), err
}
