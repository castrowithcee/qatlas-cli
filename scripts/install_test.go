package scripts

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/castrowithcee/qatlas-cli/internal/release"
	"github.com/castrowithcee/qatlas-cli/internal/release/releasetest"
)

// installEnv is the harness shared by the installer tests: a fake release server, a test signing key whose
// allowed-signers file replaces the embedded key, and a temporary HOME with the install prefix below it.
// The Windows installer test reuses releasetest.Server and the signing helpers of this file.
type installEnv struct {
	t       *testing.T
	server  *releasetest.Server
	signer  ssh.Signer
	signers string
	home    string
	prefix  string
	shell   string
	path    string
}

func newInstallEnv(t *testing.T) *installEnv {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("install.sh supports Linux and macOS only")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("no release archive for " + runtime.GOARCH)
	}
	for _, tool := range []string{"sh", "curl", "tar", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	e := &installEnv{t: t, server: releasetest.NewServer(t), signer: releasetest.NewSigner(t), shell: "/bin/bash", path: os.Getenv("PATH")}
	root := t.TempDir()
	e.home = filepath.Join(root, "home")
	e.prefix = filepath.Join(e.home, ".local")
	if err := os.MkdirAll(e.home, 0o755); err != nil {
		t.Fatal(err)
	}
	e.signers = filepath.Join(root, "allowed-signers")
	line := "qatlas-release " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(e.signer.PublicKey()))) + "\n"
	if err := os.WriteFile(e.signers, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return e
}

func archiveName(tag string) string {
	return fmt.Sprintf("qatlas_%s_%s_%s.tar.gz", tag, runtime.GOOS, runtime.GOARCH)
}

