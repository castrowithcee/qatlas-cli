// Package awsretry decides whether a failed AWS call is repeated and how long to wait first. Only an
// operation that is read-only and safe to repeat is ever repeated: a repetition of anything else could act
// twice. The decision is data and arithmetic; the caller sleeps through its rate limiter.
package awsretry

import (
	"math/rand/v2"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
)

// Policy bounds the repetitions of one call.
type Policy struct {
	// MaxRetries is the number of repetitions after the first attempt.
	MaxRetries int
	// MaxWait is the longest Retry-After a repetition waits for; a longer one is reported, not slept.
	MaxWait time.Duration
	// MaxHold is the longest Retry-After the limiter keeps for later requests.
	MaxHold time.Duration
	// ServerBackoff is the pause before the first repetition after a server-side failure; each further one
	// doubles it.
	ServerBackoff time.Duration
	// JitterShare is the largest random addition to a wait, as a share of the wait. A wait is never shorter
	// than the provider asked.
	JitterShare float64
	// Jitter returns a value in [0, 1). Nil uses math/rand/v2, which needs no cryptographic strength.
	Jitter func() float64
}

// Default is the policy of the AWS provider: two repetitions, at most 10 s of Retry-After, a quarter of
// jitter. Two repetitions of at most 12.5 s stay below the 50 s budget of one invoke.
var Default = Policy{
	MaxRetries: 2, MaxWait: 10 * time.Second, MaxHold: time.Minute,
	ServerBackoff: 500 * time.Millisecond, JitterShare: 0.25,
}

// Decision is what to do after a failed attempt.
type Decision struct {
	// Again says to send the call once more.
	Again bool
	// Hold is how long the limiter delays the next request of this connection and service, jitter included
	// when Again is set. It is also set when the call is not repeated, so the next call respects the
	// provider's Retry-After.
	Hold time.Duration
}

// Decide judges attempt (0 for the first) that failed with failure. retryAfter is the Retry-After the
// answer carried. A rate limit is repeated only with a usable Retry-After, since without one nothing says
// when the provider is ready; a server-side failure (status 500 and above) is repeated after a short,
// growing pause. Nothing is repeated unless the operation is IdempotencySafe.
func (p Policy) Decide(idem capability.Idempotency, attempt int, failure *provider.Error, retryAfter time.Duration) Decision {
	hold := min(retryAfter, p.MaxHold)
	var base time.Duration
	again := false
	switch {
	case failure.Class == provider.ClassRateLimited:
		again = retryAfter > 0 && retryAfter <= p.MaxWait
		base = hold
	case failure.Class == provider.ClassProviderError && failure.Original != nil && failure.Original.Status >= 500:
		again = true
		base = max(hold, p.ServerBackoff<<min(attempt, 8))
	}
	if idem != capability.IdempotencySafe || attempt >= p.MaxRetries {
		again = false
	}
	if !again {
		return Decision{Hold: hold}
	}
	return Decision{Again: true, Hold: base + p.jitter(base)}
}

func (p Policy) jitter(base time.Duration) time.Duration {
	draw := p.Jitter
	if draw == nil {
		draw = rand.Float64
	}
	return time.Duration(float64(base) * p.JitterShare * min(max(draw(), 0), 1))
}
