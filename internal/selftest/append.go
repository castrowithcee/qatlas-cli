package selftest

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
)

// AppendFixture is an invoke log with many synthetic day files, the newest dated like the logger's clock.
type AppendFixture struct {
	logger *invokelog.Logger
}

// NewAppendFixture seeds days day files below dir, which must exist. With emptyNewest the newest file is
// empty, so the predecessor lookup has to walk back one file.
func NewAppendFixture(dir string, days int, emptyNewest bool) (*AppendFixture, error) {
	now := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	vault := filepath.Join(dir, "vault")
	if err := invokelog.SeedDayFiles(vault, days, emptyNewest, now); err != nil {
		return nil, err
	}
	// Retention far beyond the oldest file, so no Append deletes anything.
	logger := invokelog.New(vault, 100000).WithClock(func() time.Time { return now })
	return &AppendFixture{logger: logger}, nil
}

// Step appends one entry; it is the unit of the measurement.
func (f *AppendFixture) Step() error {
	return f.logger.Append(invokelog.Fields{Path: "cli", Operation: "selftest.append", Effect: "read", Result: "success"})
}

// Measure runs Step iterations times and returns the latency percentiles. An Append error ends it.
func (f *AppendFixture) Measure(ctx context.Context, iterations int) (Stats, error) {
	samples, err := timeLoop(ctx, iterations, f.Step, nil)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Stats{}, ctxErr
	}
	if err != nil {
		return Stats{}, fmt.Errorf("append to the log failed: %w", err)
	}
	return StatsOf(samples), nil
}
