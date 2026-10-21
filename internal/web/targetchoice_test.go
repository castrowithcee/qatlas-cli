package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func bookTargetSource(entries []config.TargetEntry, listErr error) manage.TargetSource {
	return manage.TargetSource{
		Browses: func(id string) bool { return id == "book" },
		List: func(_ context.Context, _ *config.Resolved, _ *secret.Resolver,
			req config.TargetListRequest) (config.TargetPage, error) {
			return config.TargetPage{Entries: entries, Next: "n2", ServerSearch: true}, listErr
		},
		ResolveURL: func(_ context.Context, _ *config.Resolved, _ *secret.Resolver, _, _ string) ([]config.TargetEntry, error) {
			return entries, nil
		},
	}
}

// draftForm is a draft of a new service and a new env credential, which has no unsaved secret.
func draftForm() url.Values {
	return url.Values{
		"provider": {"book"}, "service": {newServiceChoice}, "svcname": {"books"},
		"credential": {newCredentialChoice}, "credname": {"cred"}, "credstorage": {"env"},
		"envname_token-id": {"BOOK_ID"}, "envname_token-secret": {"BOOK_SECRET"},
		"kind": {"shelf"},
	}
}

func TestTargetListJSON(t *testing.T) {
	s, _, _, _ := newConnectionTestServer(t, nil)
	s.svc = s.svc.WithTargetSource(bookTargetSource([]config.TargetEntry{
		{Target: "shelf/a", Name: "<script>alert(1)</script>", Kind: "shelf", Path: "p"},
		{Target: "shelf/b c", Name: "invalid target is dropped", Kind: "shelf"},
	}, nil))
	cookie, csrf := coupleAndApprove(t, s, nil)

	rec := s.postForm(t, targetListPath, s.addr, cookie, "http://"+s.addr, csrf, draftForm())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	var got targetListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Entries) != 1 || got.Entries[0].Target != "shelf/a" || got.Next != "n2" || !got.ServerSearch {
		t.Fatalf("response = %+v", got)
	}
	// The provider's name is a JSON string and nothing else: never markup, never an unescaped angle bracket.
	if got.Entries[0].Name != "<script>alert(1)</script>" || strings.Contains(rec.Body.String(), "<script>") {
		t.Fatalf("name not carried as an escaped JSON string: %s", rec.Body.String())
	}
}

func TestTargetResolveJSON(t *testing.T) {
	s, _, _, _ := newConnectionTestServer(t, nil)
	s.svc = s.svc.WithTargetSource(bookTargetSource([]config.TargetEntry{{Target: "shelf/a", Name: "A", Kind: "shelf"}}, nil))
	cookie, csrf := coupleAndApprove(t, s, nil)

	form := draftForm()
	form.Set("url", "https://books.example.test/shelves/a")
	rec := s.postForm(t, targetResolvePath, s.addr, cookie, "http://"+s.addr, csrf, form)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"shelf/a"`) {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("headers = %v", rec.Header())
	}
}

func TestTargetEndpointsRefuseWithoutGuards(t *testing.T) {
	s, _, _, _ := newConnectionTestServer(t, nil)
	s.svc = s.svc.WithTargetSource(bookTargetSource(nil, nil))
	cookie, csrf := coupleAndApprove(t, s, nil)
	origin := "http://" + s.addr
	for _, path := range []string{targetListPath, targetResolvePath} {
		for name, rec := range map[string]*httptest.ResponseRecorder{
			"no session":   s.postForm(t, path, s.addr, nil, origin, csrf, draftForm()),
			"wrong origin": s.postForm(t, path, s.addr, cookie, "http://evil.example", csrf, draftForm()),
			"no origin":    s.postForm(t, path, s.addr, cookie, "", csrf, draftForm()),
			"no csrf":      s.postForm(t, path, s.addr, cookie, origin, "", draftForm()),
			"wrong csrf":   s.postForm(t, path, s.addr, cookie, origin, "nope", draftForm()),
			"wrong host":   s.postForm(t, path, "evil.example:80", cookie, origin, csrf, draftForm()),
		} {
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s: status = %d, want 403", path, name, rec.Code)
			}
		}
		if rec := s.request(t, http.MethodGet, path, s.addr, cookie, nil); rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET %s status = %d, want 405", path, rec.Code)
		}
	}
}

func TestTargetEndpointNeedsAdminApproval(t *testing.T) {
	v := encryptedTestVault(t)
	s, _, _, _ := newConnectionTestServer(t, v)
	s.svc = s.svc.WithTargetSource(bookTargetSource([]config.TargetEntry{{Target: "shelf/a", Name: "A", Kind: "shelf"}}, nil))
	cookie, csrf := coupleAndApprove(t, s, nil) // coupled, but not approved
	origin := "http://" + s.addr

	rec := s.postForm(t, targetListPath, s.addr, cookie, origin, csrf, draftForm())
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"admin-required"`) ||
		strings.Contains(rec.Body.String(), `"entries"`) {
		t.Fatalf("unapproved: status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if err := s.VerifyAndGrantAdmin(syntheticPassphrase); err != nil {
		t.Fatal(err)
	}
	if rec := s.postForm(t, targetListPath, s.addr, cookie, origin, csrf, draftForm()); rec.Code != http.StatusOK {
		t.Fatalf("approved: status = %d, body: %s", rec.Code, rec.Body.String())
	}
}

