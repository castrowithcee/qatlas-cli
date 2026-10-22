package forge

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// releaseFixture is the answer of one product for a release of acme/widget. Forgejo adds the members only it
// documents.
func releaseFixture(p *product, id string, tag string) string {
	fields := `"id":` + id + `,"tag_name":"` + tag + `","target_commitish":"main","name":"Version","body":"Notes",` +
		`"draft":false,"prerelease":false,"created_at":"2030-01-01T00:00:00Z","published_at":"2030-01-02T00:00:00Z",` +
		`"url":"https://` + apiHost + `/api/v1/repos/acme/widget/releases/` + id + `",` +
		`"html_url":"https://` + apiHost + `/acme/widget/releases/tag/` + tag + `",` +
		`"tarball_url":"https://` + apiHost + `/acme/widget/archive/` + tag + `.tar.gz",` +
		`"zipball_url":"https://` + apiHost + `/acme/widget/archive/` + tag + `.zip?token=` + "x" + `",` +
		`"upload_url":"https://` + apiHost + `/api/v1/repos/acme/widget/releases/` + id + `/assets",` +
		`"author":{"id":3,"login":"alice"},` +
		`"assets":[{"id":1,"name":"app.zip","size":10,"browser_download_url":"https://` + apiHost +
		`/acme/widget/releases/download/` + tag + `/app.zip"}]`
	if p == forgejo {
		fields += `,"hide_archive_links":true,"archive_download_count":{"tar_gz":4,"zip":5}`
	}
	return `{` + fields + `}`
}

func releaseServer(p *product, change func(call) *http.Response) func(call) *http.Response {
	return func(c call) *http.Response {
		switch {
		case c.method == http.MethodGet && c.path == "/api/v1/repos/acme/widget/releases/12":
			return jsonResponse(200, releaseFixture(p, "12", "v1.2.0"))
		case c.method == http.MethodGet && c.path == "/api/v1/repos/acme/widget/releases/13":
			return jsonResponse(200, releaseFixture(p, "99", "v1.3.0"))
		case c.method == http.MethodGet && c.path == "/api/v1/repos/acme/widget/releases/latest":
			return jsonResponse(200, releaseFixture(p, "12", "v1.2.0"))
		case c.method == http.MethodGet && c.path == "/api/v1/repos/acme/widget/releases/tags/v1.2.0":
			return jsonResponse(200, releaseFixture(p, "12", "v1.2.0"))
		case c.method == http.MethodGet && c.path == "/api/v1/repos/acme/widget/releases":
			return jsonResponse(200, `[`+releaseFixture(p, "12", "v1.2.0")+`,`+releaseFixture(p, "11", "v1.1.0")+`]`,
				"Link", `<https://`+apiHost+`/x?page=2>; rel="next"`)
		}
		if change != nil {
			return change(c)
		}
		return notFoundResponse()
	}
}

func TestReleaseReadsShowProductMembersAndDropUnsafeAddresses(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, releaseServer(p, nil))
		for _, tc := range []struct {
			tool      *tool
			arguments string
			path      string
		}{
			{releasesGet, `{"release_id":12}`, "/api/v1/repos/acme/widget/releases/12"},
			{releasesGetLatest, `{}`, "/api/v1/repos/acme/widget/releases/latest"},
			{releasesGetByTag, `{"tag":"v1.2.0"}`, "/api/v1/repos/acme/widget/releases/tags/v1.2.0"},
		} {
			before := len(*e.calls)
			result, err := e.invoke(t, tc.tool, "repo", tc.arguments)
			if err != nil || result["tag_name"] != "v1.2.0" || result["id"] != float64(12) || result["body"] != "Notes" {
				t.Fatalf("%s %s: %v, %v", p.id, tc.tool.action, result, err)
			}
			if _, ok := result["zipball_url"]; ok {
				t.Errorf("%s: an address with a query is kept", p.id)
			}
			if result["tarball_url"] == nil || result["upload_url"] == nil {
				t.Errorf("%s: plain addresses are dropped: %v", p.id, result)
			}
			assets, _ := result["assets"].([]any)
			if len(assets) != 1 || assets[0].(map[string]any)["name"] != "app.zip" {
				t.Errorf("%s: assets = %v", p.id, result["assets"])
			}
			if _, ok := result["hide_archive_links"]; ok != (p == forgejo) {
				t.Errorf("%s: hide_archive_links = %v", p.id, result["hide_archive_links"])
			}
			if _, ok := result["archive_download_count"]; ok != (p == forgejo) {
				t.Errorf("%s: archive_download_count present = %v", p.id, ok)
			}
			if got := (*e.calls)[before]; got.method != http.MethodGet || got.path != tc.path || len(*e.calls) != before+1 {
				t.Errorf("%s %s: %+v", p.id, tc.tool.action, got)
			}
		}
	}
}

