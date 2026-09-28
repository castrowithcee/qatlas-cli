//go:build linux || darwin

package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// serveVaultInProcess serves the encrypted vault of the fixture in dir from this test process, the way
// 'qatlas vault unlock' hands it to a vault process, and lets every command of the test reach it. The
// server checks its clients by their program, which is this test binary on both ends.
func serveVaultInProcess(t *testing.T, dir string) (*vaultproc.Server, *vaultproc.Client) {
	t.Helper()
	original := vaultProcessSupported
	vaultProcessSupported = true
	t.Cleanup(func() { vaultProcessSupported = original })
	t.Setenv("XDG_RUNTIME_DIR", shortRuntimeDir(t))

	v := vault.New(dir)
	if _, err := v.Unlock("s3cret-phrase"); err != nil {
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
	client, err := vaultProcessClient(v)
	if err != nil {
		t.Fatalf("vaultProcessClient() = %v", err)
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

// refuseEveryClient makes a server refuse every client, as a vault process does once an update replaced
// its program.
func refuseEveryClient(net.Conn) error {
	return fmt.Errorf("%w: its program was removed or replaced since it started", vaultproc.ErrRefused)
}

// 'credential set' and 'credential delete' hand what they wrote to the vault process that holds the
// vault unlocked, so it answers with what the vault holds.
func TestCredentialChangesReachTheVaultProcess(t *testing.T) {
	dir := encryptedVaultFixture(t, "")
	_, client := serveVaultInProcess(t, dir)
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	ctx := context.Background()

	code, stdout, stderr := runWithInput(t, &Options{}, "changed-"+canaryVault+"\n",
		"credential", "set", "wiki-vault", "token-id", "--config", configIn(dir))
	if code != exitOK || stdout != "" || stderr != "" {
		t.Fatalf("set: exit code = %d, stdout = %q, stderr = %q, want a silent success", code, stdout, stderr)
	}
	if value, found, err := client.Get(ctx, "wiki-vault", "token-id", connectionScope(t, dir, "wiki")); err != nil || !found || value != "changed-"+canaryVault {
		t.Fatalf("Get() after set = %q, %v, %v, want the new secret", value, found, err)
	}

	code, stdout, stderr = runWithInput(t, &Options{}, "", "credential", "delete", "wiki-vault", "token-id",
		"--config", configIn(dir))
	if code != exitOK || stdout != "" || stderr != "" {
		t.Fatalf("delete: exit code = %d, stdout = %q, stderr = %q, want a silent success", code, stdout, stderr)
	}
	if _, found, err := client.Get(ctx, "wiki-vault", "token-id", connectionScope(t, dir, "wiki")); err != nil || found {
		t.Fatalf("Get() after delete = %v, %v, want nothing", found, err)
	}
}

// 'vault approve' hands what it approved to the vault process that holds the vault unlocked, at once: a
// connection changed by hand after the process started is refused until 'vault approve' releases it, and
// the running process itself checks the change right afterwards, without being restarted.
func TestVaultApproveSyncsARunningVaultProcess(t *testing.T) {
	dir := encryptedVaultFixture(t, "")
	_, client := serveVaultInProcess(t, dir)
	ctx := context.Background()
	scope := connectionScope(t, dir, "wiki")
	if err := client.Check(ctx, scope); err != nil {
		t.Fatalf("Check() before the change = %v, want the connection approved as it was started", err)
	}

	edited := strings.Replace(vaultCredentialConfig, "https://wiki.example.invalid", "http://127.0.0.1:9", 1)
	if err := os.WriteFile(configIn(dir), []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	changed := connectionScope(t, dir, "wiki")
	if err := client.Check(ctx, changed); !errors.Is(err, vault.ErrApprovalRequired) {
		t.Fatalf("Check() after the change = %v, want ErrApprovalRequired", err)
	}

	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "approve", "--config", configIn(dir))
	if code != exitOK || !strings.Contains(stdout, "approved 1 connection: wiki") {
		t.Fatalf("vault approve: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}

	if err := client.Check(ctx, changed); err != nil {
		t.Errorf("Check() after approve = %v, want the running vault process to check with the new approval", err)
	}
}

// A vault process that refuses the update keeps what it held, and the command says so without taking back
// what it stored.
func TestCredentialSetWarnsWhenTheVaultProcessRefuses(t *testing.T) {
	dir := encryptedVaultFixture(t, "")
	server, _ := serveVaultInProcess(t, dir)
	server.Verify = refuseEveryClient
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))

	code, _, stderr := runWithInput(t, &Options{}, "changed-"+canaryVault+"\n",
		"credential", "set", "wiki-vault", "token-id", "--config", configIn(dir))
	if code != exitOK || !strings.Contains(stderr, "qatlas: warning: the vault holds the change") ||
		!strings.Contains(stderr, "kill "+strconv.Itoa(os.Getpid())) || strings.Contains(stderr, canaryVault) {
		t.Fatalf("set: exit code = %d, stderr = %q, want a warning naming the process", code, stderr)
	}
	got, found, _, err := vault.New(dir).Get("wiki-vault", "token-id", offeringPassphrase("s3cret-phrase"))
	if err != nil || !found || got != "changed-"+canaryVault {
		t.Fatalf("the vault holds %q, %v, %v, want the stored secret kept", got, found, err)
	}
}

// 'qatlas connections' names a vault connection unusable exactly while the vault is locked, and asks for
// no passphrase to find out.
func TestConnectionsShowTheLockedVault(t *testing.T) {
	dir := encryptedVaultFixture(t, "")
	withInteractive(t, false)
	withVaultPassphrase(t, func(string) (string, error) {
		t.Errorf("the passphrase was asked for")
		return "", vault.ErrNoTerminal
	})
	original := vaultProcessSupported
	vaultProcessSupported = true
	t.Cleanup(func() { vaultProcessSupported = original })
	t.Setenv("XDG_RUNTIME_DIR", shortRuntimeDir(t))

	code, stdout, stderr := runWithInput(t, &Options{}, "", "connections", "--config", configIn(dir), "--output", "json")
	if code != exitOK || !strings.Contains(stdout, `"unusable":"vault-locked"`) {
		t.Fatalf("connections while locked: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	code, stdout, _ = runWithInput(t, &Options{}, "", "connections", "--config", configIn(dir))
	if code != exitOK || !strings.Contains(stdout, "unusable") || !strings.Contains(stdout, "vault-locked") {
		t.Fatalf("connections as TOON while locked: exit code = %d, stdout = %q", code, stdout)
	}

	serveVaultInProcess(t, dir)
	code, stdout, stderr = runWithInput(t, &Options{}, "", "connections", "--config", configIn(dir), "--output", "json")
	if code != exitOK || strings.Contains(stdout, "unusable") {
		t.Fatalf("connections while unlocked: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

// 'vault passphrase' and 'vault decrypt' lock a running vault process before they touch the vault, and say
// so; without one they say nothing about it.
func TestRekeyingLocksTheVaultProcess(t *testing.T) {
	for _, tt := range []struct {
		args []string
		ask  vault.PassphraseFunc
		want string
	}{
		{[]string{"vault", "passphrase"}, sequencedPassphrases("s3cret-phrase", "new-phrase", "new-phrase"),
			"qatlas: the vault process was locked before the passphrase changed; run 'qatlas vault unlock'"},
		{[]string{"vault", "decrypt", "--confirm"}, sequencedPassphrases("s3cret-phrase"),
			"qatlas: the vault process was locked before the vault was decrypted"},
	} {
		dir := encryptedVaultFixture(t, "")
		_, client := serveVaultInProcess(t, dir)
		withVaultPassphrase(t, tt.ask)

		code, _, stderr := runWithInput(t, &Options{}, "", append(tt.args, "--config", configIn(dir))...)
		if code != exitOK || !strings.Contains(stderr, tt.want) {
			t.Fatalf("%v: exit code = %d, stderr = %q, want %q", tt.args, code, stderr, tt.want)
		}
		if _, err := client.Status(context.Background()); !errors.Is(err, vaultproc.ErrNotRunning) {
			t.Fatalf("%v: Status() afterwards = %v, want the process locked", tt.args, err)
		}

		// A second run finds no process and says nothing about one.
		if tt.args[1] == "passphrase" {
			withVaultPassphrase(t, sequencedPassphrases("new-phrase", "third-phrase", "third-phrase"))
			code, _, stderr = runWithInput(t, &Options{}, "", append(tt.args, "--config", configIn(dir))...)
			if code != exitOK || strings.Contains(stderr, "vault process") {
				t.Fatalf("%v without a process: exit code = %d, stderr = %q", tt.args, code, stderr)
			}
		}
	}
}

// 'qatlas update' locks a running vault process right before it replaces the program, which the process
// would no longer serve, and says how to unlock again; a process that cannot be locked does not stop the
// update, and the warning names it.
func TestUpdateLocksTheVaultProcess(t *testing.T) {
	for _, refuse := range []bool{false, true} {
		dir := encryptedVaultFixture(t, "")
		server, client := serveVaultInProcess(t, dir)
		if refuse {
			server.Verify = refuseEveryClient
		}
		executable := installedProgram(t)
		updater := releaseUpdater(t, executable, func() {
			// Right before the replacement the process is locked already, or refused to be.
			if _, err := client.Status(context.Background()); refuse == errors.Is(err, vaultproc.ErrNotRunning) {
				t.Errorf("refuse=%v: Status() before the replacement = %v", refuse, err)
			}
			if data, _ := os.ReadFile(executable); string(data) != "old-program" {
				t.Errorf("the program was replaced before the vault process was locked")
			}
		})

		code, stdout, stderr := runWithInput(t, &Options{Updater: updater}, "", "update", "--config", configIn(dir),
			"--output", "json")
		if code != exitOK || !strings.Contains(stdout, `"updated":true`) {
			t.Fatalf("refuse=%v: exit code = %d, stdout = %q, stderr = %q", refuse, code, stdout, stderr)
		}
		want := "qatlas: the vault process was locked before qatlas was replaced; run 'qatlas vault unlock' to " +
			"unlock the vault again\n"
		if refuse {
			want = "qatlas: warning: the vault process could not be locked before qatlas was replaced: "
		}
		if !strings.HasPrefix(stderr, want) || (refuse && !strings.Contains(stderr, "kill "+strconv.Itoa(os.Getpid()))) {
			t.Fatalf("refuse=%v: stderr = %q, want %q", refuse, stderr, want)
		}
		if data, _ := os.ReadFile(executable); string(data) != "new-program" {
			t.Fatalf("refuse=%v: the program was not replaced", refuse)
		}
	}

	// Without a vault process nothing is said about one.
	dir := encryptedVaultFixture(t, "")
	t.Setenv("XDG_RUNTIME_DIR", shortRuntimeDir(t))
	updater := releaseUpdater(t, installedProgram(t), func() {})
	code, _, stderr := runWithInput(t, &Options{Updater: updater}, "", "update", "--config", configIn(dir))
	if code != exitOK || stderr != "" {
		t.Fatalf("update without a vault process: exit code = %d, stderr = %q", code, stderr)
	}
}

// installedProgram writes a stand-in for an installed <prefix>/bin/qatlas and returns its path.
func installedProgram(t *testing.T) string {
	t.Helper()
	executable := filepath.Join(t.TempDir(), "bin", "qatlas")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("old-program"), 0o755); err != nil {
		t.Fatal(err)
	}
	return executable
}

// releaseUpdater returns an updater for executable against a local release v1.1.0 whose program is
// "new-program". before runs right before the installed files are replaced, after the command's own hook.
func releaseUpdater(t *testing.T, executable string, before func()) *selfupdate.Client {
	t.Helper()
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	for name, content := range map[string]string{"bin/qatlas": "new-program", "share/man/man1/qatlas.1": "manual"} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(content))
	}
	_ = tw.Close()
	_ = gz.Close()
	const asset = "qatlas_v1.1.0_linux_amd64.tar.gz"
	sum := sha256.Sum256(archive.Bytes())

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.1.0", "assets": []map[string]string{
				{"name": asset, "url": server.URL + "/asset"},
				{"name": "checksums.txt", "url": server.URL + "/checksums"},
			}})
		case "/asset":
			_, _ = w.Write(archive.Bytes())
		case "/checksums":
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), asset)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return &selfupdate.Client{
		BaseURL: server.URL, HTTPClient: server.Client(), Version: "v1.0.0", GOOS: "linux", GOARCH: "amd64",
		Executable: executable, BeforeReplace: func(context.Context) { before() },
	}
}