// With vault.admin_timeout 0 an approval covers exactly one request; a read spends it like a write.
func TestTargetReadSpendsOneShotApproval(t *testing.T) {
	v := encryptedTestVault(t)
	s, _, _, _ := newConnectionTestServer(t, v)
	s.adminTimeout = 0
	s.svc = s.svc.WithTargetSource(bookTargetSource([]config.TargetEntry{{Target: "shelf/a", Name: "A", Kind: "shelf"}}, nil))
	cookie, csrf := coupleAndApprove(t, s, v)
	origin := "http://" + s.addr

	if rec := s.postForm(t, targetListPath, s.addr, cookie, origin, csrf, draftForm()); rec.Code != http.StatusOK {
		t.Fatalf("first read status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if rec := s.postForm(t, targetListPath, s.addr, cookie, origin, csrf, draftForm()); rec.Code != http.StatusForbidden {
		t.Fatalf("second read status = %d, want 403", rec.Code)
	}
}

func TestTargetUnsavedSecretIsCodeAndText(t *testing.T) {
	s, _, _, _ := newConnectionTestServer(t, nil)
	s.svc = s.svc.WithTargetSource(bookTargetSource(nil, nil))
	cookie, csrf := coupleAndApprove(t, s, nil)

	form := draftForm()
	form.Set("credstorage", "keyring")
	rec := s.postForm(t, targetListPath, s.addr, cookie, "http://"+s.addr, csrf, form)
	var got targetErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v, body: %s", err, rec.Body.String())
	}
	want := (&manage.TargetChoiceError{Code: "unsaved-secret"}).Error()
	if got.Error.Code != "unsaved-secret" || got.Error.Message != want {
		t.Fatalf("error = %+v, want unsaved-secret / %q", got.Error, want)
	}
}

func TestTargetProviderErrorTextIsFixed(t *testing.T) {
	s, _, _, _ := newConnectionTestServer(t, nil)
	s.svc = s.svc.WithTargetSource(bookTargetSource(nil, context.DeadlineExceeded))
	cookie, csrf := coupleAndApprove(t, s, nil)
	rec := s.postForm(t, targetListPath, s.addr, cookie, "http://"+s.addr, csrf, draftForm())
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), `"timeout"`) {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
}

func TestScriptRoute(t *testing.T) {
	s, cookie, _ := coupledServer(t, nil, defaultTestAdminTimeout)
	rec := s.request(t, http.MethodGet, scriptPrefix+"target-dialog.js", s.addr, cookie, nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/javascript; charset=utf-8" ||
		rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("status = %d, headers = %v", rec.Code, rec.Header())
	}
	if rec := s.request(t, http.MethodGet, scriptPrefix+"target-dialog.js", s.addr, nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("uncoupled status = %d, want 403", rec.Code)
	}
	for _, name := range []string{"unknown.js", "..", "%2e%2e", "..%2Fserver.go", "%2Fetc%2Fpasswd", "style.css", "static", ""} {
		if rec := s.request(t, http.MethodGet, scriptPrefix+name, s.addr, cookie, nil); rec.Code != http.StatusNotFound &&
			rec.Code != http.StatusMovedPermanently && rec.Code != http.StatusTemporaryRedirect {
			t.Fatalf("GET %q status = %d, want 404 or a redirect to a cleaned path", name, rec.Code)
		}
	}
	if rec := s.request(t, http.MethodGet, "/static/js", s.addr, cookie, nil); rec.Code == http.StatusOK {
		t.Fatalf("directory listing served")
	}
}

// No page of any template set carries an inline script or an event handler attribute.
func TestNoInlineScriptInPages(t *testing.T) {
	s, _, _, _ := newConnectionTestServer(t, nil)
	s.svc = s.svc.WithTargetSource(bookTargetSource(nil, nil)) // the build page then links its script
	cookie, _ := coupleAndApprove(t, s, nil)
	handler := regexp.MustCompile(`(?i)\son[a-z]+\s*=|javascript:`)
	for _, path := range []string{
		"/", "/credentials/new", "/credentials/new/payload", "/connections/new", "/connections/new?provider=book",
		"/connections/new/review?provider=book&service=" + url.QueryEscape(newServiceChoice) + "&svcname=b&credential=" +
			url.QueryEscape(newCredentialChoice) + "&credname=c&credstorage=env&connname=n&targets=a",
	} {
		rec := s.request(t, http.MethodGet, path, s.addr, cookie, nil)
		body := rec.Body.String()
		for _, m := range regexp.MustCompile(`(?i)<script[^>]*>`).FindAllString(body, -1) {
			if !regexp.MustCompile(`(?i)\ssrc="/static/js/[^"]+"`).MatchString(m) {
				t.Fatalf("%s: inline script %q", path, m)
			}
		}
		if handler.MatchString(body) {
			t.Fatalf("%s: event handler attribute or javascript: URL in: %s", path, body)
		}
	}
}

