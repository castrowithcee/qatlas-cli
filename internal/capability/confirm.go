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

// replacedKey carries the callback that hears of a local file replaced by a confirmed request.
type replacedKey struct{}

// WithReplacedReporter returns a copy of ctx whose ReportReplaced calls report. Only the core sets it, to
// record the replacement without learning which file it was.
func WithReplacedReporter(ctx context.Context, report func()) context.Context {
	return context.WithValue(ctx, replacedKey{}, report)
}

// ReportReplaced tells the core that an existing local file was replaced under the request's confirmation.
// It carries no path. Without a reporter in ctx, nil included, it does nothing.
func ReportReplaced(ctx context.Context) {
	if ctx == nil {
		return
	}
	if report, _ := ctx.Value(replacedKey{}).(func()); report != nil {
		report()
	}
}
