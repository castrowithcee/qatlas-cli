// Package idempotency makes the client token of a create operation. The tool, not the caller, generates it,
// so a caller never has to; a caller that wants a repetition to be the same request passes client_token.
package idempotency

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"regexp"
)

// Members of a model that carry the idempotencyToken trait in the services this broker binds.
var paramNames = map[string]bool{"ClientToken": true, "ClientRequestToken": true, "clientToken": true}

// IsParam reports whether a wire member name is one of the known idempotency token members.
func IsParam(name string) bool { return paramNames[name] }

// ErrInvalid reports a client_token the tool refuses to send.
var ErrInvalid = errors.New("client_token must be 1 to 128 printable ASCII characters without spaces")

var form = regexp.MustCompile(`^[\x21-\x7e]{1,128}$`)

// Resolve returns the token to send. A provided token is used unchanged once it is well formed; otherwise a
// random version 4 UUID is made from rnd, which is crypto/rand when nil. A failing random source is an
// error: a predictable token would defeat the purpose.
func Resolve(provided string, rnd io.Reader) (token string, generated bool, err error) {
	if provided != "" {
		if !form.MatchString(provided) {
			return "", false, ErrInvalid
		}
		return provided, false, nil
	}
	if rnd == nil {
		rnd = rand.Reader
	}
	var b [16]byte
	if _, err := io.ReadFull(rnd, b[:]); err != nil {
		return "", false, errors.New("the idempotency token could not be generated")
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), true, nil
}
