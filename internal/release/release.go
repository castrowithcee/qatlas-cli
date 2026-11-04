// Package release verifies the integrity data of a qconnect release without network access: the signature
// over checksums.txt, the archive checksum it lists, and the hash of the program inside an archive. It also
// names the files of a release by its version.
package release

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/ssh"
)

const (
	// Namespace is the SSHSIG namespace of the signature a release after LastLegacyVersion is verified with,
	// published as SignatureAsset.
	Namespace = "qatlas-connect-release"
	// SignatureAsset is the release file that holds the signature under Namespace.
	SignatureAsset = "checksums.txt.qconnect.sig"

	// LegacyNamespace and LegacySignatureAsset are the signature of the releases up to LastLegacyVersion, the
	// only one their updaters check. A rollback to such a release is verified with it. A later release
	// carries it for those updaters as well, made with the same key, but is never verified with it here.
	// Transition until the next major release.
	LegacyNamespace      = "qatlas-release"
	LegacySignatureAsset = "checksums.txt.sig"

	// LastLegacyVersion is the newest release before the rename: the last one whose assets are named qatlas
	// and that is signed under LegacyNamespace only. Every later version, prereleases included, ships
	// qconnect assets and is verified under Namespace, whatever number the first of them gets. The version a
	// release is checked for is the one asked for, never inferred from the files a release offers, which an
	// attacker can leave out; the signed checksums.txt binds it, since it lists each archive by a name that
	// carries the version.
	LastLegacyVersion = "v2.2.0"
)

const (
	sshsigMagic    = "SSHSIG"
	armorBegin     = "-----BEGIN SSH SIGNATURE-----"
	armorEnd       = "-----END SSH SIGNATURE-----"
	maxProgramSize = 64 << 20
)

// trustedKeyLines lists the public keys that may sign a release, in authorized_keys format. A successor
// key is added one release before a rotation so that installed programs already trust it. The list is
// fixed at build time: nothing at run time extends or replaces it. It must match
// .github/release-allowed-signers.
var trustedKeyLines = []string{
	"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ6euNyMyw+0rV5TVSa24o374+wDn75ueHvdYAGZnEv5",
}

// extraKeyLines is set at build time only (-ldflags -X) to trust further keys, one authorized_keys line
// per line, so that end-to-end tests can run real binaries against test releases.
var extraKeyLines string

// TrustedKeys returns the compiled-in public keys that may sign a release.
func TrustedKeys() []ssh.PublicKey {
	lines := append([]string(nil), trustedKeyLines...)
	for _, line := range strings.Split(extraKeyLines, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	keys := make([]ssh.PublicKey, 0, len(lines))
	for _, line := range lines {
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			panic("release: invalid compiled-in public key: " + err.Error())
		}
		keys = append(keys, key)
	}
	return keys
}

// Layout names the files of a release by its version: up to LastLegacyVersion the former names, after it
// the current ones.
type Layout struct {
	// Legacy marks a release up to LastLegacyVersion. Transition until the next major release.
	Legacy bool
	// Program is the name of the program, which also names its archive and manual page.
	Program string
	// SignatureAsset is the release file that holds the signature, and Namespace its SSHSIG namespace.
	SignatureAsset, Namespace string
}

// LayoutOf returns the layout of the release version.
func LayoutOf(version string) (Layout, error) {
	v, err := ParseVersion(version)
	if err != nil {
		return Layout{}, err
	}
	if v.Compare(lastLegacy()) <= 0 {
		return Layout{Legacy: true, Program: "qatlas", SignatureAsset: LegacySignatureAsset, Namespace: LegacyNamespace}, nil
	}
	return Layout{Program: "qconnect", SignatureAsset: SignatureAsset, Namespace: Namespace}, nil
}

func lastLegacy() Version {
	v, err := ParseVersion(LastLegacyVersion)
	if err != nil {
		panic(err)
	}
	return v
}

