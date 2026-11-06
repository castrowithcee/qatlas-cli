//go:build linux

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/castrowithcee/qatlas-connect/internal/release"
	"github.com/castrowithcee/qatlas-connect/internal/release/releasetest"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

const (
	// formerTag is the release before the rename an update starts from and a rollback returns to.
	formerTag = "v2.1.0"
	// renamedTag is the version of this program in these tests, a release after the rename.
	renamedTag = "v3.1.0"
	// adoptedNote is what the first start under the former name says once it took the current one.
	adoptedNote = "qatlas stays an alias of it"
)

// releaseFile is one file in a release archive.
type releaseFile struct {
	name string
	body []byte
}

// tarball packs files into a .tar.gz archive.
func tarball(t *testing.T, files ...releaseFile) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// layoutArchive is the archive a release of version publishes for this platform, with program inside it
// under the names of layout: the program, its manual page, and its documentation directory.
func layoutArchive(t *testing.T, layout release.Layout, program []byte) []byte {
	t.Helper()
	return tarball(t,
		releaseFile{"bin/" + layout.Program, program},
		releaseFile{"share/man/man1/" + layout.Program + ".1", []byte(".TH " + strings.ToUpper(layout.Program) + " 1\n")},
		releaseFile{"share/doc/" + layout.Program + "/LICENSE", []byte("license\n")},
	)
}

// testRelease is one release on a local release server: its assets by name.
type testRelease struct {
	tag    string
	assets map[string][]byte
}

