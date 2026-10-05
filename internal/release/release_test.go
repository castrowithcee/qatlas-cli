package release

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/castrowithcee/qatlas-cli/internal/release/releasetest"
)

const checksumsText = "0000000000000000000000000000000000000000000000000000000000000001  qatlas_v1.0.0_linux_amd64.tar.gz\n"

func TestVerifyChecksums(t *testing.T) {
	signer := releasetest.NewSigner(t)
	other := releasetest.NewSigner(t)
	keys := []ssh.PublicKey{other.PublicKey(), signer.PublicKey()}
	data := []byte(checksumsText)

	for _, hash := range []string{"sha256", "sha512"} {
		if err := VerifyChecksums(data, releasetest.Sign(t, signer, Namespace, hash, data), keys); err != nil {
			t.Errorf("%s: %v", hash, err)
		}
	}

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaSigner, err := ssh.NewSignerFromKey(rsaKey)
	if err != nil {
		t.Fatal(err)
	}
	good := releasetest.Sign(t, signer, Namespace, "sha512", data)
	tests := map[string]struct {
		data, sig []byte
		keys      []ssh.PublicKey
	}{
		"tampered data":   {[]byte(checksumsText + "x"), good, keys},
		"unknown key":     {data, releasetest.Sign(t, releasetest.NewSigner(t), Namespace, "sha512", data), keys},
		"no keys":         {data, good, nil},
		"wrong namespace": {data, releasetest.Sign(t, signer, "file", "sha512", data), keys},
		"unknown hash":    {data, releasetest.Sign(t, signer, Namespace, "md5", data), keys},
		"rsa key":         {data, releasetest.Sign(t, rsaSigner, Namespace, "sha512", data), append(keys, rsaSigner.PublicKey())},
		"not armored":     {data, []byte("hello"), keys},
		"bad base64":      {data, []byte("-----BEGIN SSH SIGNATURE-----\n!!!\n-----END SSH SIGNATURE-----\n"), keys},
		"short blob":      {data, []byte("-----BEGIN SSH SIGNATURE-----\nU1NIU0lH\n-----END SSH SIGNATURE-----\n"), keys},
		"truncated":       {data, []byte(strings.Join(strings.SplitN(string(good), "\n", 3)[:2], "\n") + "\n-----END SSH SIGNATURE-----\n"), keys},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if err := VerifyChecksums(tt.data, tt.sig, tt.keys); err == nil {
				t.Fatal("VerifyChecksums() = nil, want error")
			}
		})
	}
}

func TestVerifyChecksumsAcceptsSSHKeygenSignature(t *testing.T) {
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen is not installed")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	if out, err := exec.Command(keygen, "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v: %s", err, out)
	}
	file := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(file, []byte(checksumsText), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(keygen, "-Y", "sign", "-f", key, "-n", Namespace, file).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen -Y sign: %v: %s", err, out)
	}
	signature, err := os.ReadFile(file + ".sig")
	if err != nil {
		t.Fatal(err)
	}
	publicLine, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	public, _, _, _, err := ssh.ParseAuthorizedKey(publicLine)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyChecksums([]byte(checksumsText), signature, []ssh.PublicKey{public}); err != nil {
		t.Fatal(err)
	}
	if err := VerifyChecksums([]byte(checksumsText+"x"), signature, []ssh.PublicKey{public}); err == nil {
		t.Fatal("tampered checksums accepted")
	}
}

func TestChecksumFor(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	body := []byte(sum + "  a.tar.gz\n" + strings.ToUpper(sum) + " *b.zip\nzz  c.zip\n")
	for name, want := range map[string]string{"a.tar.gz": sum, "b.zip": sum} {
		got, err := ChecksumFor(body, name)
		if err != nil || got != want {
			t.Errorf("ChecksumFor(%s) = %q, %v", name, got, err)
		}
	}
	for _, name := range []string{"c.zip", "missing.zip"} {
		if _, err := ChecksumFor(body, name); err == nil {
			t.Errorf("ChecksumFor(%s) = nil error", name)
		}
	}
}

func TestProgramSHA256(t *testing.T) {
	program := []byte("program bytes")
	sum := sha256.Sum256(program)
	want := hex.EncodeToString(sum[:])

	var tarball bytes.Buffer
	gz := gzip.NewWriter(&tarball)
	tw := tar.NewWriter(gz)
	for name, body := range map[string][]byte{"share/man/man1/qatlas.1": []byte("man"), "bin/qatlas": program} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write(body)
	}
	tw.Close()
	gz.Close()

	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	w, _ := zw.Create("bin/qatlas.exe")
	w.Write(program)
	zw.Close()

	if got, err := ProgramSHA256("qatlas_v1_linux_amd64.tar.gz", tarball.Bytes()); err != nil || got != want {
		t.Errorf("tar.gz: %q, %v", got, err)
	}
	if got, err := ProgramSHA256("qatlas_v1_windows_amd64.zip", archive.Bytes()); err != nil || got != want {
		t.Errorf("zip: %q, %v", got, err)
	}
	if _, err := ProgramSHA256("qatlas_v1_windows_amd64.zip", tarball.Bytes()); err == nil {
		t.Error("tar.gz bytes as zip accepted")
	}
	if _, err := ProgramSHA256("qatlas_v1_linux_amd64.tar.gz", archive.Bytes()); err == nil {
		t.Error("zip bytes as tar.gz accepted")
	}
}

func TestTrustedKeysMatchAllowedSigners(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "release-allowed-signers"))
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != "qatlas-release" {
			t.Fatalf("unexpected allowed signers line %q", line)
		}
		want = append(want, fields[1]+" "+fields[2])
	}
	got := TrustedKeyLines()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("compiled keys %q differ from .github/release-allowed-signers %q", got, want)
	}
	var wantLines []string
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		wantLines = append(wantLines, line)
	}
	for _, installer := range []struct{ file, prefix, suffix string }{
		{"install.sh", "signer_line='", "'"},
		{"install.ps1", "$signerLine = '", "'"},
	} {
		script, err := os.ReadFile(filepath.Join("..", "..", "scripts", installer.file))
		if err != nil {
			t.Fatal(err)
		}
		var embedded []string
		for _, line := range strings.Split(string(script), "\n") {
			if rest, ok := strings.CutPrefix(strings.TrimSpace(line), installer.prefix); ok {
				embedded = append(embedded, strings.TrimSuffix(rest, installer.suffix))
			}
		}
		if strings.Join(embedded, "\n") != strings.Join(wantLines, "\n") {
			t.Fatalf("scripts/%s embeds %q, want %q from .github/release-allowed-signers", installer.file, embedded, wantLines)
		}
	}
	if len(TrustedKeys()) < len(got) {
		t.Fatal("TrustedKeys() lost a key")
	}
}
