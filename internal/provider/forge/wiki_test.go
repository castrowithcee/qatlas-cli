package forge

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

const wikiBase = "/api/v1/repos/acme/widget/wiki/"

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func wikiServer(page string) func(call) *http.Response {
	return func(c call) *http.Response {
		switch {
		case c.path == wikiBase+"pages":
			return jsonResponse(200, `[{"title":"Home","sub_url":"Home","html_url":"https://example.org/w/Home",`+
				`"last_commit":{"sha":"abc","message":"m","author":{"name":"octo","email":"author@example.invalid"}}}]`,
				"Link", `<http://x/?page=2>; rel="next"`)
		case strings.HasPrefix(c.path, wikiBase+"revisions/"):
			return jsonResponse(200, `{"commits":[{"sha":"abc","message":"m","commiter":{"name":"octo"}}],"count":120}`,
				"X-Total-Count", "120")
		case c.path == wikiBase+"new":
			return jsonResponse(201, `{"title":"guide/setup","sub_url":"guide%2Fsetup","content_base64":"`+b64("secret")+`"}`)
		case strings.HasPrefix(c.path, wikiBase+"page/") && c.method == http.MethodGet:
			return jsonResponse(200, `{"title":"T","sub_url":"s","commit_count":2,"sidebar":"sb","content_base64":"`+page+`"}`)
		case strings.HasPrefix(c.path, wikiBase+"page/") && c.method == http.MethodPatch:
			return jsonResponse(200, `{"title":"T","sub_url":"s"}`)
		case strings.HasPrefix(c.path, wikiBase+"page/") && c.method == http.MethodDelete:
			return jsonResponse(204, ``)
		}
		return notFoundResponse()
	}
}

func TestWikiPagesReadAndNameEncoding(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, wikiServer(b64("hello")))
		list, err := e.invoke(t, wikiPagesList, "repo", `{"limit":10}`)
		if pages, _ := list["pages"].([]any); err != nil || len(pages) != 1 || list["has_more"] != true {
			t.Fatalf("%s list: %v %v", p.id, list, err)
		}
		got, err := e.invoke(t, wikiPagesGet, "repo", `{"page_name":"guide/a b%"}`)
		if err != nil || got["content"] != "hello" || got["sidebar"] != "sb" || got["commit_count"] != float64(2) {
			t.Fatalf("%s get: %v %v", p.id, got, err)
		}
		c := (*e.calls)[1]
		if c.escaped != wikiBase+"page/guide%2Fa%20b%25" || c.path != wikiBase+"page/guide/a b%" {
			t.Errorf("%s sent %q", p.id, c.escaped)
		}
		for _, bad := range []string{`""`, `"."`, `".."`, `"a\nb"`, `"` + strings.Repeat("a", 256) + `"`} {
			before := len(*e.calls)
			if _, err := e.invoke(t, wikiPagesGet, "repo", `{"page_name":`+bad+`}`); err == nil || len(*e.calls) != before {
				t.Errorf("%s accepted %s", p.id, bad)
			}
		}
	}
}

func TestWikiContentLimitsAndBinary(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, wikiServer(b64(strings.Repeat("a", maxContentBytes+10))))
		got, err := e.invoke(t, wikiPagesGet, "repo", `{"page_name":"Home"}`)
		if text, _ := got["content"].(string); err != nil || len(text) != maxContentBytes || got["truncated"] != true {
			t.Errorf("%s long page: %v", p.id, err)
		}
		for _, raw := range []string{b64("a\x00b"), b64("\xff\xfe"), "!!notbase64"} {
			f := newEnvironment(t, p, wikiServer(raw))
			got, err := f.invoke(t, wikiPagesGet, "repo", `{"page_name":"Home"}`)
			if _, has := got["content"]; err != nil || has || got["binary"] != true {
				t.Errorf("%s binary page %q: %v %v", p.id, raw, got, err)
			}
		}
	}
}