func TestBuildPageTargetDialog(t *testing.T) {
	s, _, _, _ := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)
	get := func() string {
		rec := s.request(t, http.MethodGet, "/connections/new?provider=book&label_x=%3Cscript%3Ealert(1)%3C/script%3E&targets=x", s.addr, cookie, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		return rec.Body.String()
	}
	// No browsing source: no dialog element and no script.
	body := get()
	if strings.Contains(body, "target-dialog") || strings.Contains(body, "<script") {
		t.Fatalf("page without a target browser carries the dialog: %s", body)
	}
	if !strings.Contains(body, `name="targets"`) {
		t.Fatalf("targets field missing")
	}

	s.svc = s.svc.WithTargetSource(bookTargetSource(nil, nil))
	body = get()
	for _, want := range []string{
		`id="target-dialog"`, `data-csrf="` + csrf + `"`, `data-list-url="` + targetListPath + `"`,
		`data-resolve-url="` + targetResolvePath + `"`, `data-multiple="true"`, `data-noun="book"`,
		`data-kinds="[{&#34;name&#34;:&#34;shelf&#34;}]"`, `<script src="/static/js/target-dialog.js"></script>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("build page lacks %s in: %s", want, body)
		}
	}
	// The CSRF value is an attribute, not a field the GET form would put in the address.
	if strings.Contains(body, `name="csrf"`) {
		t.Fatalf("build page has a csrf form field")
	}
	if strings.Contains(body, "<script>alert") {
		t.Fatalf("label text not escaped: %s", body)
	}
	if strings.Count(body, "<script") != 1 {
		t.Fatalf("want exactly the one linked script")
	}
}

// The embedded scripts stay free of dynamic code, markup sinks, browser storage and absolute URLs.
func TestEmbeddedScriptsAreRestricted(t *testing.T) {
	if len(scripts) == 0 {
		t.Fatal("no embedded script")
	}
	forbidden := []string{"eval(", "new Function", "Function(", "innerHTML", "outerHTML", "insertAdjacentHTML",
		"document.write", "localStorage", "sessionStorage", "indexedDB", "document.cookie", "http:", "https:", "//"}
	for name, body := range scripts {
		for _, line := range strings.Split(string(body), "\n") {
			code := strings.TrimSpace(line)
			if strings.HasPrefix(code, "//") {
				continue // a comment line
			}
			for _, f := range forbidden {
				if strings.Contains(code, f) {
					t.Errorf("%s contains %q: %s", name, f, code)
				}
			}
		}
	}
}

func TestBuildPageCarriesParentsAndTop(t *testing.T) {
	old := bookMetadata.Target.Kinds
	bookMetadata.Target.Kinds = []config.TargetKind{
		{Name: "a", Forms: []string{"a/ID"}},
		{Name: "b", Forms: []string{"b/ID"}, Parents: []string{"a", "c"}, Top: true},
		{Name: "c", Forms: []string{"c/ID"}},
	}
	t.Cleanup(func() { bookMetadata.Target.Kinds = old })
	s, _, _, _ := newConnectionTestServer(t, nil)
	cookie, _ := coupleAndApprove(t, s, nil)
	s.svc = s.svc.WithTargetSource(bookTargetSource(nil, nil))
	rec := s.request(t, http.MethodGet, "/connections/new?provider=book", s.addr, cookie, nil)
	want := `data-kinds="[{&#34;name&#34;:&#34;a&#34;},{&#34;name&#34;:&#34;b&#34;,&#34;parents&#34;:[&#34;a&#34;,&#34;c&#34;],&#34;top&#34;:true},{&#34;name&#34;:&#34;c&#34;}]"`
	if body := rec.Body.String(); !strings.Contains(body, want) {
		t.Fatalf("build page lacks %s in: %s", want, body)
	}
}

func TestBuildPageMarksUnlistedKinds(t *testing.T) {
	old := bookMetadata.Target.Kinds
	bookMetadata.Target.Kinds = []config.TargetKind{{Name: "shelf", Forms: []string{"shelf/NAME"}, Unlisted: true}}
	t.Cleanup(func() { bookMetadata.Target.Kinds = old })
	s, _, _, _ := newConnectionTestServer(t, nil)
	cookie, _ := coupleAndApprove(t, s, nil)
	s.svc = s.svc.WithTargetSource(bookTargetSource(nil, nil))
	rec := s.request(t, http.MethodGet, "/connections/new?provider=book", s.addr, cookie, nil)
	body := rec.Body.String()
	for _, want := range []string{`id="target-dialog"`, `data-resolve-url="` + targetResolvePath + `"`,
		`data-kinds="[{&#34;name&#34;:&#34;shelf&#34;,&#34;unlisted&#34;:true}]"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("build page lacks %s in: %s", want, body)
		}
	}
}
