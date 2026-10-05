package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	releaseverify "github.com/castrowithcee/qatlas-cli/internal/release"
	"github.com/castrowithcee/qatlas-cli/internal/release/releasetest"
)

func TestSemverOrdering(t *testing.T) {
	tests := []struct {
		left, right string
		want        int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.1", "v1.0.0", 1},
		{"v1.2.0", "v1.10.0", -1},
		{"v2.0.0-rc.1", "v2.0.0", -1},
		{"v2.0.0-rc.2", "v2.0.0-rc.10", -1},
		{"v2.0.0-beta", "v2.0.0-rc.1", -1},
	}
	for _, tt := range tests {
		t.Run(tt.left+"_"+tt.right, func(t *testing.T) {
			left, err := parseSemver(tt.left)
			if err != nil {
				t.Fatal(err)
			}
			right, err := parseSemver(tt.right)
			if err != nil {
				t.Fatal(err)
			}
			if got := left.compare(right); got != tt.want {
				t.Fatalf("compare() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSemverRejectsInvalidTags(t *testing.T) {
	for _, value := range []string{"1.0.0", "v1", "v1.0", "v1.0.0-", "v01.0.0", "v1.0.0-01"} {
		t.Run(value, func(t *testing.T) {
			if _, err := parseSemver(value); err == nil {
				t.Fatalf("parseSemver(%q) = nil error", value)
			}
		})
	}
}

func TestCheckReportsNewerStableRelease(t *testing.T) {
	server := releaseServer(t, "v1.2.0", nil, "")
	defer server.Close()
	client := &Client{BaseURL: server.URL, HTTPClient: server.Client(), Version: "v1.1.0"}
	result, err := client.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Current != "v1.1.0" || result.Latest != "v1.2.0" || !result.UpdateAvailable || result.Updated {
		t.Fatalf("Check() = %+v", result)
	}
}

func TestCheckRefusesDevelopmentBuild(t *testing.T) {
	_, err := (&Client{Version: "dev"}).Check(context.Background())
	if !errors.Is(err, ErrDevelopmentBuild) {
		t.Fatalf("Check() error = %v, want ErrDevelopmentBuild", err)
	}
}

func TestCheckUsesExplicitToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer release-token" {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.0.0"})
	}))
	defer server.Close()

	client := &Client{
		BaseURL: server.URL, HTTPClient: server.Client(), Version: "v1.0.0", Token: "release-token",
	}
	if _, err := client.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTokenUsesGHEnvironment(t *testing.T) {
	t.Setenv("GH_TOKEN", "environment-token")
	if got := (&Client{}).token(); got != "environment-token" {
		t.Fatalf("token() = %q, want environment token", got)
	}
}

func TestCustomBaseURLDoesNotReceiveEnvironmentToken(t *testing.T) {
	t.Setenv("GH_TOKEN", "environment-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "unexpected token", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.0.0"})
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client(), Version: "v1.0.0"}
	if _, err := client.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateReplacesBinaryAndManpage(t *testing.T) {
	archive := releaseArchive(t, []byte("new-binary"), []byte("new-manpage"))
	server := releaseServer(t, "v1.1.0", archive, "")
	defer server.Close()

	prefix := t.TempDir()
	executable := filepath.Join(prefix, "bin", "qatlas")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	client := &Client{
		BaseURL: server.URL, HTTPClient: server.Client(), Version: "v1.0.0",
		GOOS: "linux", GOARCH: "amd64", Executable: executable,
	}
	// The hook runs once, with the old program still in place.
	calls := 0
	client.BeforeReplace = func(context.Context) {
		calls++
		assertFile(t, executable, "old-binary")
	}
	result, err := client.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("BeforeReplace ran %d times, want once", calls)
	}
	if !result.UpdateAvailable || !result.Updated || result.Latest != "v1.1.0" || result.Signed {
		t.Fatalf("Update() = %+v, want an unsigned update", result)
	}
	assertFile(t, executable, "new-binary")
	assertFile(t, filepath.Join(prefix, "share", "man", "man1", "qatlas.1"), "new-manpage")
	info, err := os.Stat(executable)
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no execute bit; the mode there says only whether the file is read-only.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o500 != 0o500 {
		t.Fatalf("updated executable mode = %v", info.Mode())
	}
}

