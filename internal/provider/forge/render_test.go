package forge

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func htmlResponse(body string) *http.Response {
	response := jsonResponse(http.StatusOK, body)
	response.Header.Set("Content-Type", "text/html")
	return response
}

func renderServer(html string) func(call) *http.Response {
	return func(c call) *http.Response {
		if c.method == http.MethodPost {
			return htmlResponse(html)
		}
		return notFoundResponse()
	}
}

func TestMarkdownRenderPostsOnlyTheDocumentedMembers(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, renderServer("<h1>Title</h1>"))
		result, err := e.invoke(t, markdownRender, "repo",
			`{"text":"# Title","mode":"markdown","repository":"acme/widget"}`)
		if err != nil || result["html"] != "<h1>Title</h1>" || result["truncated"] != nil {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		sent := (*e.calls)[0]
		var body map[string]any
		_ = json.Unmarshal([]byte(sent.body), &body)
		wantContext := "/acme/widget"
		if p == forgejo {
			wantContext = baseURL + "/acme/widget"
		}
		if len(*e.calls) != 1 || sent.method != http.MethodPost || sent.path != "/api/v1/markdown" ||
			sent.contentType != "application/json" || body["Text"] != "# Title" || body["Mode"] != "markdown" ||
			body["Context"] != wantContext || len(body) != 3 || sent.query != "" {
			t.Errorf("%s: call %+v", p.id, sent)
		}
	}
}

func TestMarkdownRenderWithoutRepositoryHasNoContext(t *testing.T) {
	e := newEnvironment(t, gitea, renderServer("<p>x</p>"))
	if _, err := e.invoke(t, markdownRender, "repo", `{"text":"x"}`); err != nil {
		t.Fatal(err)
	}
	if body := (*e.calls)[0].body; body != `{"Text":"x"}` {
		t.Errorf("body = %s", body)
	}
}

func TestMarkdownRawSendsPlainTextToTheRawEndpoint(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, renderServer("<p>raw</p>"))
		result, err := e.invoke(t, markdownRender, "open", `{"text":"raw *text*","raw":true}`)
		sent := (*e.calls)[0]
		if err != nil || result["html"] != "<p>raw</p>" || sent.path != "/api/v1/markdown/raw" ||
			sent.method != http.MethodPost || sent.body != "raw *text*" || !strings.HasPrefix(sent.contentType, "text/plain") {
			t.Errorf("%s: %v, %v, %+v", p.id, result, err, sent)
		}
		for _, arguments := range []string{`{"text":"x","raw":true,"mode":"gfm"}`,
			`{"text":"x","raw":true,"repository":"acme/widget"}`} {
			before := len(*e.calls)
			if _, err := e.invoke(t, markdownRender, "open", arguments); !isInvalidRequest(err) || len(*e.calls) != before {
				t.Errorf("%s %s: %v", p.id, arguments, err)
			}
		}
	}
}

func TestMarkupRenderPostsToItsFixedPathWithProductMembers(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, renderServer("<p>m</p>"))
		arguments := `{"text":"x","mode":"file","file_path":"docs/readme.md","repository":"acme/widget"}`
		if p == forgejo {
			arguments = `{"text":"x","mode":"file","file_path":"docs/readme.md","branch_path":"src/branch/main",` +
				`"wiki":true,"repository":"acme/widget"}`
		}
		if _, err := e.invoke(t, markupRender, "repo", arguments); err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		sent := (*e.calls)[0]
		var body map[string]any
		_ = json.Unmarshal([]byte(sent.body), &body)
		if sent.path != "/api/v1/markup" || sent.method != http.MethodPost || body["FilePath"] != "docs/readme.md" ||
			body["Mode"] != "file" {
			t.Errorf("%s: %+v", p.id, sent)
		}
		_, hasBranch := body["BranchPath"]
		_, hasWiki := body["Wiki"]
		if hasBranch != (p == forgejo) || hasWiki != (p == forgejo) {
			t.Errorf("%s: body %v", p.id, body)
		}
	}
}

