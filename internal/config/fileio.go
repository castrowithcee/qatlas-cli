package config

import (
	"errors"
	"os"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/fileretry"
)

// transientFile reports a file failure a short wait may cure, such as a sharing violation while another
// process reads or replaces the file. sleepFile waits between attempts; nil means time.Sleep. Tests replace
// both.
var (
	transientFile = fileretry.Transient
	sleepFile     func(time.Duration)
)

// Observer receives measurement events of the store it is attached to with Store.Observed. It is passed
// per store, never global, so concurrent stores observe independently; a nil member is ignored. The
// callbacks may run on any goroutine of the caller that uses the store.
type Observer struct {
	// FileRetry is called before each wait of the file retry, with the length of the wait.
	FileRetry func(time.Duration)
	// LockWaited is called with how long taking the cross-process lock blocked, also when it failed.
	LockWaited func(time.Duration)
}

// retryFile runs a file operation that changes nothing when it fails, and repeats it while the failure is
// transient. A missing file is never transient: it is an answer, not a lock.
func retryFile(obs Observer, op func() error) error {
	sleep := sleepFile
	if obs.FileRetry != nil {
		wait := sleepFile
		if wait == nil {
			wait = time.Sleep
		}
		sleep = func(d time.Duration) {
			obs.FileRetry(d)
			wait(d)
		}
	}
	return fileretry.Do(op, func(err error) bool {
		return !errors.Is(err, os.ErrNotExist) && transientFile(err)
	}, sleep)
}

// readFile reads the whole file and closes it before it returns, so a decode that follows holds no handle
// that would block a writer replacing the file.
func readFile(path string, obs Observer) ([]byte, error) {
	var data []byte
	err := retryFile(obs, func() (err error) {
		data, err = os.ReadFile(path)
		return err
	})
	return data, err
}