// ArchiveName is the name of the archive of version for goos and goarch: a .zip for Windows, else a .tar.gz.
func (l Layout) ArchiveName(version, goos, goarch string) string {
	extension := ".tar.gz"
	if goos == "windows" {
		extension = ".zip"
	}
	return fmt.Sprintf("%s_%s_%s_%s%s", l.Program, version, goos, goarch, extension)
}

// ProgramFile is the program's path inside an archive for goos.
func (l Layout) ProgramFile(goos string) string {
	if goos == "windows" {
		return "bin/" + l.Program + ".exe"
	}
	return "bin/" + l.Program
}

// ManpageFile is the manual page's path inside an archive.
func (l Layout) ManpageFile() string { return "share/man/man1/" + l.Program + ".1" }

// ParseArchiveName reads the layout, version, and platform an archive name stands for. A name is only
// accepted in the exact form ArchiveName gives the layout of its version.
func ParseArchiveName(name string) (layout Layout, version, goos string, err error) {
	parts := strings.SplitN(name, "_", 4)
	if len(parts) != 4 {
		return Layout{}, "", "", fmt.Errorf("%q is not a release archive name", name)
	}
	version, goos = parts[1], parts[2]
	goarch, _, _ := strings.Cut(parts[3], ".")
	layout, err = LayoutOf(version)
	if err != nil || layout.ArchiveName(version, goos, goarch) != name {
		return Layout{}, "", "", fmt.Errorf("%q is not a release archive name", name)
	}
	return layout, version, goos, nil
}

// VerifyRelease checks that signature is a valid signature over checksums for the release archiveName
// belongs to: under Namespace for a version after LastLegacyVersion, under LegacyNamespace up to it. See
// VerifyChecksums.
func VerifyRelease(checksums, signature []byte, archiveName string, keys []ssh.PublicKey) error {
	layout, _, _, err := ParseArchiveName(archiveName)
	if err != nil {
		return err
	}
	return VerifyChecksums(checksums, signature, layout.Namespace, keys)
}

// VerifyChecksums checks that signature is a valid SSHSIG over checksums for namespace, made by exactly one
// of keys. Only Ed25519 keys and the hashes sha256 and sha512 are accepted.
func VerifyChecksums(checksums, signature []byte, namespace string, keys []ssh.PublicKey) error {
	blob, err := decodeArmor(signature)
	if err != nil {
		return err
	}
	var head struct {
		Magic     [6]byte
		Version   uint32
		PublicKey []byte
		Namespace string
		Reserved  string
		Hash      string
		Signature []byte
	}
	if len(blob) < len(sshsigMagic) || string(blob[:len(sshsigMagic)]) != sshsigMagic {
		return errors.New("signature is not an SSH signature")
	}
	if err := ssh.Unmarshal(blob, &head); err != nil {
		return fmt.Errorf("parse signature: %w", err)
	}
	if head.Version != 1 {
		return fmt.Errorf("unsupported signature version %d", head.Version)
	}
	if head.Namespace != namespace {
		return fmt.Errorf("signature namespace is %q, want %q", head.Namespace, namespace)
	}
	var digest []byte
	switch head.Hash {
	case "sha256":
		sum := sha256.Sum256(checksums)
		digest = sum[:]
	case "sha512":
		sum := sha512.Sum512(checksums)
		digest = sum[:]
	default:
		return fmt.Errorf("unsupported signature hash %q", head.Hash)
	}
	key, err := ssh.ParsePublicKey(head.PublicKey)
	if err != nil {
		return fmt.Errorf("parse signing key: %w", err)
	}
	if key.Type() != ssh.KeyAlgoED25519 {
		return fmt.Errorf("unsupported signing key type %s", key.Type())
	}
	trusted := false
	for _, candidate := range keys {
		if candidate.Type() == key.Type() && bytes.Equal(candidate.Marshal(), key.Marshal()) {
			trusted = true
			break
		}
	}
	if !trusted {
		return fmt.Errorf("signing key %s is not trusted", ssh.FingerprintSHA256(key))
	}
	var sig struct {
		Format string
		Blob   []byte
		Rest   []byte `ssh:"rest"`
	}
	if err := ssh.Unmarshal(head.Signature, &sig); err != nil {
		return fmt.Errorf("parse signature: %w", err)
	}
	if sig.Format != ssh.KeyAlgoED25519 {
		return fmt.Errorf("unsupported signature format %q", sig.Format)
	}
	signed := ssh.Marshal(struct {
		Magic     [6]byte
		Namespace string
		Reserved  string
		Hash      string
		Digest    []byte
	}{[6]byte{'S', 'S', 'H', 'S', 'I', 'G'}, head.Namespace, head.Reserved, head.Hash, digest})
	if err := key.Verify(signed, &ssh.Signature{Format: sig.Format, Blob: sig.Blob}); err != nil {
		return fmt.Errorf("signature does not match checksums.txt: %w", err)
	}
	return nil
}

