package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
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

// TestAdminTimeoutZeroApprovesOnlyTheNextChange proves vault.admin_timeout: "0" grants exactly one
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
