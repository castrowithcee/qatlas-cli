package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	releaseverify "github.com/castrowithcee/qatlas-connect/internal/release"
	"github.com/castrowithcee/qatlas-connect/internal/release/releasetest"
)

// assertAlias checks that path is a link to qconnect beside it.
func assertAlias(t *testing.T, path string) {
	t.Helper()
	if target, err := os.Readlink(path); err != nil || target != "qconnect" {
		t.Fatalf("%s links to %q (%v), want qconnect", path, target, err)
	}
}

// An installation the former updater left as bin/qatlas is updated under both names: the program goes to
// qconnect with its manual page, and qatlas becomes a link to it.
func TestUpdateOfAFormerInstallationKeepsBothNames(t *testing.T) {
	archive := releaseArchive(t, []byte("new-binary"), []byte("new-manpage"))
	server := releaseServer(t, "v3.1.0", archive, "")
	defer server.Close()
	prefix, former := installedPrefixNamed(t, "qatlas")
	client := &Client{BaseURL: server.URL, HTTPClient: server.Client(), Version: "v3.0.0", SigningKeys: testKeys(t),
		GOOS: "linux", GOARCH: "amd64", Executable: former}
	if result, err := client.Update(context.Background()); err != nil || !result.Updated {
		t.Fatalf("Update() = %+v, %v", result, err)
	}
	current := filepath.Join(prefix, "bin", "qconnect")
	assertFile(t, current, "new-binary")
	assertAlias(t, former)
	assertFile(t, former, "new-binary")
	assertFile(t, filepath.Join(prefix, "share", "man", "man1", "qconnect.1"), "new-manpage")

	// Started through the alias, the next update replaces qconnect and keeps the alias.
	archive = releaseArchive(t, []byte("newer-binary"), []byte("newer-manpage"))
	server2 := releaseServer(t, "v3.2.0", archive, "")
	defer server2.Close()
	client = &Client{BaseURL: server2.URL, HTTPClient: server2.Client(), Version: "v3.1.0", SigningKeys: testKeys(t),
		GOOS: "linux", GOARCH: "amd64", Executable: former}
	if result, err := client.Update(context.Background()); err != nil || !result.Updated {
		t.Fatalf("Update() through the alias = %+v, %v", result, err)
	}
	assertFile(t, current, "newer-binary")
	assertAlias(t, former)
}

// A link named qatlas to anything but qconnect beside it is no alias: the installation is refused.
func TestUpdateRefusesAForeignLink(t *testing.T) {
	archive := releaseArchive(t, []byte("new-binary"), []byte("new-manpage"))
	server := releaseServer(t, "v3.1.0", archive, "")
	defer server.Close()
	_, executable := installedPrefix(t)
	link := filepath.Join(filepath.Dir(executable), "qatlas")
	if err := os.Symlink(executable, link); err != nil {
		t.Fatal(err)
	}
	client := &Client{BaseURL: server.URL, HTTPClient: server.Client(), Version: "v3.0.0", SigningKeys: testKeys(t),
		GOOS: "linux", GOARCH: "amd64", Executable: link}
	var unsupported *UnsupportedInstallationError
	if _, err := client.Update(context.Background()); !errors.As(err, &unsupported) {
		t.Fatalf("Update() error = %v, want an unsupported installation", err)
	}
	assertFile(t, executable, "old-binary")
}

// A rollback to a release before the rename installs it as qatlas, in place of the alias, with its manual
// page, and takes qconnect and its manual page away: the older program does not call itself qconnect.
func TestRollbackBeforeTheRenameInstallsTheFormerName(t *testing.T) {
	signer := releasetest.NewSigner(t)
	server := multiReleaseServer(t, signer, []fixture{{tag: "v2.1.0"}}, nil)
	prefix, current := installedPrefix(t)
	former := filepath.Join(prefix, "bin", "qatlas")
	if err := os.Symlink("qconnect", former); err != nil {
		t.Fatal(err)
	}
	client := &Client{BaseURL: server.URL, HTTPClient: server.Client(), Version: "v3.0.0",
		GOOS: "linux", GOARCH: "amd64", Executable: current, SigningKeys: []ssh.PublicKey{signer.PublicKey()}}
	target, err := client.Target(context.Background(), "v2.1.0")
	if err != nil || !target.Older {
		t.Fatalf("Target() = %+v, %v", target, err)
	}
	prepared, err := client.Prepare(context.Background(), target)
	if err != nil {
		t.Fatalf("Prepare() of a release with its valid former signature = %v", err)
	}
	if result, err := client.InstallPrepared(context.Background(), prepared); err != nil || !result.Signed {
		t.Fatalf("InstallPrepared() = %+v, %v", result, err)
	}
	if info, err := os.Lstat(former); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("qatlas is %v, %v, want the older program as a file", info, err)
	}
	assertFile(t, former, "new-binary")
	assertFile(t, filepath.Join(prefix, "share", "man", "man1", "qatlas.1"), "new-manpage")
	for _, gone := range []string{current, filepath.Join(prefix, "share", "man", "man1", "qconnect.1")} {
		if _, err := os.Lstat(gone); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s remains after the rollback: %v", gone, err)
		}
	}
}

