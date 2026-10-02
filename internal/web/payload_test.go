package web

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// payloadValueUser and payloadValuePass stand in for payload values; the second carries characters that
// HTML, URLs and forms all treat specially, so a leak in any encoding is found by the raw and the encoded form.
const (
	payloadValueUser = "payload-user-1f3a"
	payloadValuePass = `p&ss"<w>%2F+'x;1`
)

// assertNoPayloadValue fails when text carries a value in any of the encodings a response could use.
func assertNoPayloadValue(t *testing.T, what, text string, values ...string) {
	t.Helper()
	for _, v := range values {
		for _, enc := range []string{v, url.QueryEscape(v), url.PathEscape(v)} {
			if strings.Contains(text, enc) {
				t.Fatalf("%s leaked a payload value (%q):\n%s", what, enc, text)
			}
		}
	}
}

func headersText(rec interface{ Header() http.Header }) string {
	var b strings.Builder
	for k, vs := range rec.Header() {
		b.WriteString(k + ": " + strings.Join(vs, ",") + "\n")
	}
	return b.String()
}

func payloadCreateForm(t *testing.T, s *Server, cookie *http.Cookie, extra url.Values) url.Values {
	t.Helper()
	page := s.request(t, http.MethodGet, "/credentials/new/payload", s.addr, cookie, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /credentials/new/payload status = %d", page.Code)
	}
	form := url.Values{
		"name": {"shared"}, "storage": {"keyring"}, "description": {"login of a portal"},
		"cfgver":    {extractHiddenValue(t, page.Body.String(), "cfgver")},
		"newname_0": {"user"}, "newvalue_0": {payloadValueUser},
		"newname_1": {"pass"}, "newvalue_1": {payloadValuePass},
	}
	for k, v := range extra {
		form[k] = v
	}
	return form
}

func TestCreatePayloadCredentialKeepsValuesOutOfEverythingButTheStore(t *testing.T) {
	s, store, mem, path := newCredentialTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)

	rec := s.postForm(t, "/credentials/new/payload", s.addr, cookie, "http://"+s.addr, csrf,
		payloadCreateForm(t, s, cookie, nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303, body: %s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if location != "/credentials/shared?created=1" {
		t.Fatalf("redirect = %q", location)
	}
	assertNoPayloadValue(t, "the response", rec.Body.String()+headersText(rec), payloadValueUser, payloadValuePass)

	for field, want := range map[string]string{"user": payloadValueUser, "pass": payloadValuePass} {
		got, err := mem.Get(context.Background(), secret.StoreKey("shared", field))
		if err != nil || got != want {
			t.Fatalf("store value of %s = %q, %v", field, got, err)
		}
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	cred := cfg.Credentials["shared"]
	if !cred.Forward || cred.Type != config.CredentialTypeKeyring || strings.Join(cred.Fields, ",") != "user,pass" ||
		cred.Description != "login of a portal" {
		t.Fatalf("credential = %+v", cred)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertNoPayloadValue(t, "config.yaml", string(data), payloadValueUser, payloadValuePass)

	// The edit form shows the fields and where they stand, never a value, and its masked inputs are empty.
	detail := s.request(t, http.MethodGet, location, s.addr, cookie, nil)
	body := detail.Body.String()
	assertNoPayloadValue(t, "the edit form", body+headersText(detail), payloadValueUser, payloadValuePass)
	for _, want := range []string{`type="password" name="value_user"`, `type="password" name="value_pass"`,
		"in system keyring", "Payload credential created."} {
		if !strings.Contains(body, want) {
			t.Fatalf("edit form lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `type="password" name="value_user" value=`) {
		t.Fatal("a masked input is prefilled")
	}
}

func TestPayloadValueUnderFourCharactersIsRefusedWithoutEchoing(t *testing.T) {
	s, store, mem, _ := newCredentialTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)

	const short = "a&<"
	form := payloadCreateForm(t, s, cookie, url.Values{"newvalue_1": {short}})
	rec := s.postForm(t, "/credentials/new/payload", s.addr, cookie, "http://"+s.addr, csrf, form)
	body := rec.Body.String()
	if rec.Code == http.StatusSeeOther || !strings.Contains(body, "shorter than") {
		t.Fatalf("status = %d, want a refusal, body: %s", rec.Code, body)
	}
	assertNoPayloadValue(t, "the refusal", body+headersText(rec), short, payloadValueUser)
	if strings.Contains(body, "a&amp;&lt;") {
		t.Fatal("the refusal echoes the short value")
	}
	cfg, _ := store.Load()
	if _, ok := cfg.Credentials["shared"]; ok {
		t.Fatal("a credential was saved")
	}
	if _, err := mem.Get(context.Background(), secret.StoreKey("shared", "user")); err == nil {
		t.Fatal("a value was stored")
	}
}

func TestPayloadSaveNeedsAdminSessionAndCSRF(t *testing.T) {
	v := encryptedTestVault(t)
	s, store, mem, _ := newCredentialTestServer(t, v)
	cookie, csrf := coupleAndApprove(t, s, nil) // coupled, but never admin-approved
	form := payloadCreateForm(t, s, cookie, nil)

	assertNothingSaved := func(what string) {
		t.Helper()
		cfg, _ := store.Load()
		if _, ok := cfg.Credentials["shared"]; ok {
			t.Fatalf("%s: a credential was saved", what)
		}
		if _, err := mem.Get(context.Background(), secret.StoreKey("shared", "user")); err == nil {
			t.Fatalf("%s: a value was stored", what)
		}
	}

	rec := s.postForm(t, "/credentials/new/payload", s.addr, cookie, "http://"+s.addr, csrf, form)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("without admin: status = %d, want 403", rec.Code)
	}
	assertNothingSaved("without admin")

	if err := s.VerifyAndGrantAdmin(syntheticPassphrase); err != nil {
		t.Fatal(err)
	}
	rec = s.postForm(t, "/credentials/new/payload", s.addr, cookie, "http://"+s.addr, "", form)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("without csrf: status = %d, want 403", rec.Code)
	}
	assertNothingSaved("without csrf")
	rec = s.postForm(t, "/credentials/new/payload", s.addr, nil, "http://"+s.addr, csrf, form)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("without session: status = %d, want 403", rec.Code)
	}
	assertNothingSaved("without session")
}

func TestPayloadEditKeepsEmptyValuesAndRemovesFields(t *testing.T) {
	s, store, mem, _ := newCredentialTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)
	if rec := s.postForm(t, "/credentials/new/payload", s.addr, cookie, "http://"+s.addr, csrf,
		payloadCreateForm(t, s, cookie, nil)); rec.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d", rec.Code)
	}
	page := s.request(t, http.MethodGet, "/credentials/shared", s.addr, cookie, nil)
	form := url.Values{
		"cfgver": {extractHiddenValue(t, page.Body.String(), "cfgver")}, "description": {"new text"},
		"remove": {"pass"}, "newname_0": {"token"}, "newvalue_0": {"token-value-77"},
	}
	rec := s.postForm(t, "/credentials/shared/payload", s.addr, cookie, "http://"+s.addr, csrf, form)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/credentials/shared?saved=1" {
		t.Fatalf("edit status = %d, location %q, body: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	cfg, _ := store.Load()
	cred := cfg.Credentials["shared"]
	if strings.Join(cred.Fields, ",") != "user,token" || cred.Description != "new text" {
		t.Fatalf("credential = %+v", cred)
	}
	if got, err := mem.Get(context.Background(), secret.StoreKey("shared", "user")); err != nil || got != payloadValueUser {
		t.Fatalf("kept value = %q, %v", got, err)
	}
	if _, err := mem.Get(context.Background(), secret.StoreKey("shared", "pass")); err == nil {
		t.Fatal("the stored value of the removed field is still there")
	}
	if got, err := mem.Get(context.Background(), secret.StoreKey("shared", "token")); err != nil || got != "token-value-77" {
		t.Fatalf("new value = %q, %v", got, err)
	}
}

