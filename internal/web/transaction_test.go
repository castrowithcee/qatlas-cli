package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const conflictText = "changed since this form was opened"

func keyringForm(t *testing.T, s *Server, cookie *http.Cookie, name string, cfgver string) url.Values {
	t.Helper()
	return url.Values{
		"provider": {"wiki"}, "name": {name}, "storage": {"keyring"}, "cfgver": {cfgver},
		"secret_token-id": {"id-" + name + "-1234"}, "secret_token-secret": {"secret-" + name + "-1234"},
	}
}

func hasSecret(mem *secret.MemoryStore, credential, role string) bool {
	_, err := mem.Get(context.Background(), secret.StoreKey(credential, role))
	return err == nil
}

// Two forms opened on the same configuration: the second commit is a conflict, the first change stays and
// the second one wrote no secret.
func TestWebSaveConflictKeepsFirstChange(t *testing.T) {
	s, store, mem, _ := newCredentialTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)
	cfgverA := extractHiddenValue(t, s.request(t, http.MethodGet, "/credentials/new?provider=wiki", s.addr, cookie, nil).Body.String(), "cfgver")
	cfgverB := extractHiddenValue(t, s.request(t, http.MethodGet, "/credentials/new?provider=wiki", s.addr, cookie, nil).Body.String(), "cfgver")

	if rec := s.postForm(t, "/credentials/new", s.addr, cookie, "http://"+s.addr, csrf,
		keyringForm(t, s, cookie, "wiki-a", cfgverA)); rec.Code != http.StatusSeeOther {
		t.Fatalf("first status = %d, body: %s", rec.Code, rec.Body.String())
	}
	rec := s.postForm(t, "/credentials/new", s.addr, cookie, "http://"+s.addr, csrf,
		keyringForm(t, s, cookie, "wiki-b", cfgverB))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), conflictText) {
		t.Fatalf("second status = %d, want the conflict page, body: %s", rec.Code, rec.Body.String())
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Credentials["wiki-a"]; !ok {
		t.Errorf("credentials = %v, want wiki-a kept", cfg.Credentials)
	}
	if _, ok := cfg.Credentials["wiki-b"]; ok {
		t.Errorf("credentials = %v, want wiki-b not saved", cfg.Credentials)
	}
	if !hasSecret(mem, "wiki-a", "token-id") || hasSecret(mem, "wiki-b", "token-id") {
		t.Errorf("secrets of wiki-a/wiki-b wrong after the conflict")
	}
}

// Removing a payload field through two stale forms: the second one is a conflict and the first removal and
// the value the second one would have removed stay as they are.
func TestWebDeleteConflictKeepsFirstChange(t *testing.T) {
	s, store, mem, _ := newCredentialTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)
	if rec := s.postForm(t, "/credentials/new/payload", s.addr, cookie, "http://"+s.addr, csrf,
		payloadCreateForm(t, s, cookie, nil)); rec.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d", rec.Code)
	}
	verOf := func() string {
		return extractHiddenValue(t, s.request(t, http.MethodGet, "/credentials/shared", s.addr, cookie, nil).Body.String(), "cfgver")
	}
	verA, verB := verOf(), verOf()

	if rec := s.postForm(t, "/credentials/shared/payload", s.addr, cookie, "http://"+s.addr, csrf,
		url.Values{"cfgver": {verA}, "remove": {"pass"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("first status = %d, body: %s", rec.Code, rec.Body.String())
	}
	rec := s.postForm(t, "/credentials/shared/payload", s.addr, cookie, "http://"+s.addr, csrf,
		url.Values{"cfgver": {verB}, "remove": {"user"}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), conflictText) {
		t.Fatalf("second status = %d, want the conflict page, body: %s", rec.Code, rec.Body.String())
	}
	cfg, _ := store.Load()
	if got := strings.Join(cfg.Credentials["shared"].Fields, ","); got != "user" {
		t.Errorf("fields = %q, want only user left", got)
	}
	if !hasSecret(mem, "shared", "user") || hasSecret(mem, "shared", "pass") {
		t.Errorf("secrets after the conflict do not match the first change")
	}
}

// hookStore runs onSet after the first successful Set, which stands for a writer that replaces the
// configuration between the web handler's read and its commit.
type hookStore struct {
	*secret.MemoryStore
	onSet func()
}

func (h *hookStore) Set(key, value string) error {
	if err := h.MemoryStore.Set(key, value); err != nil {
		return err
	}
	if h.onSet != nil {
		fn := h.onSet
		h.onSet = nil
		fn()
	}
	return nil
}

// A real writer that replaces the file after the handler read it and wrote its first secret: the commit is
// a conflict, this request's secrets are rolled back and the other writer's file stays.
func TestWebSaveForeignWriterBetweenReadAndCommit(t *testing.T) {
	s, store, mem, path := newCredentialTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)
	cfgver := extractHiddenValue(t, s.request(t, http.MethodGet, "/credentials/new?provider=wiki", s.addr, cookie, nil).Body.String(), "cfgver")

	other := config.NewStore(path, wikiCatalog{})
	foreign, err := other.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := foreign.SetCredential("wiki-foreign", config.Credential{Provider: "wiki", Type: config.CredentialTypeKeyring}); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	hook := &hookStore{MemoryStore: mem}
	hook.onSet = func() {
		if err := other.Save(foreign); err != nil {
			t.Errorf("foreign Save: %v", err)
		}
	}
	red := &redact.Redactor{}
	s.secrets = secret.NewWith(func(string) string { return "" }, hook, nil, red)
	_ = store

	rec := s.postForm(t, "/credentials/new", s.addr, cookie, "http://"+s.addr, csrf,
		keyringForm(t, s, cookie, "wiki-mine", cfgver))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), conflictText) {
		t.Fatalf("status = %d, want the conflict page, body: %s", rec.Code, rec.Body.String())
	}
	cfg, _ := store.Load()
	if _, ok := cfg.Credentials["wiki-foreign"]; !ok {
		t.Errorf("credentials = %v, want the foreign change kept", cfg.Credentials)
	}
	if _, ok := cfg.Credentials["wiki-mine"]; ok {
		t.Errorf("credentials = %v, want wiki-mine not saved", cfg.Credentials)
	}
	if hasSecret(mem, "wiki-mine", "token-id") || hasSecret(mem, "wiki-mine", "token-secret") {
		t.Errorf("a secret of the conflicting request was left behind")
	}
}
