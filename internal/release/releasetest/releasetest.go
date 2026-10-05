// Package releasetest creates SSH signatures in tests.
package releasetest

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// NewSigner returns a fresh Ed25519 signer.
func NewSigner(t testing.TB) ssh.Signer {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

// Sign returns an armored SSHSIG over data made with signer for namespace and the hash "sha256" or
// "sha512".
func Sign(t testing.TB, signer ssh.Signer, namespace, hash string, data []byte) []byte {
	t.Helper()
	var digest []byte
	switch hash {
	case "sha256":
		sum := sha256.Sum256(data)
		digest = sum[:]
	case "sha512":
		sum := sha512.Sum512(data)
		digest = sum[:]
	default:
		digest = []byte("unknown")
	}
	magic := [6]byte{'S', 'S', 'H', 'S', 'I', 'G'}
	signed := ssh.Marshal(struct {
		Magic     [6]byte
		Namespace string
		Reserved  string
		Hash      string
		Digest    []byte
	}{magic, namespace, "", hash, digest})
	sig, err := signer.Sign(rand.Reader, signed)
	if err != nil {
		t.Fatal(err)
	}
	blob := ssh.Marshal(struct {
		Magic     [6]byte
		Version   uint32
		PublicKey []byte
		Namespace string
		Reserved  string
		Hash      string
		Signature []byte
	}{magic, 1, signer.PublicKey().Marshal(), namespace, "", hash, ssh.Marshal(struct {
		Format string
		Blob   []byte
	}{sig.Format, sig.Blob})})
	encoded := base64.StdEncoding.EncodeToString(blob)
	var out strings.Builder
	out.WriteString("-----BEGIN SSH SIGNATURE-----\n")
	for len(encoded) > 70 {
		out.WriteString(encoded[:70] + "\n")
		encoded = encoded[70:]
	}
	out.WriteString(encoded + "\n-----END SSH SIGNATURE-----\n")
	return []byte(out.String())
}
