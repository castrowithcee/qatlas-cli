package telegram

import (
	"encoding/base64"
	"encoding/binary"
	"os"
	"strings"
	"testing"
)

const legacyRefToken = "123456:legacy-fixture-token"

// A reference issued before the key label was renamed still checks out; a new one is signed with the new key
// and a tag under neither key is refused.
func TestLegacyReferenceStaysResolvable(t *testing.T) {
	raw, err := os.ReadFile("testdata/legacy-ref.txt")
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.TrimSpace(string(raw))
	data := func(ref string) parsedRef {
		t.Helper()
		p, ok := splitRefForTest(ref)
		if !ok {
			t.Fatalf("malformed reference %q", ref)
		}
		return p
	}
	c := &Client{token: legacyRefToken}

	id, err := c.checkRef(data(legacy))
	if err != nil || id != "canary-file-id" {
		t.Fatalf("checkRef(legacy) = %q, %v", id, err)
	}

	fresh := signRef(legacyRefToken, refFile, "-1001", "canary-file-id")
	if fresh == legacy {
		t.Fatal("a new reference is signed with the legacy key")
	}
	if id, err := c.checkRef(data(fresh)); err != nil || id != "canary-file-id" {
		t.Fatalf("checkRef(new) = %q, %v", id, err)
	}
	// Only the new key signs: the new reference does not verify under the legacy key.
	p := data(fresh)
	if string(refTagWith(legacyRefToken, legacyRefKeyLabel, p.signed)) == string(p.tag) {
		t.Fatal("a new reference carries a legacy tag")
	}

	// Another token, a tampered id and a foreign label are all refused.
	if _, err := (&Client{token: "999:other"}).checkRef(data(legacy)); err == nil {
		t.Error("legacy reference accepted for a foreign token")
	}
	forged := data(legacy)
	forged.signed = refSigned(refFile, "-1001", "other-id")
	if _, err := c.checkRef(forged); err == nil {
		t.Error("tampered legacy reference accepted")
	}
	foreign := data(fresh)
	foreign.tag = refTagWith(legacyRefToken, "qatlas-connect vault settings v1", foreign.signed)
	if _, err := c.checkRef(foreign); err == nil {
		t.Error("reference with a foreign label accepted")
	}
}

// splitRefForTest decodes the signed part and tag of a reference without the target check of parseRef.
func splitRefForTest(ref string) (parsedRef, bool) {
	data, err := decodeRefBytes(ref)
	if err != nil || len(data) <= refTagBytes {
		return parsedRef{}, false
	}
	signed := data[:len(data)-refTagBytes]
	// kind, then the length-prefixed binding and id, as refSigned lays them out.
	n := int(binary.BigEndian.Uint16(signed[1:]))
	rest := signed[3+n:]
	m := int(binary.BigEndian.Uint16(rest))
	return parsedRef{signed: signed, tag: data[len(data)-refTagBytes:], id: string(rest[2 : 2+m])}, true
}

func decodeRefBytes(ref string) ([]byte, error) {
	return base64.RawURLEncoding.Strict().DecodeString(ref)
}
