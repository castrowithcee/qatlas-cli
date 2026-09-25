//go:build !linux

package secret

import "context"

// ready has nothing to check before the library is called on this platform; storeTimeout bounds the call.
func ready(context.Context) error { return nil }
