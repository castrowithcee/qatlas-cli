package scripts

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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

// psShells lists the PowerShell flavours available for running install.ps1: Windows PowerShell 5.1 on
// Windows, and PowerShell 7 wherever pwsh is installed.
func psShells() []string {
	var shells []string
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath("powershell.exe"); err == nil {
			shells = append(shells, p)
		}
	}
	if p, err := exec.LookPath("pwsh"); err == nil {
		shells = append(shells, p)
	}
	return shells
}

func shellName(shell string) string {
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(shell)), ".exe")
	return name
}

// psEnv is the harness of the Windows installer tests: a fake release server, a test signing key, and
// temporary USERPROFILE, LOCALAPPDATA and user PATH stand-in so the real machine is never touched.
type psEnv struct {
	t        *testing.T
	shell    string
	server   *releasetest.Server
	signer   ssh.Signer
	signers  string
	home     string
	prefix   string
	pathFile string
}

func newPSEnv(t *testing.T, shell string) *psEnv {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("install.ps1 runs on Windows only")
	}
	e := &psEnv{t: t, shell: shell, server: releasetest.NewServer(t), signer: releasetest.NewSigner(t)}
	root := t.TempDir()
	e.home = filepath.Join(root, "home")
	e.prefix = filepath.Join(root, "local", "Programs", "qatlas")
	e.pathFile = filepath.Join(root, "user-path.txt")
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

func needSSHKeygen(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen is not installed")
	}
}

const zipArchive = "qatlas_%s_windows_amd64.zip"

func zipName(tag string) string { return strings.Replace(zipArchive, "%s", tag, 1) }

// zipball builds a Windows release archive with the layout of scripts/release.sh.
func zipball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// publish adds a release whose qatlas.exe is the text program and makes it the latest.
func (e *psEnv) publish(tag, program string, opts releaseOptions) {
	e.t.Helper()
	archive := zipball(e.t, map[string]string{
		"bin/qatlas.exe":           program,
		"share/doc/qatlas/LICENSE": "license " + tag + "\n",
	})
	sum := sha256.Sum256(archive)
	hash := hex.EncodeToString(sum[:])
	if opts.wrongChecksum {
		hash = strings.Repeat("0", 64)
	}
	checksums := []byte(hash + "  " + zipName(tag) + "\n" +
		strings.Repeat("1", 64) + "  install.ps1\n")
	signer := opts.signer
	if signer == nil {
		signer = e.signer
	}
	e.server.Publish(tag, map[string][]byte{
		zipName(tag):        archive,
		"checksums.txt":     checksums,
		"checksums.txt.sig": releasetest.Sign(e.t, signer, release.Namespace, "sha512", checksums),
	})
}

// run pipes scripts/install.ps1 into Invoke-Expression, like `irm ... | iex`, with errors made fatal so a
// failed installation yields a non-zero exit code. env entries are added to the environment.
func (e *psEnv) run(env ...string) (string, error) {
	e.t.Helper()
	script, err := filepath.Abs("install.ps1")
	if err != nil {
		e.t.Fatal(err)
	}
	command := "$ErrorActionPreference = 'Stop'; Get-Content -Raw -LiteralPath '" + script + "' | Invoke-Expression"
	cmd := exec.Command(e.shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command)
	cmd.Env = append(os.Environ(),
		"USERPROFILE="+e.home,
		"HOME="+e.home,
		"LOCALAPPDATA="+filepath.Join(filepath.Dir(filepath.Dir(e.prefix)), "local"),
		"TEMP="+e.t.TempDir(),
		"TMP="+e.t.TempDir(),
		"QATLAS_VERSION=",
		"QATLAS_INSTALL_BASE_URL="+e.server.URL,
		"QATLAS_INSTALL_PREFIX="+e.prefix,
		"QATLAS_INSTALL_ALLOWED_SIGNERS_FILE="+e.signers,
		"QATLAS_INSTALL_USER_PATH_FILE="+e.pathFile,
	)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (e *psEnv) mustRun(env ...string) string {
	e.t.Helper()
	out, err := e.run(env...)
	if err != nil {
		e.t.Fatalf("install.ps1 failed: %v\n%s", err, out)
	}
	return out
}

func (e *psEnv) exe() string { return filepath.Join(e.prefix, "bin", "qatlas.exe") }

func (e *psEnv) stageLeftovers() []string {
	matches, _ := filepath.Glob(filepath.Join(e.prefix, ".qatlas-stage.*"))
	return matches
}

// userPathRegistry returns the real HKCU PATH so tests can prove it stays unchanged.
func userPathRegistry() string {
	out, _ := exec.Command("reg", "query", `HKCU\Environment`, "/v", "Path").CombinedOutput()
	return string(out)
}

// forEachShell runs the test body once per available PowerShell.
func forEachShell(t *testing.T, body func(t *testing.T, e *psEnv)) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("install.ps1 runs on Windows only")
	}
	shells := psShells()
	if len(shells) == 0 {
		t.Skip("no PowerShell found")
	}
	for _, shell := range shells {
		t.Run(shellName(shell), func(t *testing.T) {
			realPath := userPathRegistry()
			body(t, newPSEnv(t, shell))
			if got := userPathRegistry(); got != realPath {
				t.Errorf("the real user PATH changed:\n%s\n-->\n%s", realPath, got)
			}
		})
	}
}

