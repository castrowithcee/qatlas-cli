package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A provider without targets is built in the browser like any other: the build page renders, and a
// connection reviewed without a target is accepted.
func TestGuidedConnectionOfAProviderWithoutTargets(t *testing.T) {
	s, store, _, _ := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)

	build := s.request(t, http.MethodGet, "/connections/new?provider=other", s.addr, cookie, nil)
	if build.Code != http.StatusOK || !strings.Contains(build.Body.String(), `name="targets"`) {
		t.Fatalf("build page status = %d, want 200 with the target row, body: %s", build.Code, build.Body.String())
	}
	form := url.Values{
		"provider": {"other"}, "service": {newServiceChoice}, "svcname": {"other-cloud"},
		"svcbaseurl": {"https://other.example.test"},
		"credential": {newCredentialChoice}, "credname": {"other-key"}, "credstorage": {"keyring"},
		"connname": {"other-conn"}, "permmode": {"custom"}, "perm": {"read"},
	}
	review := s.request(t, http.MethodGet, "/connections/new/review?"+form.Encode(), s.addr, cookie, nil)
	if review.Code != http.StatusOK {
		t.Fatalf("review status = %d, want 200, body: %s", review.Code, review.Body.String())
	}
	form.Set("cfgver", extractHiddenValue(t, review.Body.String(), "cfgver"))
	form.Set("secret_api-key", "a-test-api-key-value")
	rec := s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d, want 303, body: %s", rec.Code, rec.Body.String())
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if conn, ok := cfg.Connections["other-conn"]; !ok || conn.Target != "" || len(conn.Targets) != 0 {
		t.Fatalf("connection = %+v (saved %v), want one without a target", conn, ok)
	}
}