// tarball builds a release archive with the layout of scripts/release.sh.
func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		mode := int64(0o644)
		if strings.HasPrefix(name, "bin/") {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: "./" + name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type releaseOptions struct {
	wrongChecksum bool
	signer        ssh.Signer // nil: the environment's test key
}

// publish adds a release whose binary is the text program and makes it the latest.
func (e *installEnv) publish(tag, program string, opts releaseOptions) {
	e.t.Helper()
	archive := tarball(e.t, map[string]string{
		"bin/qatlas":               program,
		"share/man/man1/qatlas.1":  ".TH QATLAS 1 " + tag + "\n",
		"share/doc/qatlas/LICENSE": "license " + tag + "\n",
	})
	sum := sha256.Sum256(archive)
	hash := hex.EncodeToString(sum[:])
	if opts.wrongChecksum {
		hash = strings.Repeat("0", 64)
	}
	checksums := []byte(hash + "  " + archiveName(tag) + "\n" +
		strings.Repeat("1", 64) + "  install.sh\n")
	signer := opts.signer
	if signer == nil {
		signer = e.signer
	}
	e.server.Publish(tag, map[string][]byte{
		archiveName(tag):    archive,
		"checksums.txt":     checksums,
		"checksums.txt.sig": releasetest.Sign(e.t, signer, release.Namespace, "sha512", checksums),
	})
}

// run executes scripts/install.sh with a clean environment. env entries are added to it.
func (e *installEnv) run(stdinScript bool, env []string, args ...string) (string, error) {
	e.t.Helper()
	full := []string{
		"PATH=" + e.path,
		"HOME=" + e.home,
		"SHELL=" + e.shell,
		"TMPDIR=" + e.t.TempDir(),
		"QATLAS_INSTALL_BASE_URL=" + e.server.URL,
		"QATLAS_INSTALL_PREFIX=" + e.prefix,
		"QATLAS_INSTALL_ALLOWED_SIGNERS_FILE=" + e.signers,
	}
	var cmd *exec.Cmd
	if stdinScript {
		script, err := os.Open("install.sh")
		if err != nil {
			e.t.Fatal(err)
		}
		defer script.Close()
		cmd = exec.Command("sh", append([]string{"-s", "--"}, args...)...)
		cmd.Stdin = script
	} else {
		cmd = exec.Command("sh", append([]string{"install.sh"}, args...)...)
	}
	cmd.Env = append(full, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (e *installEnv) mustRun(env []string, args ...string) string {
	e.t.Helper()
	out, err := e.run(false, env, args...)
	if err != nil {
		e.t.Fatalf("install.sh failed: %v\n%s", err, out)
	}
	return out
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func (e *installEnv) stageLeftovers() []string {
	matches, _ := filepath.Glob(filepath.Join(e.prefix, ".qatlas-stage.*"))
	return matches
}

func TestInstallFreshAndReinstall(t *testing.T) {
	e := newInstallEnv(t)
	e.publish("v1.0.0", "program one", releaseOptions{})

	out := e.mustRun(nil)
	for _, want := range []string{"v1.0.0", filepath.Join(e.prefix, "bin", "qatlas"), "new terminal", "qatlas tui", "qatlas web"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	binary := filepath.Join(e.prefix, "bin", "qatlas")
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("binary: %v, %v", info, err)
	}
	if got := readFile(t, binary); got != "program one" {
		t.Errorf("binary = %q", got)
	}
	if got := readFile(t, filepath.Join(e.prefix, "share/man/man1/qatlas.1")); !strings.Contains(got, "v1.0.0") {
		t.Errorf("man page = %q", got)
	}
	if got := readFile(t, filepath.Join(e.prefix, "share/doc/qatlas/LICENSE")); got != "license v1.0.0\n" {
		t.Errorf("license = %q", got)
	}
	if _, err := os.Stat(filepath.Join(e.home, ".qatlas")); !os.IsNotExist(err) {
		t.Errorf("~/.qatlas was created: %v", err)
	}
	profile := filepath.Join(e.home, ".profile")
	if got := readFile(t, profile); strings.Count(got, `export PATH="$HOME/.local/bin:$PATH"`) != 1 {
		t.Errorf("profile = %q", got)
	}

	e.publish("v1.0.1", "program two", releaseOptions{})
	out = e.mustRun(nil)
	if !strings.Contains(out, "v1.0.1") {
		t.Errorf("reinstall output:\n%s", out)
	}
	if got := readFile(t, binary); got != "program two" {
		t.Errorf("binary after reinstall = %q", got)
	}
	if got := readFile(t, filepath.Join(e.prefix, "share/doc/qatlas/LICENSE")); got != "license v1.0.1\n" {
		t.Errorf("license after reinstall = %q", got)
	}
	if got := readFile(t, profile); strings.Count(got, "Qatlas installer") != 1 || strings.Count(got, "export PATH") != 1 {
		t.Errorf("profile after reinstall = %q", got)
	}
	if left := e.stageLeftovers(); len(left) != 0 {
		t.Errorf("staging left behind: %v", left)
	}
}

func TestInstallLeavesQatlasConfigUntouched(t *testing.T) {
	e := newInstallEnv(t)
	cli := filepath.Join(e.home, ".qatlas", "cli")
	if err := os.MkdirAll(cli, 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(cli, "config.json")
	if err := os.WriteFile(config, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.publish("v1.0.0", "program", releaseOptions{})
	e.mustRun(nil)
	e.mustRun(nil)
	if got := readFile(t, config); got != "secret" {
		t.Errorf("config = %q", got)
	}
	entries, _ := os.ReadDir(cli)
	if len(entries) != 1 {
		t.Errorf("~/.qatlas/cli changed: %v", entries)
	}
}

func TestInstallRejectsBadReleaseWithoutChange(t *testing.T) {
	other := releasetest.NewSigner(t)
	tests := map[string]struct {
		opts    releaseOptions
		env     []string
		message string
	}{
		"wrong checksum":    {releaseOptions{wrongChecksum: true}, nil, "SHA-256 mismatch"},
		"foreign signature": {releaseOptions{signer: other}, nil, "signature"},
		"embedded release key rejects test signature": {releaseOptions{}, []string{"QATLAS_INSTALL_ALLOWED_SIGNERS_FILE="}, "signature"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			e := newInstallEnv(t)
			e.publish("v1.0.0", "old program", releaseOptions{})
			e.mustRun(nil)
			profile := readFile(t, filepath.Join(e.home, ".profile"))

			e.publish("v1.0.1", "new program", tc.opts)
			out, err := e.run(false, tc.env)
			if err == nil {
				t.Fatalf("install succeeded:\n%s", out)
			}
			if !strings.Contains(out, tc.message) {
				t.Errorf("output lacks %q:\n%s", tc.message, out)
			}
			if got := readFile(t, filepath.Join(e.prefix, "bin", "qatlas")); got != "old program" {
				t.Errorf("binary changed to %q", got)
			}
			if got := readFile(t, filepath.Join(e.prefix, "share/doc/qatlas/LICENSE")); got != "license v1.0.0\n" {
				t.Errorf("license changed to %q", got)
			}
			if got := readFile(t, filepath.Join(e.home, ".profile")); got != profile {
				t.Errorf("profile changed to %q", got)
			}
			if left := e.stageLeftovers(); len(left) != 0 {
				t.Errorf("staging left behind: %v", left)
			}
		})
	}
}

func TestInstallFailureOnFreshPrefixCreatesNothing(t *testing.T) {
	e := newInstallEnv(t)
	e.publish("v1.0.0", "program", releaseOptions{wrongChecksum: true})
	if out, err := e.run(false, nil); err == nil {
		t.Fatalf("install succeeded:\n%s", out)
	}
	entries, _ := os.ReadDir(e.home)
	if len(entries) != 0 {
		t.Errorf("home changed: %v", entries)
	}
}

func TestInstallPinnedVersions(t *testing.T) {
	e := newInstallEnv(t)
	e.publish("v1.2.3-rc.1", "release candidate", releaseOptions{})
	e.publish("v1.0.0", "stable", releaseOptions{})

	out, err := e.run(true, nil, "v1.2.3-rc.1")
	if err != nil {
		t.Fatalf("argument: %v\n%s", err, out)
	}
	if got := readFile(t, filepath.Join(e.prefix, "bin", "qatlas")); got != "release candidate" {
		t.Errorf("binary = %q", got)
	}
	e.mustRun(nil)
	if got := readFile(t, filepath.Join(e.prefix, "bin", "qatlas")); got != "stable" {
		t.Errorf("latest binary = %q", got)
	}
	e.mustRun([]string{"QATLAS_VERSION=v1.2.3-rc.1"})
	if got := readFile(t, filepath.Join(e.prefix, "bin", "qatlas")); got != "release candidate" {
		t.Errorf("QATLAS_VERSION binary = %q", got)
	}
	if out, err := e.run(false, nil, "latest"); err == nil || !strings.Contains(out, "invalid version") {
		t.Errorf("invalid version: %v\n%s", err, out)
	}
}

func TestInstallPathHandling(t *testing.T) {
	t.Run("already on PATH", func(t *testing.T) {
		e := newInstallEnv(t)
		e.publish("v1.0.0", "program", releaseOptions{})
		e.path = filepath.Join(e.prefix, "bin") + string(os.PathListSeparator) + e.path
		out := e.mustRun(nil)
		if strings.Contains(out, "new terminal") {
			t.Errorf("unexpected PATH hint:\n%s", out)
		}
		if _, err := os.Stat(filepath.Join(e.home, ".profile")); !os.IsNotExist(err) {
			t.Errorf("profile written: %v", err)
		}
	})
	t.Run("zsh", func(t *testing.T) {
		e := newInstallEnv(t)
		e.shell = "/usr/bin/zsh"
		e.publish("v1.0.0", "program", releaseOptions{})
		e.mustRun(nil)
		e.mustRun(nil)
		if got := readFile(t, filepath.Join(e.home, ".zprofile")); strings.Count(got, "export PATH") != 1 {
			t.Errorf(".zprofile = %q", got)
		}
		if _, err := os.Stat(filepath.Join(e.home, ".profile")); !os.IsNotExist(err) {
			t.Errorf(".profile written: %v", err)
		}
	})
	t.Run("existing profile is appended to", func(t *testing.T) {
		e := newInstallEnv(t)
		profile := filepath.Join(e.home, ".profile")
		if err := os.WriteFile(profile, []byte("# mine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		e.publish("v1.0.0", "program", releaseOptions{})
		e.mustRun(nil)
		if got := readFile(t, profile); !strings.HasPrefix(got, "# mine\n") || strings.Count(got, "export PATH") != 1 {
			t.Errorf("profile = %q", got)
		}
	})
}
