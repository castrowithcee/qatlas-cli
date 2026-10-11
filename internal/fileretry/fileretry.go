// Package fileretry repeats a file operation that fails only because another process holds the file for a
// moment.
package fileretry

import "time"

// Delays are the waits between the attempts of one operation, 2.775 s in all at most.
var Delays = []time.Duration{25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond,
	200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond, 1200 * time.Millisecond}

// Do runs op, and repeats an op that failed with a transient error after a short, growing wait, for a
// bounded time. op must change nothing when it fails, so repeating it is safe; any other error is returned
// at once. A nil transient means no failure is retried; a nil sleep means time.Sleep.
func Do(op func() error, transient func(error) bool, sleep func(time.Duration)) error {
	err := op()
	for _, delay := range Delays {
		if err == nil || transient == nil || !transient(err) {
			return err
		}
		if sleep != nil {
			sleep(delay)
		} else {
			time.Sleep(delay)
		}
		err = op()
	}
	return err
}