// signatureServer serves the release version, latest and by tag, with the archive of its layout, its
// checksums, and the signatures, keyed by asset name, that sigs make over the checksums.
func signatureServer(t *testing.T, version string, sigs map[string]func(checksums []byte) []byte) *httptest.Server {
	t.Helper()
	archive := programArchive(t, layoutOf(t, version).Program, []byte("new-binary"), []byte("new-manpage"))
	sum := sha256.Sum256(archive)
	checksums := []byte(hex.EncodeToString(sum[:]) + "  " + assetName(version, "linux", "amd64") + "\n")
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/releases/latest" || r.URL.Path == "/releases/tags/"+version:
			assets := []map[string]string{
				{"name": assetName(version, "linux", "amd64"), "url": server.URL + "/asset"},
				{"name": "checksums.txt", "url": server.URL + "/checksums"},
			}
			for name := range sigs {
				assets = append(assets, map[string]string{"name": name, "url": server.URL + "/sig/" + name})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": version, "assets": assets})
		case r.URL.Path == "/releases":
			_ = json.NewEncoder(w).Encode([]any{})
		case r.URL.Path == "/asset":
			_, _ = w.Write(archive)
		case r.URL.Path == "/checksums":
			_, _ = w.Write(checksums)
		case strings.HasPrefix(r.URL.Path, "/sig/") && sigs[strings.TrimPrefix(r.URL.Path, "/sig/")] != nil:
			_, _ = w.Write(sigs[strings.TrimPrefix(r.URL.Path, "/sig/")](checksums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// A release after the rename is installed on its current signature only. Leaving it out and offering the
// former one, though validly made by the trusted key, installs nothing, and neither does the former
// namespace under the current name: the version asked for decides the signature, not what the release offers.
func TestAReleaseAfterTheRenameNeedsItsCurrentSignature(t *testing.T) {
	signer := releasetest.NewSigner(t)
	former := func(c []byte) []byte { return releasetest.Sign(t, signer, releaseverify.LegacyNamespace, "sha512", c) }
	current := func(c []byte) []byte { return releasetest.Sign(t, signer, releaseverify.Namespace, "sha512", c) }
	for name, sigs := range map[string]map[string]func([]byte) []byte{
		"former signature only":               {releaseverify.LegacySignatureAsset: former},
		"former namespace as current":         {releaseverify.SignatureAsset: former, releaseverify.LegacySignatureAsset: former},
		"current namespace under former name": {releaseverify.LegacySignatureAsset: current},
	} {
		t.Run(name, func(t *testing.T) {
			server := signatureServer(t, "v3.1.0", sigs)
			_, executable := installedPrefix(t)
			client := &Client{BaseURL: server.URL, HTTPClient: server.Client(), Version: "v3.0.0",
				GOOS: "linux", GOARCH: "amd64", Executable: executable, SigningKeys: []ssh.PublicKey{signer.PublicKey()},
				BeforeReplace: func(context.Context, Release) error {
					t.Error("BeforeReplace ran for a release without its current signature")
					return nil
				}}
			if result, err := client.Update(context.Background()); result.Updated {
				t.Fatalf("Update() = %+v, %v, want nothing installed", result, err)
			}
			if target, err := client.Target(context.Background(), "v3.1.0"); err == nil {
				if _, err := client.UpdateTo(context.Background(), target); err == nil {
					t.Fatal("UpdateTo() installed the release")
				}
			}
			rel, err := client.latest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.prepareRelease(context.Background(), Result{Latest: "v3.1.0"}, rel); err == nil {
				t.Fatal("prepareRelease() accepted the release")
			}
			assertFile(t, executable, "old-binary")
		})
	}
	// With its current signature beside the former one, the same release installs.
	server := signatureServer(t, "v3.1.0", map[string]func([]byte) []byte{
		releaseverify.SignatureAsset: current, releaseverify.LegacySignatureAsset: former,
	})
	_, executable := installedPrefix(t)
	client := &Client{BaseURL: server.URL, HTTPClient: server.Client(), Version: "v3.0.0",
		GOOS: "linux", GOARCH: "amd64", Executable: executable, SigningKeys: []ssh.PublicKey{signer.PublicKey()}}
	if result, err := client.Update(context.Background()); err != nil || !result.Updated || !result.Signed {
		t.Fatalf("Update() = %+v, %v", result, err)
	}
	assertFile(t, executable, "new-binary")
}

// A release before the rename is verified under the former namespace: a rollback to it with that signature
// is accepted, one with the current namespace in its place is not.
func TestARollbackBeforeTheRenameUsesTheFormerSignature(t *testing.T) {
	signer := releasetest.NewSigner(t)
	for namespace, ok := range map[string]bool{releaseverify.LegacyNamespace: true, releaseverify.Namespace: false, "file": false} {
		server := signatureServer(t, "v2.1.0", map[string]func([]byte) []byte{
			releaseverify.LegacySignatureAsset: func(c []byte) []byte { return releasetest.Sign(t, signer, namespace, "sha512", c) },
		})
		client := &Client{BaseURL: server.URL, HTTPClient: server.Client(), Version: "v3.0.0",
			GOOS: "linux", GOARCH: "amd64", SigningKeys: []ssh.PublicKey{signer.PublicKey()}}
		target, err := client.Target(context.Background(), "v2.1.0")
		if err != nil {
			t.Fatalf("%s: Target() = %v", namespace, err)
		}
		if _, err := client.Prepare(context.Background(), target); (err == nil) != ok {
			t.Errorf("%s: Prepare() = %v, want ok %v", namespace, err, ok)
		}
	}
}

// The question before a rollback to a release before the rename says what that version works on.
func TestTargetQuestionNamesTheStateBeforeTheMigration(t *testing.T) {
	question := TargetQuestion("v2.1.0", false, true)
	for _, want := range []string{"~/.qatlas/cli/", "qatlas-cli", "installed as qatlas", "qconnect is removed"} {
		if !strings.Contains(question, want) {
			t.Errorf("question lacks %q: %s", want, question)
		}
	}
	if question := TargetQuestion("v3.0.0", false, true); strings.Contains(question, "~/.qatlas/cli/") {
		t.Errorf("a rollback after the rename names the former state: %s", question)
	}
}
