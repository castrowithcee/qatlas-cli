package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// testMutation is the test-owned mutating route every admin-guard test protects with withAdminGuard,
// standing in for the credential and connection forms a later task adds; this package tests the guard
// itself, never a production write.
func testMutation(calls *int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*calls++
		w.WriteHeader(http.StatusOK)
	}
}

// TestUnapprovedMutationHasNoEffect proves the acceptance criterion directly: without any admin approval,
// a POST to a guarded route never reaches the handler that would have had an effect.
func TestUnapprovedMutationHasNoEffect(t *testing.T) {
	v := encryptedTestVault(t)
	s, cookie, csrf := coupledServer(t, v, defaultTestAdminTimeout)

	var calls int
	guarded := s.withAdminGuard(testMutation(&calls))
	rec := recordHandler(guarded, http.MethodPost, "/mutate", s.addr, cookie, "http://"+s.addr, url.Values{"csrf": {csrf}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guarded status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if calls != 0 {
		t.Fatalf("mutation ran %d times, want 0: no effect without admin approval", calls)
	}
}

func TestBrowserPassphraseApproves(t *testing.T) {
	v := encryptedTestVault(t)
	s, cookie, csrf := coupledServer(t, v, defaultTestAdminTimeout)

	var calls int
	guarded := s.withAdminGuard(testMutation(&calls))

	// Wrong passphrase: generic failure, no grant, no effect.
	wrongForm := url.Values{"passphrase": {"not-the-passphrase"}}
	authRec := s.postForm(t, "/admin", s.addr, cookie, "http://"+s.addr, csrf, wrongForm)
	if authRec.Code != http.StatusOK {
		t.Fatalf("wrong passphrase status = %d, want %d", authRec.Code, http.StatusOK)
	}
	if !strings.Contains(authRec.Body.String(), "wrong passphrase") {
		t.Fatalf("wrong passphrase body = %q, want the generic failure line", authRec.Body.String())
	}
	if strings.Contains(authRec.Body.String(), syntheticPassphrase) {
		t.Fatalf("body leaked the passphrase: %s", authRec.Body.String())
	}
	mutRec := recordHandler(guarded, http.MethodPost, "/mutate", s.addr, cookie, "http://"+s.addr, url.Values{"csrf": {csrf}})
	if mutRec.Code != http.StatusForbidden {
		t.Fatalf("mutation after wrong passphrase status = %d, want %d", mutRec.Code, http.StatusForbidden)
	}
	if calls != 0 {
		t.Fatalf("mutation ran after a wrong passphrase, want 0 calls")
	}

	// Right passphrase: grants, and the mutation now succeeds.
	rightForm := url.Values{"passphrase": {syntheticPassphrase}}
	authRec2 := s.postForm(t, "/admin", s.addr, cookie, "http://"+s.addr, csrf, rightForm)
	if authRec2.Code != http.StatusSeeOther {
		t.Fatalf("right passphrase status = %d, want %d", authRec2.Code, http.StatusSeeOther)
	}
	mutRec2 := recordHandler(guarded, http.MethodPost, "/mutate", s.addr, cookie, "http://"+s.addr, url.Values{"csrf": {csrf}})
	if mutRec2.Code != http.StatusOK {
		t.Fatalf("mutation after right passphrase status = %d, want %d", mutRec2.Code, http.StatusOK)
	}
	if calls != 1 {
		t.Fatalf("mutation ran %d times, want 1", calls)
	}
}

func TestTerminalPassphraseApproves(t *testing.T) {
	v := encryptedTestVault(t)
	s, cookie, csrf := coupledServer(t, v, defaultTestAdminTimeout)
	guarded := s.withAdminGuard(testMutation(new(int)))

	if err := s.VerifyAndGrantAdmin("not-the-passphrase"); err == nil {
		t.Fatal("wrong passphrase on the terminal path was accepted")
	}
	rec := recordHandler(guarded, http.MethodPost, "/mutate", s.addr, cookie, "http://"+s.addr, url.Values{"csrf": {csrf}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status after wrong terminal passphrase = %d, want %d", rec.Code, http.StatusForbidden)
	}

	if err := s.VerifyAndGrantAdmin(syntheticPassphrase); err != nil {
		t.Fatalf("right passphrase on the terminal path was refused: %v", err)
	}
	var calls int
	guarded2 := s.withAdminGuard(testMutation(&calls))
	rec2 := recordHandler(guarded2, http.MethodPost, "/mutate", s.addr, cookie, "http://"+s.addr, url.Values{"csrf": {csrf}})
	if rec2.Code != http.StatusOK || calls != 1 {
		t.Fatalf("status = %d, calls = %d, want 200 and 1 after right terminal passphrase", rec2.Code, calls)
	}
}

// TestTerminalPassphraseWithoutCoupledSessionRefused proves that a terminal-typed passphrase, right or
// wrong, never approves anything before a browser has coupled.
func TestTerminalPassphraseWithoutCoupledSessionRefused(t *testing.T) {
	v := encryptedTestVault(t)
	s := newTestServerWithVault(t, v, defaultTestAdminTimeout)

	if err := s.VerifyAndGrantAdmin(syntheticPassphrase); err == nil {
		t.Fatal("terminal passphrase approved with no coupled session")
	}
}

func TestAdminApprovalExpiresAndReapprovesEitherWay(t *testing.T) {
	v := encryptedTestVault(t)
	s, cookie, csrf := coupledServer(t, v, time.Minute)

	if err := s.VerifyAndGrantAdmin(syntheticPassphrase); err != nil {
		t.Fatalf("grant: %v", err)
	}
	var calls int
	guarded := s.withAdminGuard(testMutation(&calls))
	rec := recordHandler(guarded, http.MethodPost, "/mutate", s.addr, cookie, "http://"+s.addr, url.Values{"csrf": {csrf}})
	if rec.Code != http.StatusOK || calls != 1 {
		t.Fatalf("status = %d, calls = %d, want 200 and 1 right after granting", rec.Code, calls)
	}

	// Move the clock past the idle deadline the mutation itself just renewed.
	s.now = func() time.Time { return time.Now().Add(2 * time.Minute) }

	rec2 := recordHandler(guarded, http.MethodPost, "/mutate", s.addr, cookie, "http://"+s.addr, url.Values{"csrf": {csrf}})
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("status after expiry = %d, want %d", rec2.Code, http.StatusForbidden)
	}
	if calls != 1 {
		t.Fatalf("mutation ran after expiry, calls = %d, want 1", calls)
	}

	// Re-approve via the terminal path and confirm it works again.
	if err := s.VerifyAndGrantAdmin(syntheticPassphrase); err != nil {
		t.Fatalf("re-grant: %v", err)
	}
	rec3 := recordHandler(guarded, http.MethodPost, "/mutate", s.addr, cookie, "http://"+s.addr, url.Values{"csrf": {csrf}})
	if rec3.Code != http.StatusOK || calls != 2 {
		t.Fatalf("status = %d, calls = %d, want 200 and 2 after re-approval", rec3.Code, calls)
	}

	// Re-approve via the browser form as well.
	s.now = func() time.Time { return time.Now().Add(5 * time.Minute) }
	authRec := s.postForm(t, "/admin", s.addr, cookie, "http://"+s.addr, csrf, url.Values{"passphrase": {syntheticPassphrase}})
	if authRec.Code != http.StatusSeeOther {
		t.Fatalf("browser re-approval status = %d, want %d", authRec.Code, http.StatusSeeOther)
	}
	rec4 := recordHandler(guarded, http.MethodPost, "/mutate", s.addr, cookie, "http://"+s.addr, url.Values{"csrf": {csrf}})
	if rec4.Code != http.StatusOK || calls != 3 {
		t.Fatalf("status = %d, calls = %d, want 200 and 3 after browser re-approval", rec4.Code, calls)
	}
}

// TestAdminTimeoutZeroApprovesOnlyTheNextChange proves an admin session time of 0 grants exactly one
// mutation, and the one after it needs a fresh approval again.
func TestAdminTimeoutZeroApprovesOnlyTheNextChange(t *testing.T) {
	v := encryptedTestVault(t)
	s, cookie, csrf := coupledServer(t, v, 0)

	if err := s.VerifyAndGrantAdmin(syntheticPassphrase); err != nil {
		t.Fatalf("grant: %v", err)
	}
	var calls int
	guarded := s.withAdminGuard(testMutation(&calls))
	rec := recordHandler(guarded, http.MethodPost, "/mutate", s.addr, cookie, "http://"+s.addr, url.Values{"csrf": {csrf}})
	if rec.Code != http.StatusOK || calls != 1 {
		t.Fatalf("first mutation: status = %d, calls = %d, want 200 and 1", rec.Code, calls)
	}

	rec2 := recordHandler(guarded, http.MethodPost, "/mutate", s.addr, cookie, "http://"+s.addr, url.Values{"csrf": {csrf}})
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("second mutation without a fresh approval: status = %d, want %d", rec2.Code, http.StatusForbidden)
	}
	if calls != 1 {
		t.Fatalf("second mutation ran, calls = %d, want 1", calls)
	}
}

func TestUnencryptedVaultApprovesOnceCoupled(t *testing.T) {
	v := vaultAbsent(t)
	s, cookie, csrf := coupledServer(t, v, defaultTestAdminTimeout)

	var calls int
	guarded := s.withAdminGuard(testMutation(&calls))
	rec := recordHandler(guarded, http.MethodPost, "/mutate", s.addr, cookie, "http://"+s.addr, url.Values{"csrf": {csrf}})
	if rec.Code != http.StatusOK || calls != 1 {
		t.Fatalf("status = %d, calls = %d, want 200 and 1 for an unencrypted/absent vault", rec.Code, calls)
	}

	overview := s.request(t, http.MethodGet, "/", s.addr, cookie, nil)
	if !strings.Contains(overview.Body.String(), "no passphrase") {
		t.Fatalf("overview body = %q, want a clear no-encryption notice", overview.Body.String())
	}
}

func TestMissingOrWrongCSRFRefusedBeforeMutation(t *testing.T) {
	v := encryptedTestVault(t)
	s, cookie, csrf := coupledServer(t, v, defaultTestAdminTimeout)
	if err := s.VerifyAndGrantAdmin(syntheticPassphrase); err != nil {
		t.Fatalf("grant: %v", err)
	}
	var calls int
	guarded := s.withAdminGuard(testMutation(&calls))

	for _, bad := range []string{"", "not-the-real-csrf-value"} {
		rec := recordHandler(guarded, http.MethodPost, "/mutate", s.addr, cookie, "http://"+s.addr, url.Values{"csrf": {bad}})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("csrf %q: status = %d, want %d", bad, rec.Code, http.StatusForbidden)
		}
	}
	if calls != 0 {
		t.Fatalf("mutation ran with a missing or wrong csrf value, calls = %d, want 0", calls)
	}
	_ = csrf
}

