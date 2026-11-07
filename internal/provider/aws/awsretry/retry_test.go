package awsretry

import (
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
)

func limited() *provider.Error { return &provider.Error{Class: provider.ClassRateLimited} }
func server() *provider.Error {
	return &provider.Error{Class: provider.ClassProviderError, Original: &provider.Original{Status: 503}}
}

func TestDecide(t *testing.T) {
	p := Default
	p.Jitter = func() float64 { return 0 }
	tests := []struct {
		name    string
		idem    capability.Idempotency
		attempt int
		failure *provider.Error
		after   time.Duration
		again   bool
		hold    time.Duration
	}{
		{"rate limit with Retry-After", capability.IdempotencySafe, 0, limited(), 3 * time.Second, true, 3 * time.Second},
		{"rate limit without Retry-After", capability.IdempotencySafe, 0, limited(), 0, false, 0},
		{"Retry-After beyond the wait bound is reported", capability.IdempotencySafe, 0, limited(), 11 * time.Second, false, 11 * time.Second},
		{"hold is capped", capability.IdempotencySafe, 0, limited(), time.Hour, false, time.Minute},
		{"bound reached", capability.IdempotencySafe, p.MaxRetries, limited(), time.Second, false, time.Second},
		{"server failure", capability.IdempotencySafe, 0, server(), 0, true, 500 * time.Millisecond},
		{"server failure backs off", capability.IdempotencySafe, 1, server(), 0, true, time.Second},
		{"idempotent is not repeated", capability.IdempotencyIdempotent, 0, server(), 0, false, 0},
		{"non-idempotent is not repeated", capability.IdempotencyNonIdempotent, 0, limited(), 2 * time.Second, false, 2 * time.Second},
		{"unknown is not repeated", capability.IdempotencyUnknown, 0, server(), 0, false, 0},
		{"client error", capability.IdempotencySafe, 0, &provider.Error{Class: provider.ClassPermission}, 0, false, 0},
	}
	for _, test := range tests {
		got := p.Decide(test.idem, test.attempt, test.failure, test.after)
		if got.Again != test.again || got.Hold != test.hold {
			t.Errorf("%s: got %+v, want again %v hold %v", test.name, got, test.again, test.hold)
		}
	}
}

func TestJitterStaysWithinItsShare(t *testing.T) {
	p := Default
	for _, draw := range []float64{-1, 0, 0.5, 0.999, 7} {
		p.Jitter = func() float64 { return draw }
		got := p.Decide(capability.IdempotencySafe, 0, limited(), 8*time.Second)
		if got.Hold < 8*time.Second || got.Hold > 10*time.Second {
			t.Errorf("draw %v: hold %v outside [8s, 10s]", draw, got.Hold)
		}
	}
	p.Jitter = nil
	for range 50 {
		if h := p.Decide(capability.IdempotencySafe, 0, limited(), 8*time.Second).Hold; h < 8*time.Second || h > 10*time.Second {
			t.Fatalf("default jitter: hold %v", h)
		}
	}
}
