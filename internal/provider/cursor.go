package provider

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
)

// cursorBindingLength is the number of checksum bytes in front of the provider part of a bound cursor.
const cursorBindingLength = 12

// CursorBinding binds a cursor to the target and the normalized arguments of the request that produced it.
// Parts that do not change the sequence of a list, such as the page size, stay out, so a continuation stays
// exact whatever batch size the next request asks for.
func CursorBinding(parts ...any) []byte {
	encoded, _ := json.Marshal(parts)
	sum := sha256.Sum256(encoded)
	return sum[:cursorBindingLength]
}

// EncodeCursor wraps the cursor a provider reported into the opaque cursor handed to the caller. Its prefix
// is a checksum over the binding and the provider part, so both are covered.
func EncodeCursor(binding []byte, inner string) string {
	sum := sha256.Sum256(append(append([]byte(nil), binding...), inner...))
	return base64.RawURLEncoding.EncodeToString(append(sum[:cursorBindingLength], inner...))
}

// DecodeCursor returns the provider cursor a bound cursor continues after. A cursor that is malformed,
// altered, cut short, longer than max, or issued for another binding is refused with ok false.
func DecodeCursor(binding []byte, cursor string, max int) (inner string, ok bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if len(cursor) > max || err != nil || len(decoded) <= cursorBindingLength ||
		EncodeCursor(binding, string(decoded[cursorBindingLength:])) != cursor {
		return "", false
	}
	return string(decoded[cursorBindingLength:]), true
}