func TestInstallPS1Syntax(t *testing.T) {
	shells := psShells()
	if len(shells) == 0 {
		if p, err := exec.LookPath("pwsh"); err == nil {
			shells = append(shells, p)
		}
	}
	if len(shells) == 0 {
		t.Skip("no PowerShell found")
	}
	script, err := filepath.Abs("install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	command := "$errs = $null; [void][System.Management.Automation.Language.Parser]::ParseFile('" + script +
		"', [ref]$null, [ref]$errs); if ($errs -and $errs.Count) { $errs | ForEach-Object { $_.ToString() }; exit 1 }"
	for _, shell := range shells {
		t.Run(shellName(shell), func(t *testing.T) {
			if out, err := exec.Command(shell, "-NoProfile", "-NonInteractive", "-Command", command).CombinedOutput(); err != nil {
				t.Fatalf("install.ps1 does not parse: %v\n%s", err, out)
			}
		})
	}
}

func TestInstallPS1FreshAndReinstall(t *testing.T) {
	forEachShell(t, func(t *testing.T, e *psEnv) {
		e.publish("v1.0.0", "program one", releaseOptions{})
		out := e.mustRun()
		for _, want := range []string{"v1.0.0", e.exe(), "new terminal", "qatlas tui", "qatlas web"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		}
		if got := readFile(t, e.exe()); got != "program one" {
			t.Errorf("binary = %q", got)
		}
		if got := readFile(t, filepath.Join(e.prefix, `share\doc\qatlas\LICENSE`)); got != "license v1.0.0\n" {
			t.Errorf("license = %q", got)
		}
		if _, err := os.Stat(filepath.Join(e.home, ".qatlas")); !os.IsNotExist(err) {
			t.Errorf(".qatlas was created: %v", err)
		}
		binDir := filepath.Join(e.prefix, "bin")
		if got := readFile(t, e.pathFile); got != binDir {
			t.Errorf("user PATH = %q, want %q", got, binDir)
		}

		e.publish("v1.0.1", "program two", releaseOptions{})
		out = e.mustRun()
		if !strings.Contains(out, "v1.0.1") {
			t.Errorf("reinstall output:\n%s", out)
		}
		if strings.Contains(out, "new terminal") {
			t.Errorf("unexpected PATH hint on reinstall:\n%s", out)
		}
		if got := readFile(t, e.exe()); got != "program two" {
			t.Errorf("binary after reinstall = %q", got)
		}
		if got := readFile(t, e.exe()+".old"); got != "program one" {
			t.Errorf("qatlas.exe.old = %q", got)
		}
		if got := readFile(t, filepath.Join(e.prefix, `share\doc\qatlas\LICENSE`)); got != "license v1.0.1\n" {
			t.Errorf("license after reinstall = %q", got)
		}
		if got := readFile(t, e.pathFile); got != binDir {
			t.Errorf("user PATH after reinstall = %q", got)
		}

		// A third run replaces an existing .old as well.
		e.publish("v1.0.2", "program three", releaseOptions{})
		e.mustRun()
		if got := readFile(t, e.exe()+".old"); got != "program two" {
			t.Errorf("qatlas.exe.old after third run = %q", got)
		}
		if left := e.stageLeftovers(); len(left) != 0 {
			t.Errorf("staging left behind: %v", left)
		}
	})
}

func TestInstallPS1PathHandling(t *testing.T) {
	forEachShell(t, func(t *testing.T, e *psEnv) {
		binDir := filepath.Join(e.prefix, "bin")
		if err := os.WriteFile(e.pathFile, []byte(`C:\Tools;`), 0o644); err != nil {
			t.Fatal(err)
		}
		e.publish("v1.0.0", "program", releaseOptions{})
		e.mustRun()
		if got := readFile(t, e.pathFile); got != `C:\Tools;`+binDir {
			t.Errorf("user PATH = %q", got)
		}
		// Already present, including a different case and a trailing backslash.
		if err := os.WriteFile(e.pathFile, []byte(strings.ToUpper(binDir)+`\;C:\Tools`), 0o644); err != nil {
			t.Fatal(err)
		}
		out := e.mustRun()
		if got := readFile(t, e.pathFile); got != strings.ToUpper(binDir)+`\;C:\Tools` {
			t.Errorf("user PATH changed to %q", got)
		}
		if strings.Contains(out, "new terminal") {
			t.Errorf("unexpected PATH hint:\n%s", out)
		}
	})
}

func TestInstallPS1LeavesQatlasConfigUntouched(t *testing.T) {
	forEachShell(t, func(t *testing.T, e *psEnv) {
		cli := filepath.Join(e.home, ".qatlas", "cli")
		if err := os.MkdirAll(cli, 0o700); err != nil {
			t.Fatal(err)
		}
		config := filepath.Join(cli, "config.json")
		if err := os.WriteFile(config, []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		e.publish("v1.0.0", "program", releaseOptions{})
		e.mustRun()
		e.mustRun()
		if got := readFile(t, config); got != "secret" {
			t.Errorf("config = %q", got)
		}
		if entries, _ := os.ReadDir(cli); len(entries) != 1 {
			t.Errorf(".qatlas\\cli changed: %v", entries)
		}
	})
}

func TestInstallPS1RejectsBadReleaseWithoutChange(t *testing.T) {
	needSSHKeygen(t)
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
			forEachShell(t, func(t *testing.T, e *psEnv) {
				e.publish("v1.0.0", "old program", releaseOptions{})
				e.mustRun()
				pathBefore := readFile(t, e.pathFile)

				e.publish("v1.0.1", "new program", tc.opts)
				out, err := e.run(tc.env...)
				if err == nil {
					t.Fatalf("install succeeded:\n%s", out)
				}
				if !strings.Contains(out, tc.message) {
					t.Errorf("output lacks %q:\n%s", tc.message, out)
				}
				if got := readFile(t, e.exe()); got != "old program" {
					t.Errorf("binary changed to %q", got)
				}
				if _, err := os.Stat(e.exe() + ".old"); !os.IsNotExist(err) {
					t.Errorf("qatlas.exe.old exists: %v", err)
				}
				if got := readFile(t, filepath.Join(e.prefix, `share\doc\qatlas\LICENSE`)); got != "license v1.0.0\n" {
					t.Errorf("license changed to %q", got)
				}
				if got := readFile(t, e.pathFile); got != pathBefore {
					t.Errorf("user PATH changed to %q", got)
				}
				if left := e.stageLeftovers(); len(left) != 0 {
					t.Errorf("staging left behind: %v", left)
				}
			})
		})
	}
}

