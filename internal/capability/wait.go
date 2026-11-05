package capability

import (
	"context"
	"time"
)

// WaitOptions tells a waiting operation how long it may block and how often it checks. A context without
// them is the broker's: the operation then answers within the invoke limit and says that it is still waiting.
type WaitOptions struct {
	// Limit is how long the operation may wait before it ends with a timeout of its own.
	Limit time.Duration
	// Interval is the pause between two checks; zero keeps the operation's own default.
	Interval time.Duration
}

type waitKey struct{}

// WithWait returns a copy of ctx that carries the wait options of a command that waits for the end of an
// operation instead of calling it again and again.
func WithWait(ctx context.Context, options WaitOptions) context.Context {
	return context.WithValue(ctx, waitKey{}, options)
}

// WaitFrom returns the wait options ctx carries and whether it carries any.
func WaitFrom(ctx context.Context) (WaitOptions, bool) {
	if ctx == nil {
		return WaitOptions{}, false
	}
	options, ok := ctx.Value(waitKey{}).(WaitOptions)
	return options, ok
}