func TestPayloadFormShowsWeakBindingNoteWithoutEncryptedVault(t *testing.T) {
	s, _, _, _ := newCredentialTestServer(t, nil)
	cookie, _ := coupleAndApprove(t, s, nil)
	body := s.request(t, http.MethodGet, "/credentials/new/payload", s.addr, cookie, nil).Body.String()
	if !strings.Contains(body, "no encrypted vault binds this release") {
		t.Fatalf("no binding note:\n%s", body)
	}

	v := encryptedTestVault(t)
	s, _, _, _ = newCredentialTestServer(t, v)
	cookie, _ = coupleAndApprove(t, s, v)
	body = s.request(t, http.MethodGet, "/credentials/new/payload", s.addr, cookie, nil).Body.String()
	if strings.Contains(body, "no encrypted vault binds this release") {
		t.Fatal("the note shows although an encrypted vault binds the release")
	}
}

// addPayloadCredential saves a payload credential and a plain one straight into the configuration of a
// connection test server.
func addPayloadCredential(t *testing.T, store *config.Store) {
	t.Helper()
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetCredential("shared", config.Credential{Type: config.CredentialTypeKeyring, Forward: true,
		Fields: []string{"user"}}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetCredential("plain-cred", config.Credential{Provider: "book", Type: config.CredentialTypeKeyring}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionOffersOnlyPayloadCredentialsAndSavesTheList(t *testing.T) {
	s, store, _, _ := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)
	addPayloadCredential(t, store)

	build := s.request(t, http.MethodGet, "/connections/new?provider=book", s.addr, cookie, nil).Body.String()
	if !strings.Contains(build, `name="forward" value="shared"`) {
		t.Fatalf("the build page does not offer the payload credential:\n%s", build)
	}
	if strings.Contains(build, `name="forward" value="plain-cred"`) {
		t.Fatal("the build page offers a plain credential as payload credential")
	}
	if strings.Contains(build, `<option value="shared"`) {
		t.Fatal("the build page offers a payload credential as the connection's own credential")
	}
	if !strings.Contains(build, "no encrypted vault binds this release") {
		t.Fatal("the build page shows no binding note")
	}

	base := url.Values{
		"provider": {"book"}, "service": {newServiceChoice}, "svcname": {"book-cloud"},
		"svcbaseurl": {"https://books.example.test"}, "credential": {"plain-cred"},
		"connname": {"book-conn"}, "permmode": {"default"}, "toolsmode": {"all"}, "forward": {"shared"},
	}
	review := s.request(t, http.MethodGet, "/connections/new/review?"+base.Encode(), s.addr, cookie, nil)
	if review.Code != http.StatusOK || !strings.Contains(review.Body.String(), "forward_secrets") {
		t.Fatalf("review status = %d:\n%s", review.Code, review.Body.String())
	}
	form := cloneValues(base)
	form.Set("cfgver", extractHiddenValue(t, review.Body.String(), "cfgver"))
	if rec := s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, form); rec.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d, body: %s", rec.Code, rec.Body.String())
	}
	cfg, _ := store.Load()
	if got := cfg.Connections["book-conn"].ForwardSecrets; len(got) != 1 || got[0] != "shared" {
		t.Fatalf("forward_secrets = %v", got)
	}

	// The list of the existing connection changes through its own form.
	page := s.request(t, http.MethodGet, "/connections/book-conn", s.addr, cookie, nil).Body.String()
	if !strings.Contains(page, `name="forward" value="shared" checked`) {
		t.Fatalf("the result page does not tick the released credential:\n%s", page)
	}
	rec := s.postForm(t, "/connections/book-conn/forward", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"cfgver": {extractHiddenValue(t, page, "cfgver")}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("forward status = %d, body: %s", rec.Code, rec.Body.String())
	}
	cfg, _ = store.Load()
	if got := cfg.Connections["book-conn"].ForwardSecrets; len(got) != 0 {
		t.Fatalf("forward_secrets = %v, want none", got)
	}

	// An unknown or plain credential is refused by the core.
	page = s.request(t, http.MethodGet, "/connections/book-conn", s.addr, cookie, nil).Body.String()
	rec = s.postForm(t, "/connections/book-conn/forward", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"cfgver": {extractHiddenValue(t, page, "cfgver")}, "forward": {"plain-cred"}})
	if rec.Code == http.StatusSeeOther {
		t.Fatal("a plain credential was accepted as payload credential")
	}
}

