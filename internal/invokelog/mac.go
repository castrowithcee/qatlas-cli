package invokelog

import (
	"bytes"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"

	"filippo.io/age"
)

// keyLabel is the HKDF context the log key is derived with, so the log key is never the vault's key
// itself, nor any key the vault derives for another purpose. Every entry is signed under it.
const keyLabel = "qatlas-connect invocation log v1"

// legacyKeyLabel is the context entries were signed under before keyLabel. It only checks those entries and
// never signs one. Transitional until the next major release.
const legacyKeyLabel = "qatlas invocation log v1"

// currentMACKey is the value of Entry.MACKey that names keyLabel; an entry without it was signed under
// legacyKeyLabel.
const currentMACKey = 2

// macSize is the length of a check value in hex digits: an HMAC-SHA256.
const macSize = 2 * sha256.Size

// macField is how the check value begins in a stored line. It is always the line's last field, so a line
// always ends with macField, the value, and `"}`, and an unsigned line always ends with emptyMACSuffix.
var (
	macField       = []byte(`,"mac":"`)
	emptyMACSuffix = []byte(`,"mac":""}`)
	// currentSuffix is how an unsigned line under keyLabel ends: mac_key is always the field right before
	// mac, so the label a line names is read from its bytes alone, the same way by Check and by Verify.
	currentSuffix = []byte(`,"mac_key":` + strconv.Itoa(currentMACKey) + `,"mac":""}`)
)

// ErrMalformedMAC reports a line whose check value is not one Append could have written.
var ErrMalformedMAC = errors.New("the check value is malformed")

// Key is the log key, derived from the vault's own key; see DeriveKey. It signs an entry and checks the
// check value of a stored line, and it never hands out its bytes. The zero value and a nil Key sign
// nothing. A Key is safe for concurrent use until Clear.
type Key struct {
	key []byte
	// legacy is the key under legacyKeyLabel, which only checks.
	legacy []byte
}

// DeriveKey returns the log key of the vault whose private key is id: HKDF-SHA256 with the age text of id
// as the input key material, no salt, and keyLabel as the context, 32 bytes long, and beside it the key
// under legacyKeyLabel, derived the same way, for checking older entries. The same vault key always yields
// the same log key, in the vault process and in a process that unlocked the vault itself alike; another
// vault key, a new one after 'vault encrypt' included, yields another.
func DeriveKey(id *age.X25519Identity) (*Key, error) {
	if id == nil {
		return nil, errors.New("no vault key to derive the log key from")
	}
	secret := []byte(id.String())
	defer clear(secret)
	key, err := hkdf.Key(sha256.New, secret, nil, keyLabel, sha256.Size)
	if err != nil {
		return nil, errors.New("cannot derive the log key")
	}
	legacy, err := hkdf.Key(sha256.New, secret, nil, legacyKeyLabel, sha256.Size)
	if err != nil {
		clear(key)
		return nil, errors.New("cannot derive the log key")
	}
	return &Key{key: key, legacy: legacy}, nil
}

// Clear overwrites the key, after which it signs nothing and checks nothing.
func (k *Key) Clear() {
	if k != nil {
		clear(k.key)
		clear(k.legacy)
		k.key, k.legacy = nil, nil
	}
}

func (k *Key) usable() bool { return k != nil && len(k.key) > 0 && len(k.legacy) > 0 }

// mac is the check value of an unsigned line: the lowercase hex HMAC-SHA256, keyed with the log key, of the
// line's exact bytes as encoded with an empty mac field, that is, ending in `,"mac":""}`, without a
// trailing newline. The key is the one under keyLabel for a line that names it (see currentLabel) and the
// one under legacyKeyLabel for any other. The prev_hash of the next entry is still the SHA-256 of the whole
// stored line, the check value included, so a chain of signed and unsigned entries hashes the same way.
func (k *Key) mac(unsigned []byte) []byte {
	key := k.legacy
	if currentLabel(unsigned) {
		key = k.key
	}
	h := hmac.New(sha256.New, key)
	h.Write(unsigned)
	sum := h.Sum(nil)
	out := make([]byte, hex.EncodedLen(len(sum)))
	hex.Encode(out, sum)
	return out
}

// sign returns unsigned with its empty mac field filled with its check value. unsigned is a line as
// json.Marshal encoded an Entry whose MAC is empty and whose MACKey is currentMACKey, so nothing is ever
// signed under legacyKeyLabel.
func (k *Key) sign(unsigned []byte) ([]byte, error) {
	if !currentLabel(unsigned) {
		return nil, errors.New("an entry to sign does not name the current log key before its empty check value")
	}
	mac := k.mac(unsigned)
	prefix := unsigned[:len(unsigned)-len(`""}`)]
	signed := make([]byte, 0, len(prefix)+len(mac)+3)
	signed = append(signed, prefix...)
	signed = append(signed, '"')
	signed = append(signed, mac...)
	return append(signed, '"', '}'), nil
}

// Check reports, for each of lines, stored lines with a non-empty mac, whether its check value is the one
// the log key gives it, comparing in constant time. A line whose check value is malformed does not match
// either.
func (k *Key) Check(lines [][]byte) ([]bool, error) {
	if !k.usable() {
		return nil, errors.New("the log key is not available")
	}
	valid := make([]bool, len(lines))
	for i, line := range lines {
		if unsigned, mac, err := unsignedOf(line); err == nil {
			valid[i] = hmac.Equal(k.mac(unsigned), mac)
		}
	}
	return valid, nil
}

// unsignedOf splits a stored, signed line into the bytes its check value was computed over and the check
// value itself. It fails with ErrMalformedMAC unless the line ends with a mac field of exactly macSize
// lowercase hex digits, which is the only form sign writes.
func unsignedOf(line []byte) (unsigned, mac []byte, err error) {
	at := bytes.LastIndex(line, macField)
	if at < 0 {
		return nil, nil, ErrMalformedMAC
	}
	rest := line[at+len(macField):]
	if len(rest) != macSize+2 || rest[macSize] != '"' || rest[macSize+1] != '}' {
		return nil, nil, ErrMalformedMAC
	}
	mac = rest[:macSize]
	for _, c := range mac {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return nil, nil, ErrMalformedMAC
		}
	}
	unsigned = make([]byte, 0, at+len(emptyMACSuffix))
	unsigned = append(unsigned, line[:at]...)
	return append(unsigned, emptyMACSuffix...), mac, nil
}

// currentLabel reports whether unsigned, a line as unsignedOf or json.Marshal gives it, names keyLabel.
func currentLabel(unsigned []byte) bool { return bytes.HasSuffix(unsigned, currentSuffix) }

// Checker checks the check values of stored lines and answers one result per line, in order. A *Key is
// one; the vault process offers another that answers without ever handing out the key. An error means the
// check could not be made at all, which Verify never reports as either valid or invalid.
type Checker interface {
	Check(lines [][]byte) ([]bool, error)
}
