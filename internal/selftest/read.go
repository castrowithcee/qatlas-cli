package selftest

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// ReadResult is the outcome of ReadUnderWrite.
type ReadResult struct {
	Read Stats
	// Retries counts the waits of the readers' file retry, which stand for reads that hit a replace in
	// progress. The writer's own reads are not counted.
	Retries     int64
	ReadErrors  int64
	Writes      int64
	WriteErrors int64
}

// Values lists the result as measurement values.
func (r ReadResult) Values() []Value {
	return []Value{
		{"read-p50-ms", ms(r.Read.P50)},
		{"read-p95-ms", ms(r.Read.P95)},
		{"retries", float64(r.Retries)},
		{"read-errors", float64(r.ReadErrors)},
		{"writes", float64(r.Writes)},
		{"write-errors", float64(r.WriteErrors)},
	}
}

// ReadUnderWrite measures Load while one writer calls Update in a loop. Each of the readers loads
// iterations times, so the percentiles cover readers*iterations samples; a reader error is counted, not
// fatal. The writer ends before the call returns, also when ctx ends.
func (f *StorageFixture) ReadUnderWrite(ctx context.Context, readers, iterations int) (ReadResult, error) {
	var res ReadResult
	var retries, readErrors, writes, writeErrors atomic.Int64
	observed := f.store.Observed(config.Observer{FileRetry: func(time.Duration) { retries.Add(1) }})

	stop := make(chan struct{})
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			default:
			}
			url := "https://churn-" + strconv.Itoa(i%2) + ".example.invalid"
			err := f.store.Update(func(c *config.Config) error {
				return c.SetService("churn", config.Service{Provider: syntheticProvider, BaseURL: url})
			})
			writes.Add(1)
			if err != nil {
				writeErrors.Add(1)
			}
		}
	}()

	latencies := make([][]time.Duration, readers)
	var wg sync.WaitGroup
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			latencies[r], _ = timeLoop(ctx, iterations, func() error {
				_, err := observed.Load()
				return err
			}, func(error) { readErrors.Add(1) })
		}()
	}
	wg.Wait()
	close(stop)
	writer.Wait()
	if err := ctx.Err(); err != nil {
		return res, err
	}

	var all []time.Duration
	for _, lat := range latencies {
		all = append(all, lat...)
	}
	res.Read = StatsOf(all)
	res.Retries, res.ReadErrors = retries.Load(), readErrors.Load()
	res.Writes, res.WriteErrors = writes.Load(), writeErrors.Load()
	return res, nil
}