func TestUpdateLeavesInstallationUntouchedOnChecksumMismatch(t *testing.T) {
	archive := releaseArchive(t, []byte("new-binary"), []byte("new-manpage"))
	server := releaseServer(t, "v1.1.0", archive, "0000000000000000000000000000000000000000000000000000000000000000")
	defer server.Close()
	prefix := t.TempDir()
	executable := filepath.Join(prefix, "bin", "qatlas")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	client := &Client{
		BaseURL: server.URL, HTTPClient: server.Client(), Version: "v1.0.0",
		GOOS: "linux", GOARCH: "amd64", Executable: executable,
		BeforeReplace: func(context.Context) { t.Error("BeforeReplace ran although nothing is replaced") },
	}
	if _, err := client.Update(context.Background()); err == nil {
		t.Fatal("Update() = nil error, want checksum failure")
	}
	assertFile(t, executable, "old-binary")
	if _, err := os.Stat(filepath.Join(prefix, "share", "man", "man1", "qatlas.1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manpage exists after failed update: %v", err)
	}
}

func TestUpdateDoesNotDowngrade(t *testing.T) {
	server := releaseServer(t, "v1.0.0", nil, "")
	defer server.Close()
	client := &Client{BaseURL: server.URL, HTTPClient: server.Client(), Version: "v1.1.0",
		BeforeReplace: func(context.Context) { t.Error("BeforeReplace ran although nothing is replaced") }}
	result, err := client.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.UpdateAvailable || result.Updated {
		t.Fatalf("Update() = %+v, want no downgrade", result)
	}
}

func releaseServer(t *testing.T, version string, archive []byte, checksumOverride string) *httptest.Server {
	t.Helper()
	return signedReleaseServer(t, version, archive, checksumOverride, nil)
}

// signedReleaseServer serves a release whose checksums.txt.sig is sign(checksums); without sign the
// release carries no signature.
func signedReleaseServer(t *testing.T, version string, archive []byte, checksumOverride string, sign func(checksums []byte) []byte) *httptest.Server {
	t.Helper()
	checksumsFor := func(asset string) []byte {
		sum := sha256.Sum256(archive)
		encoded := hex.EncodeToString(sum[:])
		if checksumOverride != "" {
			encoded = fmt.Sprintf("%-64s", checksumOverride)
		}
		return []byte(fmt.Sprintf("%s  %s\n", encoded, asset))
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asset := assetName(version, "linux", "amd64")
		switch r.URL.Path {
		case "/releases/latest":
			assets := []map[string]string{}
			if archive != nil {
				assets = append(assets,
					map[string]string{"name": asset, "url": server.URL + "/asset", "browser_download_url": server.URL + "/wrong"},
					map[string]string{"name": "checksums.txt", "url": server.URL + "/checksums", "browser_download_url": server.URL + "/wrong"},
				)
				if sign != nil {
					assets = append(assets, map[string]string{"name": "checksums.txt.sig", "url": server.URL + "/signature", "browser_download_url": server.URL + "/wrong"})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": version, "assets": assets})
		case "/asset":
			_, _ = w.Write(archive)
		case "/checksums":
			_, _ = w.Write(checksumsFor(asset))
		case "/signature":
			_, _ = w.Write(sign(checksumsFor(asset)))
		default:
			http.NotFound(w, r)
		}
	}))
	return server
}