func TestMissingOriginOnPostRefused(t *testing.T) {
	v := encryptedTestVault(t)
	s, cookie, csrf := coupledServer(t, v, defaultTestAdminTimeout)
	if err := s.VerifyAndGrantAdmin(syntheticPassphrase); err != nil {
		t.Fatalf("grant: %v", err)
	}
	var calls int
	guarded := s.withAdminGuard(testMutation(&calls))

	rec := recordHandler(guarded, http.MethodPost, "/mutate", s.addr, cookie, "", url.Values{"csrf": {csrf}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status without an Origin = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if calls != 0 {
		t.Fatalf("mutation ran without an Origin, calls = %d, want 0", calls)
	}
}

func TestForeignSessionOrHostRefusedBeforeMutation(t *testing.T) {
	v := encryptedTestVault(t)
	s, _, csrf := coupledServer(t, v, defaultTestAdminTimeout)
	if err := s.VerifyAndGrantAdmin(syntheticPassphrase); err != nil {
		t.Fatalf("grant: %v", err)
	}
	var calls int
	guarded := s.withAdminGuard(testMutation(&calls))

	forged := &http.Cookie{Name: sessionCookieName, Value: "not-a-real-session"}
	rec := recordHandler(guarded, http.MethodPost, "/mutate", s.addr, forged, "http://"+s.addr, url.Values{"csrf": {csrf}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("forged session: status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if calls != 0 {
		t.Fatalf("mutation ran with a forged session, calls = %d, want 0", calls)
	}
}

// TestStoppingTheProcessDiscardsAdminApproval proves that close(), which every stop of 'qatlas web' goes
// through, discards an active admin approval, not just the coupled session.
func TestStoppingTheProcessDiscardsAdminApproval(t *testing.T) {
	v := encryptedTestVault(t)
	s, _, _ := coupledServer(t, v, defaultTestAdminTimeout)
	if err := s.VerifyAndGrantAdmin(syntheticPassphrase); err != nil {
		t.Fatalf("grant: %v", err)
	}
	s.close()
	if err := s.VerifyAndGrantAdmin(syntheticPassphrase); err == nil {
		t.Fatal("admin approval survived close()")
	}
}

// browserPostOrigin mirrors the Fetch rule for the Origin a browser sends on a same-origin form POST:
// the no-referrer policy serializes it as "null", every other policy keeps the real origin.
func browserPostOrigin(t *testing.T, policy, origin string) string {
	t.Helper()
	switch policy {
	case "no-referrer":
		return "null"
	case "no-referrer-when-downgrade", "origin", "origin-when-cross-origin", "same-origin",
		"strict-origin", "strict-origin-when-cross-origin", "unsafe-url":
		return origin
	}
	t.Fatalf("unknown Referrer-Policy %q", policy)
	return ""
}

func (s *Server) adminActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.adminActiveLocked()
}

// TestBrowserFormPostKeepsOrigin proves a form POST from a real browser passes the local boundary: the
// Origin it derives from the served Referrer-Policy must be accepted, while Origin "null" never is.
func TestBrowserFormPostKeepsOrigin(t *testing.T) {
	v := encryptedTestVault(t)
	s, cookie, csrf := coupledServer(t, v, defaultTestAdminTimeout)

	page := s.request(t, http.MethodGet, "/", s.addr, cookie, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", page.Code)
	}
	origin := browserPostOrigin(t, page.Header().Get("Referrer-Policy"), "http://"+s.addr)

	post := func(origin, site string) int {
		form := url.Values{"csrf": {csrf}, "passphrase": {syntheticPassphrase}}
		req := httptest.NewRequest(http.MethodPost, "/admin", strings.NewReader(form.Encode()))
		req.Host = s.addr
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", origin)
		req.Header.Set("Sec-Fetch-Site", site)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		s.mux().ServeHTTP(rec, req)
		return rec.Code
	}

	for _, site := range []string{"cross-site", "same-origin"} {
		if code := post("null", site); code != http.StatusForbidden {
			t.Fatalf("Origin null with Sec-Fetch-Site %s: status = %d, want 403", site, code)
		}
		if s.adminActive() {
			t.Fatalf("Origin null with Sec-Fetch-Site %s granted admin approval", site)
		}
	}

	if code := post(origin, "same-origin"); code != http.StatusSeeOther {
		t.Fatalf("browser form POST with Origin %q: status = %d, want 303", origin, code)
	}
	if !s.adminActive() {
		t.Fatal("browser form POST did not grant admin approval")
	}
}

// storedAdminServer is a coupled server whose admin session time comes from the vault, as in a run.
func storedAdminServer(t *testing.T, v *vault.Vault) (*Server, *http.Cookie, string) {
	t.Helper()
	s, cookie, csrf := coupledServer(t, v, defaultTestAdminTimeout)
	s.adminTimeoutSource = nil
	return s, cookie, csrf
}

func storeAdminTimeout(t *testing.T, v *vault.Vault, d time.Duration) {
	t.Helper()
	settings := vault.DefaultSettings()
	settings.AdminTimeout = d
	if err := v.SetSettings(settings); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
}

// The admin session time is the one stored in the vault: a stored 0 approves exactly one change, a stored
// time keeps the approval open until it passes without activity.
func TestStoredAdminTimeoutApplies(t *testing.T) {
	v := encryptedTestVault(t)
	storeAdminTimeout(t, v, 0)
	s, cookie, csrf := storedAdminServer(t, v)
	if err := s.VerifyAndGrantAdmin(syntheticPassphrase); err != nil {
		t.Fatalf("grant: %v", err)
	}
	var calls int
	guarded := s.withAdminGuard(testMutation(&calls))
	post := func() int {
		return recordHandler(guarded, http.MethodPost, "/mutate", s.addr, cookie, "http://"+s.addr, url.Values{"csrf": {csrf}}).Code
	}
	if got := post(); got != http.StatusOK {
		t.Fatalf("first change with stored 0: status = %d, want 200", got)
	}
	if got := post(); got != http.StatusForbidden {
		t.Fatalf("second change with stored 0: status = %d, want 403", got)
	}

	storeAdminTimeout(t, v, time.Minute)
	if err := s.VerifyAndGrantAdmin(syntheticPassphrase); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if got := post(); got != http.StatusOK {
		t.Fatalf("change with stored 1m: status = %d, want 200", got)
	}
	if got := post(); got != http.StatusOK {
		t.Fatalf("second change with stored 1m: status = %d, want 200", got)
	}
	s.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if got := post(); got != http.StatusForbidden {
		t.Fatalf("change after the stored time: status = %d, want 403", got)
	}
}

// A locked vault has no readable settings, so the safe 0 applies; the proof unlocks it and the stored time
// applies from then on.
func TestLockedVaultAdminTimeoutIsSafeUntilProven(t *testing.T) {
	v := encryptedTestVault(t)
	storeAdminTimeout(t, v, 7*time.Minute)
	v.Forget()
	s, _, _ := storedAdminServer(t, v)
	if got := s.adminTimeout(); got != 0 {
		t.Fatalf("admin timeout of a locked vault = %v, want 0", got)
	}
	if err := s.VerifyAndGrantAdmin(syntheticPassphrase); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if got := s.adminTimeout(); got != 7*time.Minute {
		t.Fatalf("admin timeout after the proof = %v, want 7m", got)
	}
	if !s.adminActiveLocked() {
		t.Fatal("the approval is not active after the proof with a stored time of 7m")
	}
}

// An untrusted settings.age never gives more than 0, an unencrypted vault has the default.
func TestAdminTimeoutUntrustedAndUnencrypted(t *testing.T) {
	v := encryptedTestVault(t)
	storeAdminTimeout(t, v, time.Hour)
	if err := os.WriteFile(filepath.Join(v.Dir(), "settings.age"), []byte("not an age file"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _, _ := storedAdminServer(t, v)
	if got := s.adminTimeout(); got != 0 {
		t.Fatalf("admin timeout with an untrusted settings.age = %v, want 0", got)
	}
	s2, _, _ := storedAdminServer(t, vaultAbsent(t))
	if got := s2.adminTimeout(); got != vault.DefaultAdminTimeout {
		t.Fatalf("admin timeout of an unencrypted vault = %v, want the default", got)
	}
}
