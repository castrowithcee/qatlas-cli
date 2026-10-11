package fileretry

import (
	"errors"
	"testing"
	"time"
)

var errBusy = errors.New("busy")

func isBusy(err error) bool { return errors.Is(err, errBusy) }

func TestDoRetriesTransientError(t *testing.T) {
	var slept []time.Duration
	calls := 0
	err := Do(func() error {
		calls++
		if calls <= 3 {
			return errBusy
		}
		return nil
	}, isBusy, func(d time.Duration) { slept = append(slept, d) })
	if err != nil {
		t.Fatal(err)
	}
	if calls != 4 || len(slept) != 3 || slept[0] >= slept[2] {
		t.Errorf("calls = %d, waits = %v, want four calls and three growing waits", calls, slept)
	}
}

func TestDoGivesUpOnPermanentError(t *testing.T) {
	var slept []time.Duration
	err := Do(func() error { return errBusy }, isBusy, func(d time.Duration) { slept = append(slept, d) })
	if !errors.Is(err, errBusy) {
		t.Fatalf("err = %v, want the transient error", err)
	}
	var total time.Duration
	for _, d := range slept {
		total += d
	}
	if len(slept) != len(Delays) || total != 2775*time.Millisecond {
		t.Errorf("waits = %v (%v)", slept, total)
	}
}

func TestDoDoesNotRetryOtherErrors(t *testing.T) {
	other := errors.New("other")
	calls := 0
	err := Do(func() error { calls++; return other }, isBusy, func(time.Duration) { t.Error("slept") })
	if !errors.Is(err, other) || calls != 1 {
		t.Errorf("err = %v, calls = %d", err, calls)
	}
}

func TestDoWithoutTransientDoesNotRetry(t *testing.T) {
	calls := 0
	_ = Do(func() error { calls++; return errBusy }, nil, nil)
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}
