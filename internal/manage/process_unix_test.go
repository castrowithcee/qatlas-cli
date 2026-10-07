//go:build linux || darwin

package manage

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultmigrate"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

const refusal = "the vault process refused this program"

func refuseEveryClient(net.Conn) error {
	return fmt.Errorf("%w: %s", vaultproc.ErrRefused, refusal)
}

// serveVault serves an encrypted vault in dir from this test process, the way a detached vault process
// holds it unlocked. The server checks its clients by their program, which is this test binary on both ends.
func serveVault(t *testing.T, dir string, refuse bool) *vaultproc.Client {
	t.Helper()
	base := ""
	if runtime.GOOS == "darwin" {
		base = "/tmp"
	}
	runtimeDir, err := os.MkdirTemp(base, "qm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	const passphrase = "hunter2-phrase"
	v := vault.New(dir)
	if err := v.Set("seed", "role", "seed-value-1", func(string) (string, error) { return passphrase, nil }); err != nil {
		t.Fatalf("Set() = %v", err)
	}
	v = vault.New(dir)
	if _, err := v.Unlock(passphrase); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	snap, err := v.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	key, err := age.ParseX25519Identity(snap.Identity)
	if err != nil {
		t.Fatal(err)
	}
	client, err := vaultmigrate.ProcessClientOf(v)
	if err != nil {
		t.Fatal(err)
	}
	l, err := vaultproc.Listen(client.Path)
	if err != nil {
		t.Fatal(err)
	}
	server := vaultproc.NewServer(key, snap.Secrets, snap.Bindings)
	if refuse {
		server.Verify = refuseEveryClient
	}
	done := make(chan struct{})
	go func() { _ = server.Serve(l); close(done) }()
	t.Cleanup(func() { _ = server.Close(); <-done })
	return client
}

func processFixture(t *testing.T, refuse bool) (*fixture, *vaultproc.Client) {
	t.Helper()
	f := newFixture(t)
	dir := filepath.Dir(f.path)
	client := serveVault(t, dir, refuse)
	f.secrets.vault = vault.New(dir)
	f.svc = New(f.store, f.secrets, connlog.SurfaceTUI, f.red).WithVaultProcessSupport(true)
	return f, client
}

func TestSyncVaultProcessHandsTheChangeOn(t *testing.T) {
	f, _ := processFixture(t, false)
	warning := f.svc.SyncVaultProcess(context.Background(), func(ctx context.Context, c *vaultproc.Client) error {
		return c.Set(ctx, "cred", "role", "value-1234")
	})
	if warning != "" {
		t.Errorf("SyncVaultProcess() = %q, want none", warning)
	}
}

func TestSyncVaultProcessWarnsRedacted(t *testing.T) {
	f, _ := processFixture(t, true)
	change := func(ctx context.Context, c *vaultproc.Client) error { return c.Set(ctx, "cred", "role", "value-1234") }
	plain := f.svc.SyncVaultProcess(context.Background(), change)
	const lead = "warning: the vault holds the change, but the vault process that holds it unlocked could not take it " +
		"and still answers with what it held before: "
	if !strings.HasPrefix(plain, lead) || !strings.Contains(plain, refusal) {
		t.Fatalf("SyncVaultProcess() = %q, want the warning with the process error", plain)
	}
	f.red.Add(refusal)
	redacted := f.svc.SyncVaultProcess(context.Background(), change)
	if strings.Contains(redacted, refusal) || !strings.Contains(redacted, redact.Marker) || !strings.HasPrefix(redacted, lead) {
		t.Errorf("SyncVaultProcess() = %q, want the warning redacted", redacted)
	}
}

func TestLockVaultProcess(t *testing.T) {
	t.Run("locks", func(t *testing.T) {
		f, client := processFixture(t, false)
		if got := f.svc.LockVaultProcess(context.Background(), "", "unlock it again"); got != "the vault process was locked; unlock it again" {
			t.Errorf("LockVaultProcess() = %q", got)
		}
		if _, err := client.Status(context.Background()); err == nil {
			t.Error("the vault process still answers after it was locked")
		}
	})
	t.Run("with a reason", func(t *testing.T) {
		f, _ := processFixture(t, false)
		got := f.svc.LockVaultProcess(context.Background(), "before the vault was decrypted", "unlock it again")
		if got != "the vault process was locked before the vault was decrypted; unlock it again" {
			t.Errorf("LockVaultProcess() = %q", got)
		}
	})
	t.Run("refused is a redacted warning", func(t *testing.T) {
		f, _ := processFixture(t, true)
		f.red.Add(refusal)
		got := f.svc.LockVaultProcess(context.Background(), "before the vault was decrypted", "next")
		if !strings.HasPrefix(got, "warning: the vault process could not be locked before the vault was decrypted: ") ||
			!strings.Contains(got, "it keeps the secrets it holds until it locks itself, unless you ") {
			t.Errorf("LockVaultProcess() = %q", got)
		}
		if strings.Contains(got, refusal) || !strings.Contains(got, redact.Marker) {
			t.Errorf("LockVaultProcess() = %q, want it redacted", got)
		}
	})
	t.Run("no process", func(t *testing.T) {
		f := newFixture(t)
		dir := filepath.Dir(f.path)
		if err := vault.New(dir).Set("seed", "role", "seed-value-1", func(string) (string, error) { return "hunter2-phrase", nil }); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
		f.secrets.vault = vault.New(dir)
		svc := New(f.store, f.secrets, connlog.SurfaceTUI, nil).WithVaultProcessSupport(true)
		if got := svc.LockVaultProcess(context.Background(), "", "next"); got != "" {
			t.Errorf("LockVaultProcess() without a process = %q", got)
		}
	})
}

// A vault credential's secrets are handed on to the process after the save; keyring secrets never are.
func TestCommitSecretsSyncsTheVaultProcessOnlyForVaultSecrets(t *testing.T) {
	f, _ := processFixture(t, true)
	cand, rev := f.candidate(t, "viavault")
	warning, err := f.svc.CommitSecrets(cand, rev, "viavault", true, []string{"a"}, map[string]string{"a": "value-1234"}, nil)
	if err != nil {
		t.Fatalf("CommitSecrets() = %v", err)
	}
	if !strings.HasPrefix(warning, "warning: the vault holds the change") {
		t.Errorf("warning = %q, want the vault process warning", warning)
	}
	f.red.Add(refusal)
	cand, rev = f.candidate(t, "viavault2")
	warning, err = f.svc.CommitSecrets(cand, rev, "viavault2", true, []string{"a"}, map[string]string{"a": "value-1234"}, nil)
	if err != nil || strings.Contains(warning, refusal) || !strings.Contains(warning, redact.Marker) {
		t.Errorf("CommitSecrets() = %q, %v, want the warning redacted", warning, err)
	}

	cand, rev = f.candidate(t, "viakeyring")
	warning, err = f.svc.CommitSecrets(cand, rev, "viakeyring", false, []string{"a"}, map[string]string{"a": "value-1234"}, nil)
	if err != nil || warning != "" {
		t.Errorf("CommitSecrets() keyring = %q, %v, want no vault process involvement", warning, err)
	}
	// Nothing written (no role given) for a vault credential also hands nothing on.
	cand, rev = f.candidate(t, "empty")
	warning, err = f.svc.CommitSecrets(cand, rev, "empty", true, nil, nil, nil)
	if err != nil || warning != "" {
		t.Errorf("CommitSecrets() without secrets = %q, %v", warning, err)
	}
}