// Members of the other product, and modes it does not document, are refused before the token.
func TestRenderRefusesUndocumentedArgumentsBeforeTheToken(t *testing.T) {
	cases := []struct {
		p         *product
		tool      *tool
		arguments string
	}{
		{gitea, markdownRender, `{"text":"x","wiki":true}`},
		{gitea, markupRender, `{"text":"x","branch_path":"b"}`},
		{gitea, markdownRender, `{"text":"x","mode":"gfm"}`},
		{forgejo, markdownRender, `{"text":"x","mode":"wiki"}`},
		{forgejo, markdownRender, `{"text":"x","mode":"file"}`},
		{forgejo, markdownRender, `{"text":"x","file_path":"a.md"}`},
		{forgejo, markupRender, `{"text":"x","raw":true}`},
		{forgejo, markdownRender, `{"text":""}`},
		{forgejo, markupRender, `{"text":"x","file_path":"a\nb"}`},
		{forgejo, markdownRender, `{"text":"x","context":"/acme/widget"}`},
		{forgejo, markdownRender, `{"text":"x","repository":"a/b/c"}`},
	}
	for _, tc := range cases {
		e := newEnvironment(t, tc.p, renderServer("<p>x</p>"))
		_, err := e.invoke(t, tc.tool, "open", tc.arguments)
		if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != 0 {
			t.Errorf("%s %s %s: %v, calls %d, reads %d", tc.p.id, tc.tool.group, tc.arguments, err, len(*e.calls), *e.reads)
		}
	}
}

func TestRenderTextIsBounded(t *testing.T) {
	e := newEnvironment(t, gitea, renderServer("<p>x</p>"))
	long, _ := json.Marshal(map[string]string{"text": strings.Repeat("a", maxRenderText+1)})
	if _, err := e.invoke(t, markdownRender, "open", string(long)); !isInvalidRequest(err) || len(*e.calls) != 0 {
		t.Errorf("err = %v, calls %d", err, len(*e.calls))
	}
	big := strings.Repeat("é", maxRenderedHTML) // twice the limit in bytes
	e = newEnvironment(t, gitea, renderServer(big))
	result, err := e.invoke(t, markdownRender, "open", `{"text":"x"}`)
	html, _ := result["html"].(string)
	if err != nil || result["truncated"] != true || len(html) > maxRenderedHTML || !strings.HasPrefix(html, "é") {
		t.Errorf("truncated render: %v, len %d", err, len(html))
	}
}

// A repository outside the targets is refused before the token and without being named.
func TestRenderRepositoryMustLieInsideTheTargets(t *testing.T) {
	for _, tool := range []*tool{markdownRender, markupRender} {
		e := newEnvironment(t, forgejo, renderServer("<p>x</p>"))
		_, err := e.invoke(t, tool, "repo", `{"text":"x","repository":"Other/secret-repo"}`)
		if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != 0 || strings.Contains(err.Error(), "secret-repo") {
			t.Errorf("%s: %v, calls %d, reads %d", tool.group, err, len(*e.calls), *e.reads)
		}
		if _, err := e.invoke(t, tool, "repo", `{"text":"x","repository":"ACME/Widget"}`); err != nil {
			t.Errorf("%s: an allowed repository in another case: %v", tool.group, err)
		}
		if !slices.Equal(e.paths(), []string{"/api/v1/" + tool.group}) {
			t.Errorf("%s: paths %v", tool.group, e.paths())
		}
	}
}

func TestRenderFailuresDoNotQuoteTheProvider(t *testing.T) {
	e := newEnvironment(t, gitea, func(call) *http.Response {
		return jsonResponse(http.StatusUnprocessableEntity, `{"message":"`+bodyCanary+`"}`)
	})
	_, err := e.invoke(t, markupRender, "open", `{"text":"x"}`)
	if classOfError(err) != "provider-error" || len(*e.calls) != 1 {
		t.Errorf("err = %v, calls %d", err, len(*e.calls))
	}
}

func TestRenderRiskIsReadAndUntrusted(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		for _, tool := range []*tool{markdownRender, markupRender} {
			d, _, _ := reg.Lookup(p.toolID(tool))
			if d.Risk.Effect != "read" || d.Risk.Confirmation != "none" {
				t.Errorf("%s risk = %+v", d.ID, d.Risk)
			}
		}
	}
}
