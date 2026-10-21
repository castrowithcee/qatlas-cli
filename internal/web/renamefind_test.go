package web

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// findingFixture is the approval fixture with its vault unlocked and shelf-conn approved, so renames made
// behind the page's back can be set up before the session is coupled.
type findingFixture struct {
	renameFixture
	v *vault.Vault
}

func newFindingFixture(t *testing.T, admin bool) findingFixture {
	t.Helper()
	s, store, v := newApprovalWebFixture(t)
	if state, _ := v.State(); state != vault.StateUnlocked {
		if _, err := v.Unlock(syntheticPassphrase); err != nil {
			t.Fatalf("Unlock: %v", err)
		}
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := v.SetUndoableWithID("shelf-cred", cfg.Credentials["shelf-cred"].ID, "token-id", "shelf-id", nil); err != nil {
		t.Fatalf("SetUndoableWithID: %v", err)
	}
	if _, _, err := approval.Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	var cookie *http.Cookie
	var csrf string
	if admin {
		cookie, csrf = coupleAndApprove(t, s, v)
	} else {
		cookie, csrf = coupleAndApprove(t, s, nil)
	}
	return findingFixture{renameFixture{s: s, store: store, cookie: cookie, csrf: csrf, path: store.Path()}, v}
}

// renameByHand renames the entry in the configuration file only.
func (f findingFixture) renameByHand(t *testing.T, kind, old, name string) {
	t.Helper()
	cfg, err := f.store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if kind == "connection" {
		_, err = cfg.RenameConnection(old, name)
	} else {
		_, err = cfg.RenameCredential(old, name)
	}
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := f.store.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func (f findingFixture) get(t *testing.T, target string) string {
	t.Helper()
	rec := f.s.request(t, http.MethodGet, target, f.s.addr, f.cookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", target, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func (f findingFixture) config2(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := f.store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func (f findingFixture) approvals(t *testing.T) map[string]vault.Approval {
	t.Helper()
	approvals, err := f.v.Approvals()
	if err != nil {
		t.Fatalf("Approvals: %v", err)
	}
	return approvals
}

// planLink is the dry-run link a page offers for label.
func planLink(t *testing.T, page, label string) string {
	t.Helper()
	i := strings.Index(page, ">"+label+"<")
	if i < 0 {
		t.Fatalf("page offers no %q:\n%s", label, page)
	}
	j := strings.LastIndex(page[:i], `href="`)
	rest := page[j+len(`href="`):]
	href := rest[:strings.Index(rest, `"`)]
	return strings.ReplaceAll(href, "&amp;", "&")
}

func (f findingFixture) run(t *testing.T, form url.Values) *httpRecord {
	t.Helper()
	return f.s.postForm(t, findingRunPath, f.s.addr, f.cookie, "http://"+f.s.addr, f.csrf, form)
}

func (f findingFixture) runForm(t *testing.T, page string) url.Values {
	t.Helper()
	form := url.Values{}
	for _, name := range []string{"to", "kind", "from", "mode", "cfgver", "digest"} {
		form.Set(name, extractHiddenValue(t, page, name))
	}
	return form
}

func TestHandRenamedConnectionIsNoticedAndAdopted(t *testing.T) {
	f := newFindingFixture(t, true)
	f.renameByHand(t, "connection", "shelf-conn", "shelf-main")

	overview := f.get(t, "/")
	for _, want := range []string{"Renames found in the vault", "Connection shelf-main was renamed by hand from shelf-conn"} {
		if !strings.Contains(overview, want) {
			t.Errorf("overview lacks %q:\n%s", want, overview)
		}
	}
	entry := f.get(t, "/connections/shelf-main")
	if !strings.Contains(entry, "was renamed by hand from shelf-conn") {
		t.Errorf("connection page lacks the hint:\n%s", entry)
	}

	before := f.config(t)
	page := f.get(t, planLink(t, entry, "Adopt the rename"))
	if !strings.Contains(page, "Adopt the rename of connection shelf-conn to shelf-main") ||
		!strings.Contains(page, "Nothing has been changed yet") {
		t.Fatalf("no dry run:\n%s", page)
	}
	if !bytes.Equal(before, f.config(t)) {
		t.Fatal("the dry run changed the configuration")
	}
	if _, ok := f.approvals(t)["shelf-conn"]; !ok {
		t.Fatal("the dry run moved the approval")
	}

	// Leaving the dry run (the cancel link) is a plain GET: nothing was run.
	if !strings.Contains(page, `<a href="/">Cancel</a>`) {
		t.Errorf("no cancel link:\n%s", page)
	}

	rec := f.run(t, f.runForm(t, page))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/connections/shelf-main" {
		t.Fatalf("run = %d %q: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	approvals := f.approvals(t)
	if _, ok := approvals["shelf-conn"]; ok {
		t.Error("the approval is still under the old name")
	}
	if _, ok := approvals["shelf-main"]; !ok {
		t.Errorf("the approval did not follow the rename: %v", approvals)
	}
	result := f.get(t, rec.Header().Get("Location"))
	if !strings.Contains(result, "Adopted the rename of connection shelf-conn to shelf-main.") ||
		strings.Contains(result, "was renamed by hand") {
		t.Errorf("result page:\n%s", result)
	}
	assertNoCanaryIn(t, "result", result)
	for _, secret := range []string{"shelf-id", "shelf-secret"} {
		if strings.Contains(page+result+overview, secret) {
			t.Errorf("a secret value was shown")
		}
	}
}

func TestHandRenamedCredentialIsNoticedAndAdopted(t *testing.T) {
	f := newFindingFixture(t, true)
	f.renameByHand(t, "credential", "shelf-cred", "shelf-cred2")

	entry := f.get(t, "/credentials/shelf-cred2")
	if !strings.Contains(entry, "Credential shelf-cred2 was renamed by hand from shelf-cred") {
		t.Fatalf("credential page lacks the hint:\n%s", entry)
	}
	page := f.get(t, planLink(t, entry, "Adopt the rename"))
	if !strings.Contains(page, "entry shelf-cred -&gt; shelf-cred2") {
		t.Fatalf("dry run lacks the vault entry:\n%s", page)
	}
	if held, _ := f.v.HasEntry("shelf-cred"); !held {
		t.Fatal("the dry run moved the vault entry")
	}
	if rec := f.run(t, f.runForm(t, page)); rec.Code != http.StatusSeeOther {
		t.Fatalf("run = %d: %s", rec.Code, rec.Body.String())
	}
	if old, _ := f.v.HasEntry("shelf-cred"); old {
		t.Error("the vault entry is still under the old name")
	}
	if now, _ := f.v.HasEntry("shelf-cred2"); !now {
		t.Error("the vault entry did not follow the rename")
	}
}

func (f findingFixture) stopCredentialRename(t *testing.T) {
	t.Helper()
	cfg := f.config2(t)
	if _, _, err := f.v.RenameCredential(vault.CredentialRename{From: "shelf-cred", To: "shelf-cred2",
		CredentialID: cfg.Credentials["shelf-cred"].ID, Entry: true, Mark: true}); err != nil {
		t.Fatalf("RenameCredential: %v", err)
	}
}

func TestUnfinishedCredentialRenameIsFinishedOrPutBack(t *testing.T) {
	t.Run("finish", func(t *testing.T) {
		f := newFindingFixture(t, true)
		f.stopCredentialRename(t)
		entry := f.get(t, "/credentials/shelf-cred")
		if !strings.Contains(entry, "Credential shelf-cred has an unfinished rename to shelf-cred2") {
			t.Fatalf("credential page lacks the hint:\n%s", entry)
		}
		page := f.get(t, planLink(t, entry, "Finish the rename"))
		if rec := f.run(t, f.runForm(t, page)); rec.Code != http.StatusSeeOther ||
			rec.Header().Get("Location") != "/credentials/shelf-cred2" {
			t.Fatalf("run = %d %q: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
		}
		if _, ok := f.config2(t).Credentials["shelf-cred2"]; !ok {
			t.Error("the configuration was not renamed")
		}
	})

	t.Run("put back", func(t *testing.T) {
		f := newFindingFixture(t, true)
		f.stopCredentialRename(t)
		before := f.config(t)
		entry := f.get(t, "/credentials/shelf-cred")
		page := f.get(t, planLink(t, entry, "Put it back"))
		if !strings.Contains(page, "Put credential shelf-cred back") {
			t.Fatalf("no dry run:\n%s", page)
		}
		if held, _ := f.v.HasEntry("shelf-cred2"); !held {
			t.Fatal("the dry run moved the vault entry")
		}
		if rec := f.run(t, f.runForm(t, page)); rec.Code != http.StatusSeeOther ||
			rec.Header().Get("Location") != "/credentials/shelf-cred" {
			t.Fatalf("run = %d %q: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
		}
		if held, _ := f.v.HasEntry("shelf-cred"); !held {
			t.Error("the vault entry was not put back")
		}
		if held, _ := f.v.HasEntry("shelf-cred2"); held {
			t.Error("the rest is still in the vault")
		}
		if !bytes.Equal(before, f.config(t)) {
			t.Error("putting back changed the configuration")
		}
	})
}

func TestUnfinishedConnectionRenameIsNoticedAndFinished(t *testing.T) {
	f := newFindingFixture(t, true)
	id := f.config2(t).Connections["shelf-conn"].ID
	if _, _, err := f.v.CarryApproval("shelf-conn", "shelf-main", id); err != nil {
		t.Fatalf("CarryApproval: %v", err)
	}
	f.renameByHand(t, "connection", "shelf-conn", "shelf-main")

	entry := f.get(t, "/connections/shelf-main")
	if !strings.Contains(entry, "Connection shelf-main has an unfinished rename to shelf-conn") ||
		strings.Contains(entry, "Put it back") {
		t.Fatalf("connection page:\n%s", entry)
	}
	page := f.get(t, planLink(t, entry, "Finish the rename"))
	if rec := f.run(t, f.runForm(t, page)); rec.Code != http.StatusSeeOther {
		t.Fatalf("run = %d: %s", rec.Code, rec.Body.String())
	}
	if _, ok := f.config2(t).Connections["shelf-conn"]; !ok {
		t.Error("the connection did not take the name of the approval")
	}
}

func TestAmbiguousFindingIsOnlyReported(t *testing.T) {
	f := newFindingFixture(t, true)
	id := f.config2(t).Connections["shelf-conn"].ID
	if _, _, err := f.v.CarryApproval("shelf-conn", "other", id); err != nil {
		t.Fatalf("CarryApproval: %v", err)
	}
	f.renameByHand(t, "connection", "shelf-conn", "shelf-main")

	overview := f.get(t, "/")
	if !strings.Contains(overview, "nothing is suggested, so there is no action here") ||
		strings.Contains(overview, "Adopt the rename") || strings.Contains(overview, findingPlanPath) {
		t.Fatalf("overview:\n%s", overview)
	}
	before, approvals := f.config(t), len(f.approvals(t))
	for _, form := range []url.Values{
		{"kind": {"connection"}, "from": {"other"}, "to": {""}, "mode": {findingAdopt}},
		{"kind": {"connection"}, "from": {"shelf-conn"}, "to": {"shelf-main"}, "mode": {findingAdopt}},
		{"kind": {"connection"}, "from": {"other"}, "to": {"shelf-main"}, "mode": {findingAdopt}},
	} {
		if rec := f.run(t, form); rec.Code != http.StatusConflict {
			t.Errorf("POST %v = %d, want a refusal", form, rec.Code)
		}
		if rec := f.s.request(t, http.MethodGet, findingPlanPath+"?"+form.Encode(), f.s.addr, f.cookie, nil); rec.Code != http.StatusConflict {
			t.Errorf("GET %v = %d, want a refusal", form, rec.Code)
		}
	}
	if !bytes.Equal(before, f.config(t)) || len(f.approvals(t)) != approvals {
		t.Error("a refused request changed something")
	}
}

func TestFindingActionNeedsTheAdminSession(t *testing.T) {
	f := newFindingFixture(t, false)
	f.renameByHand(t, "connection", "shelf-conn", "shelf-main")
	if state, _ := f.v.State(); state != vault.StateUnlocked {
		t.Fatalf("vault state = %v, want unlocked", state)
	}
	entry := f.get(t, "/connections/shelf-main")
	page := f.get(t, planLink(t, entry, "Adopt the rename"))
	if !strings.Contains(page, "Nothing has been changed yet") || strings.Contains(page, `action="`+findingRunPath+`"`) ||
		!strings.Contains(page, "Confirming needs the admin approval") {
		t.Fatalf("a dry run without admin offers to confirm:\n%s", page)
	}
	before := f.config(t)
	form := url.Values{"kind": {"connection"}, "from": {"shelf-conn"}, "to": {"shelf-main"}, "mode": {findingAdopt},
		"cfgver": {"x"}, "digest": {"x"}}
	if rec := f.run(t, form); rec.Code != http.StatusForbidden {
		t.Fatalf("run without admin = %d, want 403", rec.Code)
	}
	if rec := f.s.postForm(t, findingRunPath, f.s.addr, f.cookie, "", f.csrf, form); rec.Code != http.StatusForbidden {
		t.Fatalf("run without origin = %d, want 403", rec.Code)
	}
	if rec := f.s.postForm(t, findingRunPath, f.s.addr, f.cookie, "http://"+f.s.addr, "wrong", form); rec.Code != http.StatusForbidden {
		t.Fatalf("run with a wrong csrf = %d, want 403", rec.Code)
	}
	if !bytes.Equal(before, f.config(t)) {
		t.Error("a refused request changed the configuration")
	}
	if _, ok := f.approvals(t)["shelf-conn"]; !ok {
		t.Error("a refused request moved the approval")
	}
}

func TestFindingActionConflicts(t *testing.T) {
	t.Run("configuration changed", func(t *testing.T) {
		f := newFindingFixture(t, true)
		f.renameByHand(t, "connection", "shelf-conn", "shelf-main")
		page := f.get(t, planLink(t, f.get(t, "/"), "Adopt the rename"))
		form := f.runForm(t, page)
		cfg := f.config2(t)
		cfg.Connections["shelf-main"] = withDescription(cfg.Connections["shelf-main"], "changed")
		if err := f.store.Save(cfg); err != nil {
			t.Fatalf("Save: %v", err)
		}
		rec := f.run(t, form)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), errConfigChanged) {
			t.Fatalf("run = %d, want the conflict:\n%s", rec.Code, rec.Body.String())
		}
		if _, ok := f.approvals(t)["shelf-conn"]; !ok {
			t.Error("a conflicting run moved the approval")
		}
	})

	t.Run("plan changed", func(t *testing.T) {
		f := newFindingFixture(t, true)
		f.renameByHand(t, "connection", "shelf-conn", "shelf-main")
		page := f.get(t, planLink(t, f.get(t, "/"), "Adopt the rename"))
		form := f.runForm(t, page)
		form.Set("digest", "stale")
		rec := f.run(t, form)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), errRenamePlanChanged) {
			t.Fatalf("run = %d, want the conflict:\n%s", rec.Code, rec.Body.String())
		}
		if _, ok := f.approvals(t)["shelf-conn"]; !ok {
			t.Error("a conflicting run moved the approval")
		}
	})
}

func withDescription(c config.Connection, text string) config.Connection {
	c.Description = text
	return c
}

func TestFindingTextIsRedacted(t *testing.T) {
	f := newFindingFixture(t, true)
	f.renameByHand(t, "connection", "shelf-conn", "shelf-main")
	f.s.redactor.Add("shelf-conn")
	overview := f.get(t, "/")
	// Only the link carries the name, as a request parameter; the words never do.
	if strings.Contains(strings.ReplaceAll(overview, "from=shelf-conn", ""), "shelf-conn") {
		t.Errorf("a registered secret value was shown:\n%s", overview)
	}
}

func TestNoFindingsNoHint(t *testing.T) {
	f := newFindingFixture(t, true)
	if overview := f.get(t, "/"); strings.Contains(overview, "Renames found in the vault") {
		t.Errorf("overview hints at a rename with none found:\n%s", overview)
	}
}