func decodeArmor(signature []byte) ([]byte, error) {
	text := strings.TrimSpace(strings.ReplaceAll(string(signature), "\r\n", "\n"))
	if !strings.HasPrefix(text, armorBegin) || !strings.HasSuffix(text, armorEnd) {
		return nil, errors.New("signature is not an armored SSH signature")
	}
	body := strings.TrimSuffix(strings.TrimPrefix(text, armorBegin), armorEnd)
	blob, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(body), ""))
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}
	return blob, nil
}

// ChecksumFor returns the lowercase hex SHA-256 that checksums lists for archiveName. The caller
// verifies checksums with VerifyChecksums first.
func ChecksumFor(checksums []byte, archiveName string) (string, error) {
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != archiveName {
			continue
		}
		if decoded, err := hex.DecodeString(fields[0]); err == nil && len(decoded) == sha256.Size {
			return strings.ToLower(fields[0]), nil
		}
		return "", fmt.Errorf("checksums.txt contains an invalid SHA-256 for %s", archiveName)
	}
	return "", fmt.Errorf("checksums.txt does not contain %s", archiveName)
}

// ProgramSHA256 returns the lowercase hex SHA-256 of the program inside a release archive, at the place
// the layout of archiveName's version names for its platform (see ParseArchiveName).
func ProgramSHA256(archiveName string, archive []byte) (string, error) {
	layout, _, goos, err := ParseArchiveName(archiveName)
	if err != nil {
		return "", err
	}
	program := layout.ProgramFile(goos)
	var sum [sha256.Size]byte
	if goos == "windows" {
		sum, err = zipProgram(archive, program)
	} else {
		sum, err = tarProgram(archive, program)
	}
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sum[:]), nil
}

func tarProgram(archive []byte, program string) ([sha256.Size]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("open release archive: %w", err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return [sha256.Size]byte{}, fmt.Errorf("release archive does not contain %s", program)
		}
		if err != nil {
			return [sha256.Size]byte{}, fmt.Errorf("read release archive: %w", err)
		}
		if header.Typeflag == tar.TypeReg && strings.TrimPrefix(header.Name, "./") == program {
			return hashEntry(reader)
		}
	}
}

func zipProgram(archive []byte, program string) ([sha256.Size]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("open release archive: %w", err)
	}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() || strings.TrimPrefix(file.Name, "./") != program {
			continue
		}
		entry, err := file.Open()
		if err != nil {
			return [sha256.Size]byte{}, fmt.Errorf("open %s in release archive: %w", file.Name, err)
		}
		defer entry.Close()
		return hashEntry(entry)
	}
	return [sha256.Size]byte{}, fmt.Errorf("release archive does not contain %s", program)
}

func hashEntry(reader io.Reader) ([sha256.Size]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxProgramSize+1))
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("read program: %w", err)
	}
	if len(body) > maxProgramSize {
		return [sha256.Size]byte{}, fmt.Errorf("program exceeds %d bytes", maxProgramSize)
	}
	return sha256.Sum256(body), nil
}