func TestChangedForwardListOfApprovedConnectionStaysOpen(t *testing.T) {
	s, store, v := newApprovalWebFixture(t)
	cookie, csrf := coupleAndApprove(t, s, v)

	form := payloadCreateForm(t, s, cookie, url.Values{"storage": {"vault"}})
	if rec := s.postForm(t, "/credentials/new/payload", s.addr, cookie, "http://"+s.addr, csrf, form); rec.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d, body: %s", rec.Code, rec.Body.String())
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, warning, err := approval.Approve(context.Background(), cfg, v, []string{"shelf-conn"}); err != nil || warning != "" {
		t.Fatalf("Approve: %v %q", err, warning)
	}
	report, err := approval.Pending(cfg, v)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range report.Open {
		if c.Connection == "shelf-conn" {
			t.Fatalf("shelf-conn is still open before the change: %+v", c)
		}
	}

	page := s.request(t, http.MethodGet, "/connections/shelf-conn", s.addr, cookie, nil).Body.String()
	rec := s.postForm(t, "/connections/shelf-conn/forward", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"cfgver": {extractHiddenValue(t, page, "cfgver")}, "forward": {"shared"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("forward status = %d, body: %s", rec.Code, rec.Body.String())
	}
	cfg, _ = store.Load()
	if got := cfg.Connections["shelf-conn"].ForwardSecrets; len(got) != 1 {
		t.Fatalf("forward_secrets = %v", got)
	}
	report, err = approval.Pending(cfg, v)
	if err != nil {
		t.Fatal(err)
	}
	var open bool
	for _, c := range report.Open {
		if c.Connection == "shelf-conn" {
			open = true
			if !approvalChangeHasField(c, approval.FieldForward) {
				t.Fatalf("shelf-conn is open for another reason: %+v", c)
			}
		}
	}
	if !open {
		t.Fatal("the changed release was approved in passing")
	}
	result := s.request(t, http.MethodGet, "/connections/shelf-conn?forward=1", s.addr, cookie, nil).Body.String()
	if !strings.Contains(result, "stays open") {
		t.Fatalf("the result page does not say the connection stays open:\n%s", result)
	}
}

func approvalChangeHasField(c approval.Change, field string) bool {
	for _, f := range c.Fields {
		if f.Field == field {
			return true
		}
	}
	return false
}
