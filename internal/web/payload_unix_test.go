//go:build linux || darwin

package web

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"testing"

	"filippo.io/age"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// serveVaultProcess starts a vault process serving the encrypted vault at dir, unlocked with passphrase, the
// way a detached 'qatlas vault unlock' would. The server checks its clients by their program, which is this
// test binary on both ends.
func serveVaultProcess(t *testing.T, dir, passphrase string) (*vaultproc.Server, *vaultproc.Client) {
	t.Helper()
	// t.TempDir() nests the full test name, long enough to overflow a socket path.
	runtimeDir, err := os.MkdirTemp("", "qv")
	if err != nil {
		t.Fatalf("MkdirTemp() = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	v := vault.New(dir)
	if _, err := v.Unlock(passphrase); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	snap, err := v.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() = %v", err)
	}
	key, err := age.ParseX25519Identity(snap.Identity)
	if err != nil {
		t.Fatalf("ParseX25519Identity() = %v", err)
	}
	client, err := vaultproc.ProcessClientOf(v)
	if err != nil {
		t.Fatalf("ProcessClientOf() = %v", err)
	}
	l, err := vaultproc.Listen(client.Path)
	if err != nil {
		t.Fatalf("Listen() = %v", err)
	}
	server := vaultproc.NewServer(key, snap.Secrets, snap.Bindings)
	done := make(chan struct{})
	go func() { _ = server.Serve(l); close(done) }()
	t.Cleanup(func() { _ = server.Close(); <-done })
	return server, client
}

// probeScope approves a probe connection for credential in the running vault process, so the test can read
// back what the process holds.
func probeScope(t *testing.T, client *vaultproc.Client, dir, passphrase, credential string) vault.Scope {
	t.Helper()
	v := vault.New(dir)
	if _, err := v.Unlock(passphrase); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	id, ok, err := v.CredentialID(credential)
	if err != nil || !ok {
		t.Fatalf("CredentialID(%q) = %v, %v", credential, ok, err)
	}
	bindings, err := v.Bindings()
	if err != nil {
		t.Fatalf("Bindings() = %v", err)
	}
	scope := vault.Scope{Connection: "probe", Credential: credential}
	bindings.Approvals[scope.Connection] = vault.Fingerprint(scope, id)
	if err := client.Bind(context.Background(), bindings); err != nil {
		t.Fatalf("Bind() = %v", err)
	}
	return scope
}

// A field removed from a vault payload credential in the browser is forgotten by a vault process that holds
// the vault unlocked, as it is when it is removed in the terminal editor.
func TestPayloadFieldRemovedInTheWebIsForgottenByTheVaultProcess(t *testing.T) {
	dir := t.TempDir()
	v := vault.New(dir)
	if err := v.Encrypt(syntheticPassphrase); err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	_, client := serveVaultProcess(t, dir, syntheticPassphrase)

	s, _, _, _ := newCredentialTestServer(t, v)
	cookie, csrf := coupleAndApprove(t, s, v)
	form := payloadCreateForm(t, s, cookie, url.Values{"storage": {"vault"}})
	if rec := s.postForm(t, "/credentials/new/payload", s.addr, cookie, "http://"+s.addr, csrf, form); rec.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d, body: %s", rec.Code, rec.Body.String())
	}
	ctx := context.Background()
	probe := probeScope(t, client, dir, syntheticPassphrase, "shared")
	for field, want := range map[string]string{"user": payloadValueUser, "pass": payloadValuePass} {
		if got, found, err := client.Get(ctx, "shared", field, probe); err != nil || !found || got != want {
			t.Fatalf("process Get(%s) after create = found %v, err %v, want the stored value", field, found, err)
		}
	}

	page := s.request(t, http.MethodGet, "/credentials/shared", s.addr, cookie, nil)
	edit := url.Values{
		"cfgver": {extractHiddenValue(t, page.Body.String(), "cfgver")}, "remove": {"pass"},
	}
	rec := s.postForm(t, "/credentials/shared/payload", s.addr, cookie, "http://"+s.addr, csrf, edit)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/credentials/shared?saved=1" {
		t.Fatalf("edit status = %d, location %q, body: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	// Removing a field changes the credential's entry, so the probe is approved again for what is on disk now.
	probe = probeScope(t, client, dir, syntheticPassphrase, "shared")
	if _, found, err := client.Get(ctx, "shared", "pass", probe); err != nil || found {
		t.Fatalf("process Get(pass) after removal = found %v, err %v, want it forgotten", found, err)
	}
	if got, found, err := client.Get(ctx, "shared", "user", probe); err != nil || !found || got != payloadValueUser {
		t.Fatalf("process Get(user) after removal = found %v, err %v, want it kept", found, err)
	}
}