func TestWikiDisabledIsNotAvailableWithoutProviderText(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, func(call) *http.Response { return notFoundResponse() })
		_, err := e.invoke(t, wikiPagesList, "repo", `{}`)
		if err == nil || !strings.Contains(err.Error(), "wiki") || !strings.Contains(err.Error(), "disabled") ||
			classOfError(err) != "not-found" {
			t.Errorf("%s: %v", p.id, err)
		}
	}
}

func TestWikiRevisionsSendOnlyPage(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, wikiServer(""))
		got, err := e.invoke(t, wikiRevisionsList, "repo", `{"page_name":"a/b"}`)
		if err != nil || got["has_more"] != true || got["count"] != float64(120) || got["next_cursor"] == nil {
			t.Fatalf("%s: %v %v", p.id, got, err)
		}
		c := (*e.calls)[0]
		if c.query != "page=1" || c.escaped != wikiBase+"revisions/a%2Fb" {
			t.Errorf("%s sent %q %q", p.id, c.escaped, c.query)
		}
		next, _ := got["next_cursor"].(string)
		if _, err := e.invoke(t, wikiRevisionsList, "repo", `{"page_name":"a/b","cursor":"`+next+`"}`); err != nil ||
			(*e.calls)[1].query != "page=2" {
			t.Errorf("%s continuation: %v %q", p.id, err, (*e.calls)[1].query)
		}
		if _, err := e.invoke(t, wikiRevisionsList, "repo", `{"page_name":"a/b","limit":5}`); err == nil {
			t.Errorf("%s accepted limit", p.id)
		}
		if _, err := e.invoke(t, wikiRevisionsList, "repo", `{"page_name":"other","cursor":"`+next+`"}`); err == nil {
			t.Errorf("%s accepted a cursor of another page", p.id)
		}
	}
}

func TestWikiWritesSendBase64(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, wikiServer(""))
		got, err := e.invoke(t, wikiPagesCreate, "repo", `{"title":"guide/setup","content":"# Hi ä","message":"add"}`)
		if err != nil || got["sub_url"] != "guide%2Fsetup" || got["content"] != nil || got["content_base64"] != nil {
			t.Fatalf("%s create: %v %v", p.id, got, err)
		}
		var body map[string]string
		_ = json.Unmarshal([]byte((*e.calls)[0].body), &body)
		if (*e.calls)[0].method != http.MethodPost || (*e.calls)[0].path != wikiBase+"new" ||
			body["title"] != "guide/setup" || body["message"] != "add" || body["content_base64"] != b64("# Hi ä") {
			t.Errorf("%s create sent %+v", p.id, (*e.calls)[0])
		}
		if _, err := e.invoke(t, wikiPagesUpdate, "repo", `{"page_name":"guide/setup","title":"new","content":"x"}`); err != nil {
			t.Fatalf("%s update: %v", p.id, err)
		}
		u := (*e.calls)[1]
		if u.method != http.MethodPatch || u.escaped != wikiBase+"page/guide%2Fsetup" ||
			u.body != `{"content_base64":"`+b64("x")+`","title":"new"}` {
			t.Errorf("%s update sent %+v", p.id, u)
		}
		before := len(*e.calls)
		for tl, args := range map[*tool]string{
			wikiPagesCreate: `{"title":"t","content":"x","message":"a\u0001"}`,
			wikiPagesUpdate: `{"page_name":"..","content":"x"}`,
		} {
			if _, err := e.invoke(t, tl, "repo", args); err == nil {
				t.Errorf("%s accepted %s", p.id, args)
			}
		}
		if _, err := e.invokeAs(t, wikiPagesCreate, "repo", `{"title":"t","content":"x"}`, false); err == nil ||
			len(*e.calls) != before {
			t.Errorf("%s: create without confirm: %v", p.id, err)
		}
	}
}

func TestWikiDeleteIsListedOnlyAndInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		d, _, ok := reg.Lookup(p.toolID(wikiPagesDelete))
		if !ok || !d.RequiresToolAllowList || d.Risk.Confirmation != "required" || d.Risk.Effect != "delete" {
			t.Fatalf("%s descriptor = %+v", p.id, d)
		}
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, profile := range metadata.Profiles {
			if slices.Contains(profile.Tools, d.ID) {
				t.Errorf("%s: profile %s holds %s", p.id, profile.ID, d.ID)
			}
		}
		e := newEnvironment(t, p, wikiServer(""))
		if _, err := e.invoke(t, wikiPagesDelete, "repo", `{"page_name":"Home"}`); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: delete without a tools list: %v", p.id, err)
		}
		if _, err := e.invokeAs(t, wikiPagesDelete, "wiki-delete", `{"page_name":"Home"}`, false); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed delete: %v", p.id, err)
		}
		got, err := e.invoke(t, wikiPagesDelete, "wiki-delete", `{"page_name":"a/b"}`)
		if err != nil || got["deleted"] != true || (*e.calls)[0].method != http.MethodDelete ||
			(*e.calls)[0].escaped != wikiBase+"page/a%2Fb" {
			t.Errorf("%s delete: %v %v %+v", p.id, got, err, *e.calls)
		}
	}
}

func TestWikiTargetsAndNoRetry(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, wikiServer(""))
		for tl, args := range map[*tool]string{
			wikiPagesList:     `{"repository":"other/place"}`,
			wikiPagesGet:      `{"repository":"other/place","page_name":"a"}`,
			wikiPagesCreate:   `{"repository":"other/place","title":"a","content":"x"}`,
			wikiPagesUpdate:   `{"repository":"other/place","page_name":"a","content":"x"}`,
			wikiRevisionsList: `{"repository":"other/place","page_name":"a"}`,
		} {
			if _, err := e.invoke(t, tl, "repo", args); !isInvalidRequest(err) {
				t.Errorf("%s %s: %v", p.id, tl.action, err)
			}
		}
		if _, err := e.invoke(t, wikiPagesDelete, "wiki-delete", `{"repository":"other/place","page_name":"a"}`); !isInvalidRequest(err) {
			t.Errorf("%s delete: %v", p.id, err)
		}
		if len(*e.calls) != 0 || *e.reads != 0 {
			t.Errorf("%s: refusals did I/O", p.id)
		}
		f := newEnvironment(t, p, func(call) *http.Response { return jsonResponse(503, `{"message":"`+bodyCanary+`"}`) })
		_, err := f.invoke(t, wikiPagesCreate, "repo", `{"title":"a","content":"x"}`)
		if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*f.calls) != 1 {
			t.Errorf("%s 503: %v %d", p.id, err, len(*f.calls))
		}
	}
}

func TestWikiRevisionsEndAtTheReportedTotalWhateverTheBatchSize(t *testing.T) {
	for _, header := range []bool{true, false} {
		for _, p := range products {
			e := newEnvironment(t, p, func(c call) *http.Response {
				body := `{"commits":[{"sha":"abc","message":"m","commiter":{"name":"octo"}}],"count":2}`
				if header {
					return jsonResponse(200, body, "X-Total-Count", "2")
				}
				return jsonResponse(200, body)
			})
			first, err := e.invoke(t, wikiRevisionsList, "repo", `{"page_name":"Home"}`)
			next, _ := first["next_cursor"].(string)
			if err != nil || first["has_more"] != true || next == "" {
				t.Fatalf("%s header %v: %v %v", p.id, header, first, err)
			}
			last, err := e.invoke(t, wikiRevisionsList, "repo", `{"page_name":"Home","cursor":"`+next+`"}`)
			if err != nil || last["has_more"] != false || last["next_cursor"] != nil || (*e.calls)[1].query != "page=2" {
				t.Errorf("%s header %v: %v %v", p.id, header, last, err)
			}
		}
	}
}
