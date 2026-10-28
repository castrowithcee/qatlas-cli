package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/castrowithcee/qatlas-cli/internal/release"
	"github.com/castrowithcee/qatlas-cli/internal/release/releasetest"
	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
)

// chosenRelease is one release of the local release server.
type chosenRelease struct {
	tag        string
	prerelease bool
	unsigned   bool
}

// chosenUpdater serves the given releases signed by a fresh signer and returns an updater for a program
// that runs v1.0.0.
func chosenUpdater(t *testing.T, executable string, releases []chosenRelease, replaced *int) *selfupdate.Client {
	t.Helper()
	signer := releasetest.NewSigner(t)
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
	asset := func(tag string) string { return fmt.Sprintf("qatlas_%s_linux_amd64.tar.gz", tag) }
	checksums := func(tag string) []byte {
		sum := sha256.Sum256(archive.Bytes())
		return []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset(tag)))
	}
	var server *httptest.Server
	describe := func(r chosenRelease) map[string]any {
		assets := []map[string]string{
			{"name": asset(r.tag), "url": server.URL + "/asset"},
			{"name": "checksums.txt", "url": server.URL + "/checksums/" + r.tag},
		}
		if !r.unsigned {
			assets = append(assets, map[string]string{"name": "checksums.txt.sig", "url": server.URL + "/signature/" + r.tag})
		}
		return map[string]any{"tag_name": r.tag, "prerelease": r.prerelease, "assets": assets}
	}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := req.URL.Path
		switch {
		case path == "/releases":
			out := []map[string]any{}
			if req.URL.Query().Get("page") == "1" {
				for _, r := range releases {
					out = append(out, describe(r))
				}
			}
			_ = json.NewEncoder(w).Encode(out)
		case strings.HasPrefix(path, "/releases/tags/"):
			for _, r := range releases {
				if r.tag == strings.TrimPrefix(path, "/releases/tags/") {
					_ = json.NewEncoder(w).Encode(describe(r))
					return
				}
			}
			http.NotFound(w, req)
		case path == "/asset":
			_, _ = w.Write(archive.Bytes())
		case strings.HasPrefix(path, "/checksums/"):
			_, _ = w.Write(checksums(strings.TrimPrefix(path, "/checksums/")))
		case strings.HasPrefix(path, "/signature/"):
			_, _ = w.Write(releasetest.Sign(t, signer, release.Namespace, "sha512", checksums(strings.TrimPrefix(path, "/signature/"))))
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(server.Close)
	return &selfupdate.Client{
		BaseURL: server.URL, HTTPClient: server.Client(), Version: "v1.0.0", GOOS: "linux", GOARCH: "amd64",
		Executable: executable, SigningKeys: []ssh.PublicKey{signer.PublicKey()},
		BeforeReplace: func(context.Context, selfupdate.Release) error {
			*replaced++
			return nil
		},
	}
}

var chosenReleases = []chosenRelease{
	{tag: "v1.2.0"}, {tag: "v1.3.0-rc.1", prerelease: true}, {tag: "v1.0.0"}, {tag: "v0.31.0"},
	{tag: "v1.4.0", unsigned: true}, {tag: "v0.29.0"},
}

// chosenRun runs 'qatlas update' with args against chosenReleases.
func chosenRun(t *testing.T, args ...string) (code int, stdout, stderr, program string, replaced int) {
	t.Helper()
	executable := installedProgram(t)
	opts := &Options{Updater: chosenUpdater(t, executable, chosenReleases, &replaced)}
	code, stdout, stderr = runWithInput(t, opts, "", append([]string{"update", "--config", configIn(t.TempDir())}, args...)...)
	data, _ := os.ReadFile(executable)
	return code, stdout, stderr, string(data), replaced
}

func TestUpdateListShowsTheChoosableReleases(t *testing.T) {
	withInteractive(t, false)
	code, stdout, stderr, program, replaced := chosenRun(t, "--list", "--output", "json")
	if code != exitOK || program != "old-program" || replaced != 0 {
		t.Fatalf("exit code = %d, stderr = %q, program = %q, replaced %d", code, stderr, program, replaced)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("%v: %q", err, stdout)
	}
	var got []string
	for _, row := range rows {
		got = append(got, fmt.Sprintf("%v/%v/%v/%v", row["version"], row["prerelease"], row["current"], row["older"]))
	}
	want := "v1.3.0-rc.1/true/false/false v1.2.0/false/false/false v1.0.0/false/true/false v0.31.0/false/false/true"
	if strings.Join(got, " ") != want {
		t.Fatalf("list = %v, want %s", got, want)
	}
}