func TestReleaseGetBindsTheIDToItsAnswerAndRepository(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, releaseServer(p, nil))
		// The instance answers another release than asked for.
		if _, err := e.invoke(t, releasesGet, "repo", `{"release_id":13}`); classOfError(err) != provider.ClassInvalidResponse {
			t.Errorf("%s: %v", p.id, err)
		}
		// A release of another repository is not found: the path carries the repository.
		if _, err := e.invoke(t, releasesGet, "repo", `{"release_id":14}`); classOfError(err) != "not-found" {
			t.Errorf("%s: %v", p.id, err)
		}
		if got := e.paths(); !slices.Equal(got, []string{"/api/v1/repos/acme/widget/releases/13",
			"/api/v1/repos/acme/widget/releases/14"}) {
			t.Errorf("%s: paths = %v", p.id, got)
		}
	}
}

func TestReleaseListFiltersAndPages(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, giteaOrForgejo(p, "28.2.0", releaseServer(p, nil)))
		filter := `"q":"notes"`
		if p == gitea {
			filter = `"tag_filter":"v1"`
		}
		result, err := e.invoke(t, releasesList, "repo", `{"draft":false,"prerelease":true,"limit":2,`+filter+`}`)
		if err != nil || result["has_more"] != true || result["next_cursor"] == nil || result["repository"] != "acme/widget" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if rows, _ := result["releases"].([]any); len(rows) != 2 {
			t.Errorf("%s: rows = %v", p.id, result["releases"])
		}
		var list call
		for _, c := range *e.calls {
			if c.path == "/api/v1/repos/acme/widget/releases" {
				list = c
			}
		}
		want := "draft=false&limit=2&page=1&pre-release=true&q=notes"
		if p == gitea {
			want = "draft=false&limit=2&page=1&pre-release=true&tag_filter=v1"
		}
		if list.query != want {
			t.Errorf("%s: query = %q, want %q", p.id, list.query, want)
		}
		// The continuation keeps the filters.
		next, err := e.invoke(t, releasesList, "repo", `{"draft":false,"prerelease":true,"cursor":"`+
			result["next_cursor"].(string)+`",`+filter+`}`)
		if err != nil || next == nil {
			t.Errorf("%s: continuation: %v", p.id, err)
		}
		if _, err := e.invoke(t, releasesList, "repo", `{"cursor":"`+result["next_cursor"].(string)+`"}`); !isInvalidRequest(err) {
			t.Errorf("%s: a cursor of other filters is accepted: %v", p.id, err)
		}
	}
}

// giteaOrForgejo answers the version endpoints of the product and everything else with api.
func giteaOrForgejo(p *product, v string, api func(call) *http.Response) func(call) *http.Response {
	if p == forgejo {
		return forgejoAt(v, api)
	}
	return giteaAt(v, api)
}

func TestReleaseTagFilterNeedsAVersionThatDocumentsIt(t *testing.T) {
	e := newEnvironment(t, gitea, giteaAt("1.27.3", releaseServer(gitea, nil)))
	_, err := e.invoke(t, releasesList, "repo", `{"tag_filter":"v1"}`)
	if err == nil || !strings.Contains(err.Error(), "28.2.0") {
		t.Fatalf("err = %v", err)
	}
	for _, path := range e.paths() {
		if path == "/api/v1/repos/acme/widget/releases" {
			t.Errorf("the list was requested: %v", e.paths())
		}
	}
	// Without the filter the old version lists.
	if _, err := e.invoke(t, releasesList, "repo", `{}`); err != nil {
		t.Errorf("list on 1.27.3: %v", err)
	}
}