func TestInstallPS1FailureOnFreshPrefixCreatesNothing(t *testing.T) {
	forEachShell(t, func(t *testing.T, e *psEnv) {
		e.publish("v1.0.0", "program", releaseOptions{wrongChecksum: true})
		if out, err := e.run(); err == nil {
			t.Fatalf("install succeeded:\n%s", out)
		}
		for _, path := range []string{e.prefix, e.pathFile} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("%s exists: %v", path, err)
			}
		}
		if entries, _ := os.ReadDir(e.home); len(entries) != 0 {
			t.Errorf("home changed: %v", entries)
		}
	})
}

func TestInstallPS1PinnedVersions(t *testing.T) {
	forEachShell(t, func(t *testing.T, e *psEnv) {
		e.publish("v1.2.3-rc.1", "release candidate", releaseOptions{})
		e.publish("v1.0.0", "stable", releaseOptions{})

		e.mustRun()
		if got := readFile(t, e.exe()); got != "stable" {
			t.Errorf("latest binary = %q", got)
		}
		e.mustRun("QATLAS_VERSION=v1.2.3-rc.1")
		if got := readFile(t, e.exe()); got != "release candidate" {
			t.Errorf("QATLAS_VERSION binary = %q", got)
		}
		if out, err := e.run("QATLAS_VERSION=latest"); err == nil || !strings.Contains(out, "invalid version") {
			t.Errorf("invalid version: %v\n%s", err, out)
		}
	})
}
