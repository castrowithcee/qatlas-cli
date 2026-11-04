//go:build !legacy_vault_names

package vaultproc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"testing"

	"filippo.io/age"
)

// The names this release and the one before qatlas-connect reach their vault process by.
func TestNamesOfThisAndTheEarlierRelease(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{proofLabel, "qatlas-connect vault process proof v2\x00"},
		{runtimeDirName, "qatlas-connect"},
		{pipeName, "qatlas-connect-vault-"},
		{legacyProofLabel, "qatlas vault process proof v2\x00"},
		{legacyRuntimeDirName, "qatlas"},
		{legacyPipeName, "qatlas-vault-"},
	} {
		if tc.got != tc.want {
			t.Errorf("name = %q, want %q", tc.got, tc.want)
		}
	}
}

// legacyProofOf is the proof a vault process of the earlier release derives from a nonce.
func legacyProofOf(nonce []byte) []byte {
	sum := sha256.Sum256(append([]byte(legacyProofLabel), nonce...))
	return sum[:]
}

// legacySolvedWith answers a challenge the way a vault process of the earlier release does: with the vault's
// key, under the earlier label.
func legacySolvedWith(key age.Identity) func([]byte) response {
	return func(challenge []byte) response {
		r, err := age.Decrypt(bytes.NewReader(challenge), key)
		if err != nil {
			return response{V: Version, Error: codeChallenge}
		}
		nonce, _ := io.ReadAll(r)
		defer clear(nonce)
		return response{V: Version, Proof: legacyProofOf(nonce)}
	}
}

// A process that holds the vault's key but proves with the earlier release's label is told nothing: no
// request outside a handover accepts that label, and a handover asks no proof of its successor at all.
func TestClientRefusesTheEarlierProofLabel(t *testing.T) {
	conn, _, rest := fakeServer(t, legacySolvedWith(testKey))
	c := testClient()
	ctx := context.Background()
	req := request{Op: opGet, Credential: "wiki-reader", Role: "token", Scope: testScope()}
	_, err := c.exchange(ctx, conn, c.deadline(ctx), req)
	_ = conn.Close()
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("exchange with a proof under the earlier label error = %v, want ErrRefused", err)
	}
	if data := <-rest; len(data) != 0 {
		t.Fatalf("the client sent %q after a proof under the earlier label", data)
	}
}

// Neither label stands in for the other: a proof under one never checks under the other, so a client of the
// earlier release refuses this release's vault process as well.
func TestProofLabelsDoNotStandInForEachOther(t *testing.T) {
	nonce := bytes.Repeat([]byte{7}, nonceSize)
	current, legacy := proofOf(nonce), legacyProofOf(nonce)
	if bytes.Equal(current, legacy) {
		t.Fatal("both labels derive the same proof")
	}
	if checkProof(nonce, legacy) {
		t.Error("checkProof() accepts the proof under the earlier label")
	}
	if !checkProof(nonce, current) {
		t.Error("checkProof() refuses the proof under this release's label")
	}
	proof, ok := solveChallenge(testKey, mustChallenge(t, nonce))
	if !ok || bytes.Equal(proof, legacy) || !bytes.Equal(proof, current) {
		t.Errorf("solveChallenge() = %x, %v, want the proof under this release's label only", proof, ok)
	}
}

// mustChallenge encrypts nonce to the test vault's recipient, as a client's challenge carries it.
func mustChallenge(t *testing.T, nonce []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, testKey.Recipient())
	if err == nil {
		_, err = w.Write(nonce)
	}
	if err == nil {
		err = w.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