func TestReleaseOutsideTheTargetsAndOtherProductMembersAreRefusedBeforeTheSecret(t *testing.T) {
	foreign := map[string][]struct {
		tool      *tool
		arguments string
	}{
		forgejo.id: {
			{releasesCreate, `{"tag_name":"v1","tag_message":"m"}`},
			{releasesList, `{"tag_filter":"v1"}`},
		},
		gitea.id: {
			{releasesCreate, `{"tag_name":"v1","hide_archive_links":true}`},
			{releasesUpdate, `{"release_id":12,"hide_archive_links":true}`},
			{releasesList, `{"q":"x"}`},
		},
	}
	for _, p := range products {
		e := newEnvironment(t, p, releaseServer(p, nil))
		for _, entry := range foreign[p.id] {
			if _, err := e.invoke(t, entry.tool, "repo", entry.arguments); !isInvalidRequest(err) {
				t.Errorf("%s %s %s: %v", p.id, entry.tool.action, entry.arguments, err)
			}
		}
		for _, tl := range []*tool{releasesList, releasesGet, releasesGetLatest, releasesGetByTag, releasesCreate,
			releasesUpdate} {
			arguments := `{"repository":"secret-owner/hidden","release_id":12,"tag":"v1","tag_name":"v1"}`
			_, err := e.invoke(t, tl, "repo", arguments)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "secret-owner") {
				t.Errorf("%s %s: %v", p.id, tl.action, err)
			}
		}
		if *e.reads != 0 || len(*e.calls) != 0 {
			t.Errorf("%s: reads %d, calls %d", p.id, *e.reads, len(*e.calls))
		}
	}
}

func TestReleaseTagNamesAreValidatedAndEscaped(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "a/b", "a b", "-x", "x.", "a..b", "a\\x", "x?y", "a\u0000b",
		strings.Repeat("a", 256), "x.lock", "@", "a@{b"} {
		if validRefName(bad, false) {
			t.Errorf("tag %q accepted", bad)
		}
	}
	if !validRefName("release/1.0", true) || validRefName("release//1.0", true) || validRefName("/x", true) ||
		validRefName("a/.b", true) {
		t.Error("tag names with a slash are not checked for a body")
	}
	for _, p := range products {
		e := newEnvironment(t, p, releaseServer(p, func(c call) *http.Response { return jsonResponse(204, "") }))
		for _, tag := range []string{"a/b", "..", "x y"} {
			for _, tl := range []*tool{releasesGetByTag, releasesDeleteByTag} {
				connection := "repo"
				if tl == releasesDeleteByTag {
					connection = "release-delete"
				}
				if _, err := e.invoke(t, tl, connection, `{"tag":"`+tag+`"}`); !isInvalidRequest(err) {
					t.Errorf("%s %s %q: %v", p.id, tl.action, tag, err)
				}
			}
		}
		if len(*e.calls) != 0 || *e.reads != 0 {
			t.Errorf("%s: calls %v", p.id, methods(e))
		}
		// A name with a percent sign and a plus sign stays one escaped segment.
		if _, err := e.invoke(t, releasesGetByTag, "repo", `{"tag":"v1%2B2"}`); !isInvalidRequest(err) && len(*e.calls) == 1 {
			if got := (*e.calls)[0].path; got != "/api/v1/repos/acme/widget/releases/tags/v1%2B2" {
				t.Errorf("%s: path = %q", p.id, got)
			}
		}
	}
}