func TestUpdateToInstallsAPrereleaseAfterTheQuestion(t *testing.T) {
	var asked []string
	withVaultConfirm(t, func(prompt string) (bool, error) {
		asked = append(asked, prompt)
		return true, nil
	})
	code, stdout, stderr, program, replaced := chosenRun(t, "--to", "v1.3.0-rc.1", "--output", "json")
	if code != exitOK || program != "new-program" || replaced != 1 || len(asked) != 1 {
		t.Fatalf("exit code = %d, stderr = %q, program = %q, replaced %d, questions %d", code, stderr, program, replaced, len(asked))
	}
	for _, want := range []string{"prerelease v1.3.0-rc.1", "newer", "vault process is handed over"} {
		if !strings.Contains(asked[0], want) {
			t.Errorf("the question does not say %q: %q", want, asked[0])
		}
	}
	for _, want := range []string{`"signed":true`, `"updated":true`, `"latest":"v1.3.0-rc.1"`} {
		if !strings.Contains(strings.ReplaceAll(stdout, " ", ""), want) {
			t.Errorf("stdout = %q does not hold %s", stdout, want)
		}
	}
}

func TestUpdateToAnsweringNoChangesNothing(t *testing.T) {
	withVaultConfirm(t, func(string) (bool, error) { return false, nil })
	code, _, stderr, program, replaced := chosenRun(t, "--to", "v1.2.0")
	if code != exitOK || program != "old-program" || replaced != 0 || !strings.Contains(stderr, "cancelled") {
		t.Fatalf("exit code = %d, stderr = %q, program = %q, replaced %d", code, stderr, program, replaced)
	}
}

func TestUpdateToWithoutTerminalNeedsYes(t *testing.T) {
	withVaultConfirm(t, func(string) (bool, error) {
		t.Error("a refused update asked")
		return true, nil
	})
	for name, args := range map[string][]string{"pipe": nil, "agent": {"--agent"}} {
		withInteractive(t, name == "agent")
		code, _, stderr, program, replaced := chosenRun(t, append([]string{"--to", "v1.2.0"}, args...)...)
		if code != exitUsage || !strings.Contains(stderr, "--yes") || program != "old-program" || replaced != 0 {
			t.Errorf("%s: exit code = %d, stderr = %q, program = %q, replaced %d", name, code, stderr, program, replaced)
		}
	}
	withInteractive(t, false)
	code, _, stderr, program, replaced := chosenRun(t, "--to", "v1.2.0", "--yes")
	if code != exitOK || program != "new-program" || replaced != 1 {
		t.Errorf("--yes: exit code = %d, stderr = %q, program = %q, replaced %d", code, stderr, program, replaced)
	}
}

func TestUpdateToRefusesWithoutChanges(t *testing.T) {
	withVaultConfirm(t, func(string) (bool, error) {
		t.Error("a refused update asked")
		return true, nil
	})
	for name, tt := range map[string]struct{ tag, want string }{
		"unsigned":      {"v1.4.0", "not signed"},
		"below minimum": {"v0.29.0", "oldest version"},
		"older":         {"v0.31.0", "not available yet"},
	} {
		code, _, stderr, program, replaced := chosenRun(t, "--to", tt.tag, "--yes")
		if code != exitUsage || !strings.Contains(stderr, tt.want) || program != "old-program" || replaced != 0 {
			t.Errorf("%s: exit code = %d, stderr = %q, program = %q, replaced %d", name, code, stderr, program, replaced)
		}
	}
}

func TestUpdateToTheRunningVersionOnlyReportsIt(t *testing.T) {
	withVaultConfirm(t, func(string) (bool, error) {
		t.Error("the running version asked")
		return true, nil
	})
	withInteractive(t, false)
	code, _, stderr, program, replaced := chosenRun(t, "--to", "v1.0.0")
	if code != exitOK || !strings.Contains(stderr, "already installed") || program != "old-program" || replaced != 0 {
		t.Fatalf("exit code = %d, stderr = %q, program = %q, replaced %d", code, stderr, program, replaced)
	}
}

func TestUpdateListAndToExcludeEachOther(t *testing.T) {
	for _, args := range [][]string{
		{"--list", "--to", "v1.2.0"}, {"--list", "--check"}, {"--to", "v1.2.0", "--check"}, {"--list", "--yes"}, {"--to", ""},
	} {
		code, _, stderr, program, replaced := chosenRun(t, args...)
		if code != exitUsage || program != "old-program" || replaced != 0 {
			t.Errorf("%v: exit code = %d, stderr = %q", args, code, stderr)
		}
	}
}
