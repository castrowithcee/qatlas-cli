package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// renameFixture couples a session over the approval fixture (a vault credential shelf-cred read by the
// connection shelf-conn) and returns the config path, so a test can compare the file byte for byte.
type renameFixture struct {
	s      *Server
	store  *config.Store
	cookie *http.Cookie
	csrf   string
	path   string
}

func newRenameFixture(t *testing.T, admin bool) renameFixture {
	t.Helper()
	s, store, v := newApprovalWebFixture(t)
	var cookie *http.Cookie
	var csrf string
	if admin {
		cookie, csrf = coupleAndApprove(t, s, v)
	} else {
		cookie, csrf = coupleAndApprove(t, s, nil)
	}
	return renameFixture{s: s, store: store, cookie: cookie, csrf: csrf, path: store.Path()}
}

func (f renameFixture) config(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	return data
}

func (f renameFixture) plan(t *testing.T, kind, old, to string) string {
	t.Helper()
	rec := f.s.request(t, http.MethodGet, "/"+kind+"s/"+old+"/rename?to="+url.QueryEscape(to), f.s.addr, f.cookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET plan status = %d, body: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func (f renameFixture) confirmForm(t *testing.T, page, to string) url.Values {
	t.Helper()
	return url.Values{
		"to": {to}, "cfgver": {extractHiddenValue(t, page, "cfgver")}, "digest": {extractHiddenValue(t, page, "digest")},
	}
}

func (f renameFixture) confirm(t *testing.T, kind, old string, form url.Values) *httpRecord {
	t.Helper()
	return f.s.postForm(t, "/"+kind+"s/"+old+"/rename", f.s.addr, f.cookie, "http://"+f.s.addr, f.csrf, form)
}

type httpRecord = httptest.ResponseRecorder

func TestRenameCredentialPlanChangesNothingAndConfirmRenames(t *testing.T) {
	f := newRenameFixture(t, true)
	before := f.config(t)

	page := f.plan(t, "credential", "shelf-cred", "shelf-cred-2")
	for _, want := range []string{"credentials.shelf-cred -&gt; credentials.shelf-cred-2", "shelf-conn", "Vault", "Outside"} {
		if !strings.Contains(page, want) {
			t.Errorf("plan page lacks %q:\n%s", want, page)
		}
	}
	if !bytes.Equal(before, f.config(t)) {
		t.Fatal("the dry run changed the configuration file")
	}

	rec := f.confirm(t, "credential", "shelf-cred", f.confirmForm(t, page, "shelf-cred-2"))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/credentials/shelf-cred-2" {
		t.Fatalf("confirm = %d %q, body: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	cfg, err := f.store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Credentials["shelf-cred-2"]; !ok || cfg.Connections["shelf-conn"].Credential != "shelf-cred-2" {
		t.Fatalf("credential not renamed: %+v", cfg.Credentials)
	}
	if _, ok := cfg.Credentials["shelf-cred"]; ok {
		t.Fatal("old credential name still present")
	}
	result := f.s.request(t, http.MethodGet, rec.Header().Get("Location"), f.s.addr, f.cookie, nil)
	if !strings.Contains(result.Body.String(), "Renamed credential shelf-cred to shelf-cred-2.") {
		t.Fatalf("result page lacks the outcome:\n%s", result.Body.String())
	}
	for _, secret := range []string{"shelf-id", "shelf-secret"} {
		if strings.Contains(page, secret) || strings.Contains(result.Body.String(), secret) {
			t.Fatalf("a secret value was shown")
		}
	}
}

func TestRenameConnectionWarnsOfLapsedTokenAndRenames(t *testing.T) {
	f := newRenameFixture(t, true)
	if _, err := f.s.vault.CreateToken("agent-one", []string{"shelf-conn"}, nil); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	before := f.config(t)

	page := f.plan(t, "connection", "shelf-conn", "shelf-conn-2")
	if !strings.Contains(page, "agent-one") || !strings.Contains(page, "Ends the cover of agent tokens") {
		t.Fatalf("plan page lacks the token warning:\n%s", page)
	}
	if !bytes.Equal(before, f.config(t)) {
		t.Fatal("the dry run changed the configuration file")
	}
	rec := f.confirm(t, "connection", "shelf-conn", f.confirmForm(t, page, "shelf-conn-2"))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/connections/shelf-conn-2" {
		t.Fatalf("confirm = %d %q, body: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	cfg, err := f.store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Connections["shelf-conn-2"]; !ok {
		t.Fatalf("connection not renamed: %+v", cfg.Connections)
	}
	result := f.s.request(t, http.MethodGet, rec.Header().Get("Location"), f.s.addr, f.cookie, nil)
	if !strings.Contains(result.Body.String(), "Renamed connection shelf-conn to shelf-conn-2.") {
		t.Fatalf("result page lacks the outcome:\n%s", result.Body.String())
	}
}

func TestRenameWithoutAdminApprovalIsRefused(t *testing.T) {
	f := newRenameFixture(t, false)
	before := f.config(t)
	for _, kind := range []struct{ kind, old, to string }{
		{"credential", "shelf-cred", "shelf-cred-2"}, {"connection", "shelf-conn", "shelf-conn-2"},
	} {
		page := f.plan(t, kind.kind, kind.old, kind.to)
		if strings.Contains(page, `name="digest"`) {
			t.Fatalf("%s: the plan offers a confirmation without admin approval", kind.kind)
		}
		rec := f.confirm(t, kind.kind, kind.old, url.Values{"to": {kind.to}, "cfgver": {"x"}, "digest": {"x"}})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403", kind.kind, rec.Code)
		}
	}
	if !bytes.Equal(before, f.config(t)) {
		t.Fatal("the configuration changed")
	}
}

func TestRenameConfirmGuards(t *testing.T) {
	f := newRenameFixture(t, true)
	before := f.config(t)
	page := f.plan(t, "credential", "shelf-cred", "shelf-cred-2")
	form := f.confirmForm(t, page, "shelf-cred-2")
	target := "/credentials/shelf-cred/rename"
	origin := "http://" + f.s.addr

	if rec := f.s.postForm(t, target, f.s.addr, f.cookie, origin, "", form); rec.Code != http.StatusForbidden {
		t.Errorf("missing csrf: status = %d, want 403", rec.Code)
	}
	if rec := f.s.postForm(t, target, f.s.addr, f.cookie, origin, "wrong", form); rec.Code != http.StatusForbidden {
		t.Errorf("wrong csrf: status = %d, want 403", rec.Code)
	}
	if rec := f.s.postForm(t, target, f.s.addr, f.cookie, "", f.csrf, form); rec.Code != http.StatusForbidden {
		t.Errorf("missing origin: status = %d, want 403", rec.Code)
	}
	if rec := f.s.postForm(t, target, f.s.addr, nil, origin, f.csrf, form); rec.Code != http.StatusForbidden {
		t.Errorf("missing session: status = %d, want 403", rec.Code)
	}
	if rec := f.s.request(t, http.MethodDelete, target, f.s.addr, f.cookie, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE: status = %d, want 405", rec.Code)
	}
	// A GET on the route only plans: it never renames, even with a complete form in its query.
	rec := f.s.request(t, http.MethodGet, target+"?to=shelf-cred-2&"+form.Encode(), f.s.addr, f.cookie, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("GET: status = %d, want 200", rec.Code)
	}
	if !bytes.Equal(before, f.config(t)) {
		t.Fatal("the configuration changed")
	}
}

func TestRenameShowsCoreErrors(t *testing.T) {
	f := newRenameFixture(t, true)
	before := f.config(t)
	if page := f.plan(t, "connection", "shelf-conn", "shelf-conn"); !strings.Contains(page, `role="alert"`) {
		t.Errorf("a taken name shows no error:\n%s", page)
	}
	if page := f.plan(t, "credential", "shelf-cred", "(bad)"); !strings.Contains(page, `role="alert"`) ||
		strings.Contains(page, `name="digest"`) {
		t.Errorf("an invalid name shows no error or offers confirmation:\n%s", page)
	}
	if page := f.plan(t, "credential", "shelf-cred", ""); !strings.Contains(page, "type the new name") {
		t.Errorf("an empty name shows no hint:\n%s", page)
	}
	if rec := f.s.request(t, http.MethodGet, "/credentials/nope/rename?to=x", f.s.addr, f.cookie, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown credential: status = %d, want 404", rec.Code)
	}
	// A confirmation for an invalid name shows the core's refusal and writes nothing.
	rec := f.confirm(t, "credential", "shelf-cred", url.Values{"to": {"(bad)"}, "cfgver": {extractCfgver(t, f)}, "digest": {"x"}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `role="alert"`) {
		t.Errorf("confirm with a bad name: %d\n%s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(before, f.config(t)) {
		t.Fatal("the configuration changed")
	}
}

func extractCfgver(t *testing.T, f renameFixture) string {
	t.Helper()
	fp, err := f.s.configFingerprint()
	if err != nil {
		t.Fatalf("configFingerprint: %v", err)
	}
	return fp
}

func TestRenameConflictOnChangedConfigOrPlan(t *testing.T) {
	for _, kind := range []struct{ kind, old, to string }{
		{"credential", "shelf-cred", "shelf-cred-2"}, {"connection", "shelf-conn", "shelf-conn-2"},
	} {
		t.Run(kind.kind, func(t *testing.T) {
			f := newRenameFixture(t, true)
			page := f.plan(t, kind.kind, kind.old, kind.to)
			form := f.confirmForm(t, page, kind.to)

			cfg, err := f.store.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if err := cfg.SetProviderNote("book", "changed after the plan"); err != nil {
				t.Fatalf("SetProviderNote: %v", err)
			}
			if err := f.store.Save(cfg); err != nil {
				t.Fatalf("Save: %v", err)
			}
			changed := f.config(t)
			rec := f.confirm(t, kind.kind, kind.old, form)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "changed since this form was opened") {
				t.Fatalf("no conflict reported: %d\n%s", rec.Code, rec.Body.String())
			}
			if !bytes.Equal(changed, f.config(t)) {
				t.Fatal("a conflicting confirmation changed the configuration")
			}

			if kind.kind != "connection" {
				return
			}
			// The file is as the form saw it, but what the rename touches differs: a token appears.
			fresh := f.plan(t, kind.kind, kind.old, kind.to)
			stale := f.confirmForm(t, fresh, kind.to)
			if _, err := f.s.vault.CreateToken("late-agent", []string{kind.old}, nil); err != nil {
				t.Fatalf("CreateToken: %v", err)
			}
			rec = f.confirm(t, kind.kind, kind.old, stale)
			if !strings.Contains(rec.Body.String(), "what the rename touches changed") {
				t.Fatalf("a changed plan was not refused: %d\n%s", rec.Code, rec.Body.String())
			}
		})
	}
}