func TestReleaseCreateSendsOneRequestWithValidatedMembers(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, func(c call) *http.Response {
			return jsonResponse(201, releaseFixture(p, "12", "v1.2.0"))
		})
		extra := `"hide_archive_links":true`
		if p == gitea {
			extra = `"tag_message":"Annotated"`
		}
		result, err := e.invoke(t, releasesCreate, "repo", `{"tag_name":"v1.2.0","target_commitish":"main",`+
			`"name":"Version","body":"Notes","draft":true,"prerelease":false,`+extra+`}`)
		if err != nil || result["tag_name"] != "v1.2.0" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if len(*e.calls) != 1 || (*e.calls)[0].method != http.MethodPost ||
			(*e.calls)[0].path != "/api/v1/repos/acme/widget/releases" {
			t.Fatalf("%s: calls = %v", p.id, methods(e))
		}
		body := bodyOf(t, (*e.calls)[0])
		want := map[string]any{"tag_name": "v1.2.0", "target_commitish": "main", "name": "Version", "body": "Notes",
			"draft": true, "prerelease": false}
		if p == gitea {
			want["tag_message"] = "Annotated"
		} else {
			want["hide_archive_links"] = true
		}
		if !reflect.DeepEqual(body, want) {
			t.Errorf("%s: body = %v, want %v", p.id, body, want)
		}
		// Without confirmation nothing is sent.
		before := len(*e.calls)
		if _, err := e.invokeAs(t, releasesCreate, "repo", `{"tag_name":"v1.3.0"}`, false); err == nil ||
			len(*e.calls) != before {
			t.Errorf("%s: unconfirmed create: %v", p.id, err)
		}
		for _, bad := range []string{`{}`, `{"tag_name":"a b"}`, `{"tag_name":"v1","target_commitish":""}`,
			`{"tag_name":"v1","name":"` + strings.Repeat("x", 256) + `"}`} {
			if _, err := e.invoke(t, releasesCreate, "repo", bad); !isInvalidRequest(err) {
				t.Errorf("%s: %s accepted: %v", p.id, bad, err)
			}
		}
		if len(*e.calls) != before {
			t.Errorf("%s: invalid arguments sent a request", p.id)
		}
	}
}

func TestReleaseUpdatePatchesOnlyTheSetMembersByID(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, func(c call) *http.Response {
			return jsonResponse(200, releaseFixture(p, "12", "v1.2.0"))
		})
		result, err := e.invoke(t, releasesUpdate, "repo", `{"release_id":12,"draft":false,"name":""}`)
		if err != nil || result["id"] != float64(12) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/releases/12",
			"PATCH /api/v1/repos/acme/widget/releases/12"}) {
			t.Fatalf("%s: calls = %v", p.id, got)
		}
		if body := bodyOf(t, (*e.calls)[1]); !reflect.DeepEqual(body, map[string]any{"draft": false, "name": ""}) {
			t.Errorf("%s: body = %v", p.id, body)
		}
		for _, bad := range []string{`{"release_id":12}`, `{"draft":true}`, `{"release_id":12,"tag_message":"m"}`} {
			if _, err := e.invoke(t, releasesUpdate, "repo", bad); !isInvalidRequest(err) {
				t.Errorf("%s: %s accepted: %v", p.id, bad, err)
			}
		}
		if len(*e.calls) != 2 {
			t.Errorf("%s: invalid arguments sent a request", p.id)
		}
	}
}

func TestReleaseDeleteToolsAreListedOnlyAndInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, tl := range []*tool{releasesDelete, releasesDeleteByTag} {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if !ok || !d.RequiresToolAllowList || d.Risk.Confirmation != "required" {
				t.Fatalf("%s descriptor = %+v", p.id, d)
			}
			for _, profile := range metadata.Profiles {
				if slices.Contains(profile.Tools, d.ID) {
					t.Errorf("%s: profile %s holds %s", p.id, profile.ID, d.ID)
				}
			}
		}
		for _, tl := range []*tool{releasesList, releasesGet, releasesGetLatest, releasesGetByTag, releasesCreate,
			releasesUpdate} {
			if d, _, _ := reg.Lookup(p.toolID(tl)); d.RequiresToolAllowList {
				t.Errorf("%s is listed-only", d.ID)
			}
		}
		e := newEnvironment(t, p, releaseServer(p, func(c call) *http.Response {
			if c.method == http.MethodDelete {
				return jsonResponse(204, "")
			}
			return notFoundResponse()
		}))
		for _, arguments := range []map[*tool]string{{releasesDelete: `{"release_id":12}`},
			{releasesDeleteByTag: `{"tag":"v1.2.0"}`}} {
			for tl, args := range arguments {
				if _, err := e.invoke(t, tl, "repo", args); err == nil || len(*e.calls) != 0 {
					t.Errorf("%s %s without a tools list: %v", p.id, tl.action, err)
				}
			}
		}
		if _, err := e.invokeAs(t, releasesDelete, "release-delete", `{"release_id":12}`, false); err == nil ||
			len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed delete: %v", p.id, err)
		}
		result, err := e.invoke(t, releasesDelete, "release-delete", `{"release_id":12}`)
		if err != nil || result["deleted"] != true || result["release_id"] != float64(12) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		result, err = e.invoke(t, releasesDeleteByTag, "release-delete", `{"tag":"v1.2.0"}`)
		if err != nil || result["deleted"] != true || result["tag"] != "v1.2.0" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/releases/12",
			"DELETE /api/v1/repos/acme/widget/releases/12", "GET /api/v1/repos/acme/widget/releases/tags/v1.2.0",
			"DELETE /api/v1/repos/acme/widget/releases/tags/v1.2.0"}) || (*e.calls)[1].body != "" {
			t.Errorf("%s calls = %v", p.id, got)
		}
	}
}

