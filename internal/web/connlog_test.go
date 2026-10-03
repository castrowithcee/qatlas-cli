package web

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

func connectionLogEntries(t *testing.T, configPath string) []invokelog.Entry {
	t.Helper()
	logs := filepath.Join(vault.New(filepath.Dir(configPath)).Dir(), "logs")
	files, _ := filepath.Glob(filepath.Join(logs, "*.jsonl"))
	var entries []invokelog.Entry
	for _, name := range files {
		f, err := os.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			var e invokelog.Entry
			if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
				t.Fatal(err)
			}
			entries = append(entries, e)
		}
		f.Close()
	}
	return entries
}

func wantWebEntry(t *testing.T, e invokelog.Entry, operation, effect, connection string) {
	t.Helper()
	if e.Path != "web" || e.Operation != operation || e.Effect != effect || e.Connection != connection ||
		e.Result != "success" || e.Token != "" || e.Time.IsZero() {
		t.Errorf("entry = %+v, want web %s %s %s success", e, operation, effect, connection)
	}
}

// reviewAndCreate posts the guided form for a connection, reviewing it first for its revision.
func reviewAndCreate(t *testing.T, s *Server, cookie *http.Cookie, csrf string, form url.Values) int {
	t.Helper()
	review := s.request(t, http.MethodGet, "/connections/new/review?"+form.Encode(), s.addr, cookie, nil)
	posted := cloneValues(form)
	posted.Set("cfgver", extractHiddenValue(t, review.Body.String(), "cfgver"))
	return s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, posted).Code
}

func TestWebLogsTheCreatedConnectionOnce(t *testing.T) {
	for name, extra := range map[string]url.Values{
		"env credential": {"credstorage": {"env"}, "envname_token-id": {"BOOK_TOKEN_ID"},
			"envname_token-secret": {"BOOK_TOKEN_SECRET"}},
		"keyring credential": {"credstorage": {"keyring"}, "secret_token-id": {newConnCanaryID},
			"secret_token-secret": {newConnCanaryValue}},
	} {
		t.Run(name, func(t *testing.T) {
			s, _, _, path := newConnectionTestServer(t, nil)
			cookie, csrf := coupleAndApprove(t, s, nil)
			form := url.Values{
				"provider": {"book"}, "service": {newServiceChoice}, "svcname": {"book-cloud"},
				"svcbaseurl": {"https://books.example.test"}, "credential": {newCredentialChoice},
				"credname": {"book-reader"}, "connname": {"book-conn"}, "permmode": {"default"},
				"toolsmode": {"all"},
			}
			for k, v := range extra {
				form[k] = v
			}
			if code := reviewAndCreate(t, s, cookie, csrf, form); code != http.StatusSeeOther {
				t.Fatalf("create status = %d", code)
			}
			entries := connectionLogEntries(t, path)
			if len(entries) != 1 {
				t.Fatalf("logged %d entries, want 1", len(entries))
			}
			wantWebEntry(t, entries[0], invokelog.OperationConnectionCreate, "create", "book-conn")
			raw, _ := os.ReadFile(filepath.Join(vault.New(filepath.Dir(path)).Dir(), "logs",
				entries[0].Time.UTC().Format("2006-01-02")+".jsonl"))
			for _, value := range []string{"books.example.test", newConnCanaryID, newConnCanaryValue, "BOOK_TOKEN"} {
				if strings.Contains(string(raw), value) {
					t.Errorf("the log carries %q", value)
				}
			}
		})
	}
}

func TestWebLogsAChangedForwardList(t *testing.T) {
	s, store, _, path := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)
	addPayloadCredential(t, store)
	form := url.Values{
		"provider": {"book"}, "service": {newServiceChoice}, "svcname": {"book-cloud"},
		"svcbaseurl": {"https://books.example.test"}, "credential": {"plain-cred"},
		"connname": {"book-conn"}, "permmode": {"default"}, "toolsmode": {"all"}, "forward": {"shared"},
	}
	if code := reviewAndCreate(t, s, cookie, csrf, form); code != http.StatusSeeOther {
		t.Fatalf("create status = %d", code)
	}
	page := s.request(t, http.MethodGet, "/connections/book-conn", s.addr, cookie, nil).Body.String()
	rec := s.postForm(t, "/connections/book-conn/forward", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"cfgver": {extractHiddenValue(t, page, "cfgver")}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("forward status = %d", rec.Code)
	}
	entries := connectionLogEntries(t, path)
	if len(entries) != 2 {
		t.Fatalf("logged %d entries, want 2", len(entries))
	}
	wantWebEntry(t, entries[0], invokelog.OperationConnectionCreate, "create", "book-conn")
	wantWebEntry(t, entries[1], invokelog.OperationConnectionChange, "update", "book-conn")

	// The same list again changes nothing and logs nothing.
	page = s.request(t, http.MethodGet, "/connections/book-conn", s.addr, cookie, nil).Body.String()
	s.postForm(t, "/connections/book-conn/forward", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"cfgver": {extractHiddenValue(t, page, "cfgver")}})
	if got := len(connectionLogEntries(t, path)); got != 2 {
		t.Errorf("an unchanged list logged: %d entries", got)
	}
}

func TestWebCredentialAndConflictLogNoConnectionChange(t *testing.T) {
	s, store, _, path := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)
	addPayloadCredential(t, store)

	form := url.Values{
		"provider": {"book"}, "service": {newServiceChoice}, "svcname": {"book-cloud"},
		"svcbaseurl": {"https://books.example.test"}, "credential": {"plain-cred"},
		"connname": {"book-conn"}, "permmode": {"default"}, "toolsmode": {"all"},
	}
	review := s.request(t, http.MethodGet, "/connections/new/review?"+form.Encode(), s.addr, cookie, nil)
	stale := extractHiddenValue(t, review.Body.String(), "cfgver")
	cfg, _ := store.Load()
	if err := cfg.SetProviderNote("book", "changed after the review page was shown"); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	posted := cloneValues(form)
	posted.Set("cfgver", stale)
	if rec := s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, posted); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want the conflict page", rec.Code)
	}
	if got := connectionLogEntries(t, path); len(got) != 0 {
		t.Errorf("a conflict logged %d entries", len(got))
	}
}

func TestWebLogFailureIsAWarningAndKeepsTheSave(t *testing.T) {
	s, store, _, path := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)
	vaultDir := vault.New(filepath.Dir(path)).Dir()
	if err := os.MkdirAll(vaultDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vaultDir, "logs"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"provider": {"book"}, "service": {newServiceChoice}, "svcname": {"book-cloud"},
		"svcbaseurl": {"https://books.example.test"}, "credential": {newCredentialChoice},
		"credname": {"book-reader"}, "credstorage": {"env"}, "envname_token-id": {"BOOK_TOKEN_ID"},
		"envname_token-secret": {"BOOK_TOKEN_SECRET"}, "connname": {"book-conn"}, "permmode": {"default"},
		"toolsmode": {"all"},
	}
	if code := reviewAndCreate(t, s, cookie, csrf, form); code != http.StatusSeeOther {
		t.Fatalf("create status = %d, want the save to go through", code)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Connections["book-conn"]; !ok {
		t.Fatal("the connection was not saved")
	}
	if notice := s.takeNotice(); !strings.Contains(notice, "warning: the change was saved") {
		t.Errorf("notice = %q, want the log warning", notice)
	}
}
