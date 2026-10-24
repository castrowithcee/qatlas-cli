package vault

import "testing"

// LowerWorkFactorForTests drops the scrypt work factor of vault keys and encrypted exports to a value that
// keeps a test binary fast. It panics outside a test binary, so a mistaken call in a real program fails loudly
// instead of silently weakening a key; testing.Testing is false there and nothing else can change the factor.
func LowerWorkFactorForTests() {
	if !testing.Testing() {
		panic("vault: LowerWorkFactorForTests called outside a test binary")
	}
	scryptLogN = 4
}
