package capability

import "context"

// confirmedKey marks a context whose request carried its confirmation.
type confirmedKey struct{}

// WithConfirmed returns a copy of ctx that says the request confirmed its effects, such as replacing a local
// file that already exists. Only the core sets it, from the confirmation of the request it handles.
func WithConfirmed(ctx context.Context) context.Context {
	return context.WithValue(ctx, confirmedKey{}, true)
}

// Confirmed reports whether ctx carries the confirmation of its request. A context without one, nil
// included, is unconfirmed.
func Confirmed(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	confirmed, _ := ctx.Value(confirmedKey{}).(bool)
	return confirmed
}