// renamedRelease is a release after the rename as it is published until the next major release: the
// current archive and, for updaters of the releases before the rename, the former one with the same
// program; checksums.txt over both; the current signature and, for those updaters, the former one.
func renamedRelease(t *testing.T, signer ssh.Signer, tag string, program []byte) testRelease {
	t.Helper()
	current, err := release.LayoutOf(tag)
	if err != nil {
		t.Fatal(err)
	}
	former := release.Layout{Program: "qatlas"}
	assets := map[string][]byte{}
	var checksums strings.Builder
	for _, layout := range []release.Layout{current, former} {
		name := layout.ArchiveName(tag, "linux", runtime.GOARCH)
		assets[name] = layoutArchive(t, layout, program)
		sum := sha256.Sum256(assets[name])
		fmt.Fprintf(&checksums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	assets["checksums.txt"] = []byte(checksums.String())
	assets[release.SignatureAsset] = releasetest.Sign(t, signer, release.Namespace, "sha512", assets["checksums.txt"])
	assets[release.LegacySignatureAsset] = releasetest.Sign(t, signer, release.LegacyNamespace, "sha512", assets["checksums.txt"])
	return testRelease{tag: tag, assets: assets}
}

// formerRelease is a release before the rename: the former archive, its checksums, and the former signature.
func formerRelease(t *testing.T, signer ssh.Signer, tag string, program []byte) testRelease {
	t.Helper()
	layout, err := release.LayoutOf(tag)
	if err != nil || !layout.Legacy {
		t.Fatalf("%s is no release before the rename: %v", tag, err)
	}
	name := layout.ArchiveName(tag, "linux", runtime.GOARCH)
	archive := layoutArchive(t, layout, program)
	sum := sha256.Sum256(archive)
	checksums := []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	return testRelease{tag: tag, assets: map[string][]byte{
		name: archive, "checksums.txt": checksums,
		release.LegacySignatureAsset: releasetest.Sign(t, signer, release.LegacyNamespace, "sha512", checksums),
	}}
}

// releaseAPI serves releases the way the GitHub API does for the updater: the latest one, each by tag, and
// the list, with their assets. The first release is the latest.
func releaseAPI(t *testing.T, releases func() []testRelease) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	describe := func(r testRelease) map[string]any {
		var assets []map[string]string
		for name := range r.assets {
			assets = append(assets, map[string]string{"name": name, "url": server.URL + "/download/" + r.tag + "/" + name})
		}
		return map[string]any{"tag_name": r.tag, "assets": assets}
	}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		all := releases()
		switch {
		case r.URL.Path == "/releases/latest" && len(all) > 0:
			_ = json.NewEncoder(w).Encode(describe(all[0]))
		case r.URL.Path == "/releases":
			var list []map[string]any
			if r.URL.Query().Get("page") == "1" {
				for _, rel := range all {
					list = append(list, describe(rel))
				}
			}
			_ = json.NewEncoder(w).Encode(list)
		case strings.HasPrefix(r.URL.Path, "/releases/tags/"):
			for _, rel := range all {
				if rel.tag == strings.TrimPrefix(r.URL.Path, "/releases/tags/") {
					_ = json.NewEncoder(w).Encode(describe(rel))
					return
				}
			}
			http.NotFound(w, r)
		case strings.HasPrefix(r.URL.Path, "/download/"):
			tag, name, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/download/"), "/")
			for _, rel := range all {
				if body, ok := rel.assets[name]; ok && rel.tag == tag {
					_, _ = w.Write(body)
					return
				}
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// buildRenamed builds this program as version renamedTag into path, trusting keyLine and updating from
// releasesURL.
func buildRenamed(t *testing.T, path, keyLine, releasesURL string) []byte {
	t.Helper()
	cmd := exec.Command("go", "build", "-tags", "e2e", "-buildvcs=false", "-ldflags",
		"-X github.com/castrowithcee/qatlas-connect/internal/cli.version="+renamedTag+
			" -X 'github.com/castrowithcee/qatlas-connect/internal/release.extraKeyLines="+keyLine+"'"+
			" -X github.com/castrowithcee/qatlas-connect/internal/selfupdate.testBaseURL="+releasesURL,
		"-o", path, ".")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("building the binary: %v", err)
	}
	program, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return program
}

// installFormerly puts the program of archive at <prefix>/bin/qatlas the way an updater of a release before
// the rename does: it checks the former signature over checksums.txt, the archive's checksum, and replaces
// bin/qatlas and share/man/man1/qatlas.1 from the archive's bin/qatlas and its manual page.
func installFormerly(t *testing.T, prefix string, rel testRelease, key ssh.PublicKey) {
	t.Helper()
	name := release.Layout{Program: "qatlas"}.ArchiveName(rel.tag, "linux", runtime.GOARCH)
	checksums := rel.assets["checksums.txt"]
	if err := release.VerifyChecksums(checksums, rel.assets["checksums.txt.sig"], "qatlas-release", []ssh.PublicKey{key}); err != nil {
		t.Fatalf("the former signature does not verify: %v", err)
	}
	want, err := release.ChecksumFor(checksums, name)
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(rel.assets[name]); hex.EncodeToString(sum[:]) != want {
		t.Fatal("the former archive does not match its checksum")
	}
	gz, err := gzip.NewReader(bytes.NewReader(rel.assets[name]))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		target := map[string]string{"bin/qatlas": "bin/qatlas", "share/man/man1/qatlas.1": "share/man/man1/qatlas.1"}[header.Name]
		if target == "" {
			continue
		}
		body, _ := io.ReadAll(tr)
		path := filepath.Join(prefix, target)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		staged := path + ".staged"
		if err := os.WriteFile(staged, body, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(staged, path); err != nil {
			t.Fatal(err)
		}
	}
}

// assertAdopted checks that bin holds the program as qconnect and qatlas as a link to it.
func assertAdopted(t *testing.T, bin string, program []byte) {
	t.Helper()
	current := filepath.Join(bin, "qconnect")
	if info, err := os.Lstat(current); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("qconnect is %v, %v, want the program", info, err)
	}
	if got, _ := os.ReadFile(current); !bytes.Equal(got, program) {
		t.Fatal("qconnect is not the installed program")
	}
	if target, err := os.Readlink(filepath.Join(bin, "qatlas")); err != nil || target != "qconnect" {
		t.Fatalf("qatlas links to %q (%v), want qconnect", target, err)
	}
}

// assertMCPAnswersAsQconnect runs 'mcp' through r and checks that it serves as qconnect with its current
// tools, and that the protocol stream carries nothing else.
func assertMCPAnswersAsQconnect(t *testing.T, r *runner) {
	t.Helper()
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	code, stdout, stderr := r.runInput(t, input, "mcp")
	if code != 0 {
		t.Fatalf("mcp: exit %d, stderr %q", code, stderr)
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if !json.Valid([]byte(line)) {
			t.Errorf("mcp: the protocol stream carries %q", line)
		}
	}
	for _, want := range []string{`"name":"qconnect"`, `"name":"search"`, `"name":"describe"`, `"name":"invoke"`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("mcp: stdout lacks %s:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, `"qatlas.invoke"`) {
		t.Errorf("mcp: stdout still offers the former tools:\n%s", stdout)
	}
}

// TestUpdateInTheFormerFormGetsTheCurrentName stands in for an update by a release before the rename: that
// updater reads only the former assets and signature and installs the program as <prefix>/bin/qatlas. The
// first start of the program there gives it its current name with qatlas as an alias, in exactly one of the
// processes that start at once, and migrates the configuration; afterwards qconnect and qatlas both run it,
// and 'qatlas mcp' serves as qconnect. A qconnect already there, whoever put it there, is left alone.
func TestUpdateInTheFormerFormGetsTheCurrentName(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	dir := t.TempDir()
	signer := releasetest.NewSigner(t)
	keyLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	program := buildRenamed(t, filepath.Join(dir, "build", "qconnect"), keyLine, "http://127.0.0.1:1")
	rel := renamedRelease(t, signer, renamedTag, program)

	t.Run("adopts the current name once", func(t *testing.T) {
		home := t.TempDir()
		legacy := filepath.Join(home, ".qatlas", "cli")
		if err := os.MkdirAll(legacy, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(legacy, "config.yaml"), []byte("version: 4\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		prefix := filepath.Join(home, "prefix")
		installFormerly(t, prefix, rel, signer.PublicKey())
		bin := filepath.Join(prefix, "bin")
		env := []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), secret.StoreSelector + "=none"}
		var seen strings.Builder
		former := &runner{bin: filepath.Join(bin, "qatlas"), env: env, seen: &seen}

		const starts = 8
		var wg sync.WaitGroup
		type outcome struct {
			code           int
			stdout, stderr string
		}
		outcomes := make([]outcome, starts)
		var mu sync.Mutex
		for i := range starts {
			wg.Add(1)
			go func() {
				defer wg.Done()
				cmd := exec.Command(former.bin, "providers", "--output", "json")
				cmd.Env = env
				var stdout, stderr strings.Builder
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				err := cmd.Run()
				code := 0
				if exit, ok := err.(*exec.ExitError); ok {
					code = exit.ExitCode()
				} else if err != nil {
					code = -1
				}
				mu.Lock()
				outcomes[i] = outcome{code, stdout.String(), stderr.String()}
				mu.Unlock()
			}()
		}
		wg.Wait()
		notes := 0
		for i, o := range outcomes {
			if o.code != 0 || !json.Valid([]byte(o.stdout)) || strings.Contains(o.stdout, adoptedNote) {
				t.Errorf("start %d: exit %d, stdout %q, stderr %q", i, o.code, o.stdout, o.stderr)
			}
			notes += strings.Count(o.stderr, adoptedNote)
		}
		if notes != 1 {
			t.Fatalf("%d starts said they adopted the name, want exactly one", notes)
		}
		assertAdopted(t, bin, program)
		if _, err := os.Stat(filepath.Join(home, ".qatlas", "connect", "config.yaml")); err != nil {
			t.Errorf("the configuration was not migrated: %v", err)
		}

		current := &runner{bin: filepath.Join(bin, "qconnect"), env: env, seen: &seen}
		for name, r := range map[string]*runner{"qconnect": current, "qatlas": former} {
			code, stdout, stderr := r.run(t, "--version")
			if code != 0 || strings.TrimSpace(stdout) != "qconnect "+renamedTag || strings.Contains(stderr, adoptedNote) {
				t.Errorf("%s --version: exit %d, stdout %q, stderr %q", name, code, stdout, stderr)
			}
		}
		assertMCPAnswersAsQconnect(t, former)
	})

	t.Run("leaves an existing qconnect alone", func(t *testing.T) {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, ".qatlas", "connect"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".qatlas", "connect", "config.yaml"), []byte("version: 4\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		prefix := filepath.Join(home, "prefix")
		installFormerly(t, prefix, rel, signer.PublicKey())
		bin := filepath.Join(prefix, "bin")
		foreign := filepath.Join(bin, "qconnect")
		if err := os.WriteFile(foreign, []byte("#!/bin/sh\necho foreign\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		var seen strings.Builder
		former := &runner{bin: filepath.Join(bin, "qatlas"), seen: &seen,
			env: []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), secret.StoreSelector + "=none"}}
		code, _, stderr := former.run(t, "providers", "--output", "json")
		if code != 0 || strings.Contains(stderr, adoptedNote) {
			t.Fatalf("providers: exit %d, stderr %q, want success and no adoption", code, stderr)
		}
		if got, _ := os.ReadFile(foreign); string(got) != "#!/bin/sh\necho foreign\n" {
			t.Errorf("the existing qconnect was changed: %q", got)
		}
		if info, err := os.Lstat(former.bin); err != nil || !info.Mode().IsRegular() {
			t.Errorf("qatlas is %v, %v, want the program as it was", info, err)
		}
	})
}

// dialogStep answers one prompt of a dialog: once marker appears after the last answer, answer is typed;
// for a secret answer only once the terminal no longer echoes.
type dialogStep struct {
	marker, answer string
	secret         bool
}

// runDialog runs the binary with a pseudo-terminal as its controlling terminal, standard output, and
// standard error, and stdin as its standard input, and answers its prompts in turn. It returns everything
// written to the terminal and the exit code.
func runDialog(t *testing.T, c *runner, stdin string, steps []dialogStep, args ...string) (string, int) {
	t.Helper()
	master, slave := openPTY(t)
	defer master.Close()
	cmd := exec.Command(c.bin, args...)
	cmd.Env = c.environ(t)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout, cmd.Stderr = slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 1}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting %v: %v", args, err)
	}
	_ = slave.Close()
	var mu sync.Mutex
	var out strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			mu.Lock()
			out.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	text := func() string { mu.Lock(); defer mu.Unlock(); return out.String() }
	from := 0
	for _, step := range steps {
		for deadline := time.Now().Add(60 * time.Second); ; {
			now := text()
			if i := strings.Index(now[from:], step.marker); i >= 0 &&
				(!step.secret || echoOff(master)) {
				from += i + len(step.marker)
				break
			}
			if time.Now().After(deadline) {
				_ = cmd.Process.Kill()
				t.Fatalf("%v: no %q appeared: %q", args, step.marker, now)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if _, err := io.WriteString(master, step.answer+"\n"); err != nil {
			t.Fatalf("typing at the terminal: %v", err)
		}
	}
	err := cmd.Wait()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("running %v: %v", args, err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	c.seen.WriteString(text())
	return text(), code
}

// formerSource extracts the source of formerTag from this repository into a new directory, or skips the
// test where the tag is not there, as in a shallow checkout without tags.
func formerSource(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if err := exec.Command("git", "rev-parse", "--verify", "--quiet", formerTag+"^{commit}").Run(); err != nil {
		t.Skipf("the tag %s is not in this checkout, as in a shallow one without tags; "+
			"TestUpdateInTheFormerFormGetsTheCurrentName covers the same update form", formerTag)
	}
	src := t.TempDir()
	archive := filepath.Join(t.TempDir(), "source.tar")
	if out, err := exec.Command("git", "archive", "--format=tar", "--output="+archive, formerTag).CombinedOutput(); err != nil {
		t.Fatalf("git archive %s: %v: %s", formerTag, err, out)
	}
	if out, err := exec.Command("tar", "-xf", archive, "-C", src).CombinedOutput(); err != nil {
		t.Fatalf("extracting %s: %v: %s", formerTag, err, out)
	}
	return src
}

// buildFormer builds formerTag from src into path, offline with the modules this host has, trusting keyLine
// and updating from releasesURL, as the release before the rename was built for its own tests.
func buildFormer(t *testing.T, src, path, keyLine, releasesURL string) []byte {
	t.Helper()
	const module = "github.com/castrowithcee/qatlas-cli"
	cmd := exec.Command("go", "build", "-tags", "e2e", "-buildvcs=false", "-ldflags",
		"-X "+module+"/internal/cli.version="+formerTag+
			" -X '"+module+"/internal/release.extraKeyLines="+keyLine+"'"+
			" -X "+module+"/internal/selfupdate.testBaseURL="+releasesURL,
		"-o", path, ".")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off", "GOWORK=off")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("building %s offline: %v", formerTag, err)
	}
	program, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return program
}

// logCalls counts the calls of the tool pages.list recorded in the vault logs below home.
func logCalls(t *testing.T, home string) int {
	t.Helper()
	return strings.Count(readLogs(t, home), `"bookstack.pages.list"`)
}

// TestUpdateFromTheFormerReleaseAndRollBack updates a program built from the tag of the release before the
// rename, with its own updater, to this release on a local signed release server, and back. The former
// program sets up its configuration, vault secrets, approval, and a logged call at its own default place and
// holds the vault unlocked. Its update installs this program as qatlas and hands the vault over; the first
// start then gives it the name qconnect with qatlas as an alias, and the vault stays unlocked through it.
// Configuration, secrets, approval, and log are migrated: after a fresh unlock with the same passphrase
// qconnect still reads the secret. 'qatlas mcp' serves as qconnect. A rollback to the former release names
// the state it works on before it installs, and afterwards qatlas is the former program on that state.
func TestUpdateFromTheFormerReleaseAndRollBack(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the former release and this one")
	}
	skipWithoutCertFile(t)
	src := formerSource(t)
	const (
		storedID     = "canary-former-id-3f81c2"
		storedSecret = "canary-former-secret-7a20d9"
		passphrase   = "canary-passphrase-former-51be"
	)
	dir := t.TempDir()
	wiki := mock(t, "Token "+storedID+":"+storedSecret, []map[string]any{page(1, "Former Runbook")}, "<p>x</p>")

	signer := releasetest.NewSigner(t)
	keyLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	var releases []testRelease
	var releasesMu sync.Mutex
	server := releaseAPI(t, func() []testRelease { releasesMu.Lock(); defer releasesMu.Unlock(); return releases })
	formerProgram := buildFormer(t, src, filepath.Join(dir, "build", "former", "qatlas"), keyLine, server.URL)
	renamedProgram := buildRenamed(t, filepath.Join(dir, "build", "renamed", "qconnect"), keyLine, server.URL)
	releasesMu.Lock()
	releases = []testRelease{renamedRelease(t, signer, renamedTag, renamedProgram), formerRelease(t, signer, formerTag, formerProgram)}
	releasesMu.Unlock()

	home := filepath.Join(dir, "home")
	legacyHome := filepath.Join(home, ".qatlas", "cli")
	currentHome := filepath.Join(home, ".qatlas", "connect")
	bin := filepath.Join(home, "prefix", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "qatlas"), formerProgram, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(legacyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`version: 4
services:
  wiki:
    provider: bookstack
    base_url: %s
credentials:
  vault-reader:
    type: vault
connections:
  wiki:
    service: wiki
    credential: vault-reader
defaults:
  connections:
    bookstack: wiki
`, wiki.URL)
	if err := os.WriteFile(filepath.Join(legacyHome, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimeDir := shortRuntimeDir(t)
	var seen strings.Builder
	env := []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "XDG_RUNTIME_DIR=" + runtimeDir,
		"QATLAS_CREDENTIAL_STORE=none", secret.StoreSelector + "=none"}
	former := &runner{bin: filepath.Join(bin, "qatlas"), env: env, seen: &seen}
	current := &runner{bin: filepath.Join(bin, "qconnect"), env: env, seen: &seen}
	var vaultPIDs []int
	t.Cleanup(func() {
		for _, r := range []*runner{current, former} {
			if _, err := os.Stat(r.bin); err == nil {
				_, _, _ = r.run(t, "vault", "lock")
			}
		}
		for _, pid := range vaultPIDs {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	unlockedPID := regexp.MustCompile(`unlocked in a vault process \(pid (\d+)\)`)
	unlock := func(r *runner) {
		t.Helper()
		out, code := runDialog(t, r, "", []dialogStep{{"passphrase: ", passphrase, true}}, "vault", "unlock")
		match := unlockedPID.FindStringSubmatch(out)
		if code != 0 || match == nil {
			t.Fatalf("%s vault unlock: exit %d, output %q", filepath.Base(r.bin), code, out)
		}
		var pid int
		_, _ = fmt.Sscan(match[1], &pid)
		vaultPIDs = append(vaultPIDs, pid)
	}
	invoke := func(r *runner) string {
		t.Helper()
		code, stdout, stderr := r.run(t, "invoke", "bookstack.pages.list", "--output", "json")
		if code != 0 || !strings.Contains(stdout, "Former Runbook") {
			t.Fatalf("%s invoke: exit %d, stdout %q, stderr %q", filepath.Base(r.bin), code, stdout, stderr)
		}
		return stderr
	}

	// The former release sets up its own state: two vault secrets, the approval, an unlocked vault, a call.
	if out, code := runDialog(t, former, storedID, []dialogStep{{"unencrypted: ", passphrase, true}, {"confirm the passphrase: ", passphrase, true}},
		"credential", "set", "vault-reader", "token-id"); code != 0 {
		t.Fatalf("former credential set token-id: exit %d, output %q", code, out)
	}
	if out, code := runDialog(t, former, storedSecret, []dialogStep{{"passphrase: ", passphrase, true}},
		"credential", "set", "vault-reader", "token-secret"); code != 0 {
		t.Fatalf("former credential set token-secret: exit %d, output %q", code, out)
	}
	if out, code := runDialog(t, former, "", []dialogStep{{"passphrase: ", passphrase, true}}, "vault", "approve"); code != 0 {
		t.Fatalf("former vault approve: exit %d, output %q", code, out)
	}
	unlock(former)
	invoke(former)
	if logCalls(t, legacyHome) == 0 {
		t.Fatal("the former release logged no call")
	}

	// The former updater installs this release as qatlas from the former assets.
	code, stdout, stderr := former.run(t, "update", "--yes", "--output", "json")
	if code != 0 || !strings.Contains(stdout, `"updated":true`) || !strings.Contains(stdout, `"latest":"`+renamedTag+`"`) {
		t.Fatalf("former update: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if got, _ := os.ReadFile(former.bin); !bytes.Equal(got, renamedProgram) {
		t.Fatal("the former updater did not install this release as qatlas")
	}
	if _, err := os.Lstat(current.bin); err == nil {
		t.Fatal("the former updater left a qconnect")
	}

	// The first start adopts the current name; the vault handed over stays unlocked through it.
	stderr = invoke(former)
	if strings.Count(stderr, adoptedNote) != 1 {
		t.Errorf("first start stderr = %q, want the adoption said once", stderr)
	}
	assertAdopted(t, bin, renamedProgram)
	if stderr := invoke(current); strings.Contains(stderr, adoptedNote) {
		t.Errorf("qconnect said the adoption again: %q", stderr)
	}
	invoke(former)
	assertMCPAnswersAsQconnect(t, former)
	for _, r := range []*runner{current, former} {
		if code, stdout, _ := r.run(t, "--version"); code != 0 || strings.TrimSpace(stdout) != "qconnect "+renamedTag {
			t.Errorf("%s --version = %d, %q", r.bin, code, stdout)
		}
	}

	// Migrated: the configuration, the vault with its secrets and approval, and the log with the former call.
	if _, err := os.Stat(filepath.Join(currentHome, "config.yaml")); err != nil {
		t.Fatalf("the configuration was not migrated: %v", err)
	}
	if got := logCalls(t, currentHome); got < 2 {
		t.Errorf("the migrated log records %d calls, want the former one and those after the update", got)
	}
	if code, _, stderr := current.run(t, "vault", "lock"); code != 0 {
		t.Fatalf("qconnect vault lock: exit %d, stderr %q", code, stderr)
	}
	unlock(current)
	invoke(current)

	// The rollback names the state the former release works on before it installs anything.
	formerCalls := logCalls(t, legacyHome)
	out, code := runDialog(t, current, "", []dialogStep{{"[y/N] ", "y", false}}, "update", "--to", formerTag)
	question := out
	if i := strings.Index(out, "[y/N]"); i >= 0 {
		question = out[:i]
	}
	if code != 0 || !strings.Contains(question, "~/.qatlas/cli/") || !strings.Contains(question, "qatlas-cli") ||
		!strings.Contains(question, "installed as qatlas") {
		t.Fatalf("rollback: exit %d, output %q, want the former state named in the question", code, out)
	}
	if got, _ := os.ReadFile(former.bin); !bytes.Equal(got, formerProgram) {
		t.Fatal("qatlas is not the former release after the rollback")
	}
	if info, err := os.Lstat(former.bin); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("qatlas is %v, %v, want the former program as a file", info, err)
	}
	if _, err := os.Lstat(current.bin); err == nil {
		t.Fatal("qconnect remains after the rollback to a release that does not call itself so")
	}
	if code, stdout, _ := former.run(t, "--version"); code != 0 || strings.TrimSpace(stdout) != "qatlas "+formerTag {
		t.Fatalf("qatlas --version after the rollback = %d, %q", code, stdout)
	}
	// The former release works on its own state: its configuration, vault, and log in ~/.qatlas/cli.
	currentCalls := logCalls(t, currentHome)
	unlock(former)
	invoke(former)
	if got := logCalls(t, legacyHome); got <= formerCalls {
		t.Errorf("the former log records %d calls, was %d, want the call after the rollback", got, formerCalls)
	}
	if got := logCalls(t, currentHome); got != currentCalls {
		t.Errorf("the migrated log changed after the rollback: %d calls, was %d", got, currentCalls)
	}

	for _, canary := range []string{storedID, storedSecret, passphrase} {
		if strings.Contains(seen.String(), canary) {
			t.Errorf("a secret reached the output: %s", canary)
		}
	}
}
