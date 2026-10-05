// Package release verifies the integrity data of a Qatlas CLI release without network access: the
// signature over checksums.txt, the archive checksum it lists, and the hash of the program inside an archive.
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

// Namespace is the SSHSIG namespace every release signature is made for.
const Namespace = "qatlas-release"

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

// TrustedKeyLines returns the production key list in authorized_keys format.
func TrustedKeyLines() []string { return append([]string(nil), trustedKeyLines...) }

// VerifyChecksums checks that signature is a valid SSHSIG over checksums for the release namespace, made
// by exactly one of keys. Only Ed25519 keys and the hashes sha256 and sha512 are accepted.
func VerifyChecksums(checksums, signature []byte, keys []ssh.PublicKey) error {
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
	if head.Namespace != Namespace {
		return fmt.Errorf("signature namespace is %q, want %q", head.Namespace, Namespace)
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

// ProgramSHA256 returns the lowercase hex SHA-256 of the program inside a release archive: bin/qatlas of a
// .tar.gz archive or bin/qatlas.exe of a .zip archive, chosen by the suffix of archiveName.
func ProgramSHA256(archiveName string, archive []byte) (string, error) {
	var sum [sha256.Size]byte
	var err error
	if strings.HasSuffix(archiveName, ".zip") {
		sum, err = zipProgram(archive)
	} else {
		sum, err = tarProgram(archive)
	}
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sum[:]), nil
}

func tarProgram(archive []byte) ([sha256.Size]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("open release archive: %w", err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return [sha256.Size]byte{}, errors.New("release archive does not contain bin/qatlas")
		}
		if err != nil {
			return [sha256.Size]byte{}, fmt.Errorf("read release archive: %w", err)
		}
		if header.Typeflag == tar.TypeReg && strings.TrimPrefix(header.Name, "./") == "bin/qatlas" {
			return hashEntry(reader)
		}
	}
}

func zipProgram(archive []byte) ([sha256.Size]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("open release archive: %w", err)
	}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() || strings.TrimPrefix(file.Name, "./") != "bin/qatlas.exe" {
			continue
		}
		entry, err := file.Open()
		if err != nil {
			return [sha256.Size]byte{}, fmt.Errorf("open %s in release archive: %w", file.Name, err)
		}
		defer entry.Close()
		return hashEntry(entry)
	}
	return [sha256.Size]byte{}, errors.New("release archive does not contain bin/qatlas.exe")
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