func releaseArchive(t *testing.T, executable, manpage []byte) []byte {
	t.Helper()
	var body bytes.Buffer
	gz := gzip.NewWriter(&body)
	tarWriter := tar.NewWriter(gz)
	for name, content := range map[string][]byte{
		"bin/qatlas":              executable,
		"share/man/man1/qatlas.1": manpage,
	} {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != want {
		t.Fatalf("%s = %q, want %q", path, body, want)
	}
}

func installedPrefix(t *testing.T) (prefix, executable string) {
	t.Helper()
	prefix = t.TempDir()
	executable = filepath.Join(prefix, "bin", "qatlas")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	manpage := filepath.Join(prefix, "share", "man", "man1", "qatlas.1")
	if err := os.MkdirAll(filepath.Dir(manpage), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manpage, []byte("old-manpage"), 0o644); err != nil {
		t.Fatal(err)
	}
	return prefix, executable
}

func TestUpdateReportsValidSignature(t *testing.T) {
	signer := releasetest.NewSigner(t)
	archive := releaseArchive(t, []byte("new-binary"), []byte("new-manpage"))
	server := signedReleaseServer(t, "v1.1.0", archive, "", func(checksums []byte) []byte {
		return releasetest.Sign(t, signer, releaseverify.Namespace, "sha512", checksums)
	})
	defer server.Close()
	prefix, executable := installedPrefix(t)
	client := &Client{
		BaseURL: server.URL, HTTPClient: server.Client(), Version: "v1.0.0",
		GOOS: "linux", GOARCH: "amd64", Executable: executable,
		SigningKeys: []ssh.PublicKey{signer.PublicKey()},
	}
	result, err := client.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated || !result.Signed {
		t.Fatalf("Update() = %+v, want a signed update", result)
	}
	assertFile(t, executable, "new-binary")
	assertFile(t, filepath.Join(prefix, "share", "man", "man1", "qatlas.1"), "new-manpage")
}

func TestUpdateRefusesInvalidSignatureWithoutChanges(t *testing.T) {
	trusted := releasetest.NewSigner(t)
	rsaSigner := rsaTestSigner(t)
	tests := map[string]func(checksums []byte) []byte{
		"unknown key": func(c []byte) []byte {
			return releasetest.Sign(t, releasetest.NewSigner(t), releaseverify.Namespace, "sha512", c)
		},
		"wrong namespace": func(c []byte) []byte { return releasetest.Sign(t, trusted, "file", "sha512", c) },
		"rsa key":         func(c []byte) []byte { return releasetest.Sign(t, rsaSigner, releaseverify.Namespace, "sha512", c) },
		"other data": func(c []byte) []byte {
			return releasetest.Sign(t, trusted, releaseverify.Namespace, "sha512", append([]byte("x"), c...))
		},
		"broken blob": func([]byte) []byte { return []byte("not a signature") },
	}
	for name, sign := range tests {
		t.Run(name, func(t *testing.T) {
			archive := releaseArchive(t, []byte("new-binary"), []byte("new-manpage"))
			server := signedReleaseServer(t, "v1.1.0", archive, "", sign)
			defer server.Close()
			prefix, executable := installedPrefix(t)
			client := &Client{
				BaseURL: server.URL, HTTPClient: server.Client(), Version: "v1.0.0",
				GOOS: "linux", GOARCH: "amd64", Executable: executable,
				SigningKeys:   []ssh.PublicKey{trusted.PublicKey(), rsaSigner.PublicKey()},
				BeforeReplace: func(context.Context) { t.Error("BeforeReplace ran although nothing is replaced") },
			}
			result, err := client.Update(context.Background())
			if err == nil || !strings.Contains(err.Error(), "signature is invalid") {
				t.Fatalf("Update() = %+v, %v, want a signature failure", result, err)
			}
			assertFile(t, executable, "old-binary")
			assertFile(t, filepath.Join(prefix, "share", "man", "man1", "qatlas.1"), "old-manpage")
		})
	}
}

func rsaTestSigner(t *testing.T) ssh.Signer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}
