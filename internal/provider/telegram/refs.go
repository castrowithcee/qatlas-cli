package telegram

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// A reference carries an identifier that Telegram scopes to no chat (a file, a callback query, a join request)
// together with the target it came from. It is signed, not encrypted: it stops a model from forging or
// redirecting an identifier, and Qatlas stores nothing.
//
//	ref = base64url(kind | len(binding) | binding | len(id) | id | tag)
//	tag = HMAC-SHA256(K, kind | len(binding) | binding | len(id) | id)
//
// K is derived from the bot token, so rotating the token invalidates every earlier reference. Lengths are
// two-byte big-endian prefixes, so no byte can move from one field to the next.

type refKind byte

const (
	refFile refKind = iota + 1
	refCallback
	refJoinRequest
	refInlineQuery
)

const (
	refKeyLabel   = "qatlas telegram reference key v1"
	refTagBytes   = sha256.Size
	maxRefBinding = 9 + 128 // business/<id>
	maxRefID      = 1024
	maxRefText    = 2048
)

// parsedRef is a reference that passed the format, kind, and binding checks but whose tag is still unchecked.
type parsedRef struct {
	kind    refKind
	binding string
	id      string
	signed  []byte
	tag     []byte
}

func (k refKind) valid() bool { return k >= refFile && k <= refInlineQuery }

func refKey(token string) []byte {
	// HKDF-SHA256 with a 32-byte output cannot fail.
	key, err := hkdf.Key(sha256.New, []byte(token), nil, refKeyLabel, sha256.Size)
	if err != nil {
		panic(err)
	}
	return key
}

func refSigned(kind refKind, binding, id string) []byte {
	out := make([]byte, 0, 5+len(binding)+len(id))
	out = append(out, byte(kind))
	out = binary.BigEndian.AppendUint16(out, uint16(len(binding)))
	out = append(out, binding...)
	out = binary.BigEndian.AppendUint16(out, uint16(len(id)))
	return append(out, id...)
}

func refTag(token string, signed []byte) []byte {
	mac := hmac.New(sha256.New, refKey(token))
	mac.Write(signed)
	return mac.Sum(nil)
}

// signRef returns the reference of one identifier, or "" when the identifier cannot be represented.
func signRef(token string, kind refKind, binding, id string) string {
	if !kind.valid() || binding == "" || len(binding) > maxRefBinding || id == "" || len(id) > maxRefID {
		return ""
	}
	signed := refSigned(kind, binding, id)
	return base64.RawURLEncoding.EncodeToString(append(signed, refTag(token, signed)...))
}

// parseRef is the first check stage. It needs no credential: it verifies the format, the expected kind, and
// that the binding names a target of this connection.
func parseRef(resolved *config.Resolved, want refKind, raw string) (parsedRef, error) {
	const op = "check reference"
	if raw == "" || len(raw) > maxRefText {
		return parsedRef{}, providerError(op, "the reference is malformed")
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(data) < 1+2+2+refTagBytes {
		return parsedRef{}, providerError(op, "the reference is malformed")
	}
	p := parsedRef{kind: refKind(data[0])}
	rest := data[1:]
	field := func() (string, bool) {
		if len(rest) < 2 {
			return "", false
		}
		n := int(binary.BigEndian.Uint16(rest))
		if len(rest) < 2+n {
			return "", false
		}
		value := string(rest[2 : 2+n])
		rest = rest[2+n:]
		return value, true
	}
	var ok bool
	if p.binding, ok = field(); !ok {
		return parsedRef{}, providerError(op, "the reference is malformed")
	}
	if p.id, ok = field(); !ok || p.binding == "" || p.id == "" || len(rest) != refTagBytes {
		return parsedRef{}, providerError(op, "the reference is malformed")
	}
	p.tag = rest
	p.signed = data[:len(data)-refTagBytes]
	if !p.kind.valid() {
		return parsedRef{}, providerError(op, "the reference is malformed")
	}
	if p.kind != want {
		return parsedRef{}, providerError(op, "the reference is not of the expected kind")
	}
	set, err := targetsOf(resolved)
	if err != nil {
		return parsedRef{}, providerError(op, "the configured Telegram targets are unusable")
	}
	if !set.binds(p.binding) {
		return parsedRef{}, providerError(op, "the reference does not belong to a target of this connection")
	}
	return p, nil
}

// checkRef is the second stage: it verifies the tag against this client's bot token in constant time and
// returns the identifier. It runs before any request to Telegram.
func (c *Client) checkRef(p parsedRef) (string, error) {
	if !hmac.Equal(p.tag, refTag(c.token, p.signed)) {
		return "", providerError("check reference", "the reference was not issued for this bot token")
	}
	return p.id, nil
}

// binds reports whether binding spells exactly one target of the connection.
func (s targetSet) binds(binding string) bool {
	if binding == botTarget {
		return s.bot
	}
	for _, chat := range s.chats {
		if chat == binding {
			return true
		}
	}
	for _, id := range s.businesses {
		if businessPrefix+id == binding {
			return true
		}
	}
	return false
}