func TestReleaseChangeWithAnOpenOutcomeIsNeverRepeated(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, func(c call) *http.Response { return jsonResponse(500, `{"message":"`+bodyCanary+`"}`) })
		_, err := e.invoke(t, releasesCreate, "repo", `{"tag_name":"v1.2.0"}`)
		if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*e.calls) != 1 {
			t.Errorf("%s: %v, calls %d", p.id, err, len(*e.calls))
		}
	}
}

// releaseWithURL is a release of acme/widget whose url member is replaced; an empty address removes it.
func releaseWithURL(p *product, id, address string) string {
	body := releaseFixture(p, id, "v1.2.0")
	start := strings.Index(body, `"url":"`)
	end := start + strings.Index(body[start+7:], `"`) + 8
	replacement := `"url":"` + address + `"`
	if address == "" {
		replacement = `"x":0`
	}
	return body[:start] + replacement + body[end:]
}

func TestReleaseOfAnotherRepositoryOrWithoutURLIsRefusedBeforeAnyChange(t *testing.T) {
	foreign := "https://" + apiHost + "/api/v1/repos/other/place/releases/12"
	for _, p := range products {
		for _, address := range []string{foreign, "", "https://" + apiHost + "/api/v1/repos/acme/widget/releases/99",
			"https://" + apiHost + "/api/v1/repos/acme/widget/releases/12/assets", "not a url"} {
			e := newEnvironment(t, p, func(c call) *http.Response {
				if c.method == http.MethodGet {
					return jsonResponse(200, releaseWithURL(p, "12", address))
				}
				t.Errorf("%s: change request %s %s", p.id, c.method, c.path)
				return jsonResponse(204, "")
			})
			for tl, arguments := range map[*tool]string{releasesUpdate: `{"release_id":12,"draft":false}`,
				releasesDelete: `{"release_id":12}`, releasesDeleteByTag: `{"tag":"v1.2.0"}`, releasesGet: `{"release_id":12}`,
				releasesGetLatest: `{}`, releasesGetByTag: `{"tag":"v1.2.0"}`} {
				connection := "repo"
				if tl == releasesDelete || tl == releasesDeleteByTag {
					connection = "release-delete"
				}
				before := len(*e.calls)
				_, err := e.invoke(t, tl, connection, arguments)
				if !isInvalidRequest(err) || strings.Contains(err.Error(), "place") ||
					len(*e.calls) != before+1 || (*e.calls)[before].method != http.MethodGet {
					t.Errorf("%s %s %q: %v, calls %v", p.id, tl.action, address, err, methods(e))
				}
			}
		}
	}
}

func TestReleaseURLComparesWithoutCase(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, func(c call) *http.Response {
			if c.method == http.MethodGet && strings.HasSuffix(c.path, "/releases") {
				return jsonResponse(200, `[`+releaseWithURL(p, "12", "https://"+apiHost+"/api/v1/repos/ACME/Widget/releases/12")+`,`+
					releaseWithURL(p, "13", "https://"+apiHost+"/api/v1/repos/other/place/releases/13")+`,`+
					releaseWithURL(p, "14", "")+`]`)
			}
			return jsonResponse(200, releaseWithURL(p, "12", "https://"+apiHost+"/api/v1/repos/ACME/Widget/releases/12"))
		})
		if _, err := e.invoke(t, releasesGet, "repo", `{"release_id":12}`); err != nil {
			t.Errorf("%s: %v", p.id, err)
		}
		result, err := e.invoke(t, releasesList, "repo", `{}`)
		if rows, _ := result["releases"].([]any); err != nil || len(rows) != 1 {
			t.Errorf("%s: rows = %v, %v; rows of other repositories or without url are dropped", p.id, result, err)
		}
	}
}
