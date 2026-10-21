package selftest

import (
	"context"
	"math"
	"sort"
	"time"
)

// Stats summarizes latency samples.
type Stats struct {
	Samples       int
	P50, P95, Max time.Duration
}

// StatsOf computes nearest-rank percentiles of samples; it does not modify them.
func StatsOf(samples []time.Duration) Stats {
	if len(samples) == 0 {
		return Stats{}
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := func(p float64) time.Duration {
		i := int(math.Ceil(p*float64(len(sorted)))) - 1
		return sorted[max(i, 0)]
	}
	return Stats{Samples: len(sorted), P50: rank(0.50), P95: rank(0.95), Max: sorted[len(sorted)-1]}
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// timeLoop runs step n times and returns how long each call took. It stops when ctx ends. An error from step
// ends the loop too, unless onErr is set: then it is passed there and the loop goes on.
func timeLoop(ctx context.Context, n int, step func() error, onErr func(error)) ([]time.Duration, error) {
	samples := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		if err := ctx.Err(); err != nil {
			return samples, err
		}
		start := time.Now()
		err := step()
		samples = append(samples, time.Since(start))
		if err != nil {
			if onErr == nil {
				return samples, err
			}
			onErr(err)
		}
	}
	return samples, nil
}
