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

// retryFile runs a file operation that changes nothing when it fails, and repeats it while the failure is
// transient. A missing file is never transient: it is an answer, not a lock.
func retryFile(op func() error) error {
	return fileretry.Do(op, func(err error) bool {
		return !errors.Is(err, os.ErrNotExist) && transientFile(err)
	}, sleepFile)
}

// readFile reads the whole file and closes it before it returns, so a decode that follows holds no handle
// that would block a writer replacing the file.
func readFile(path string) ([]byte, error) {
	var data []byte
	err := retryFile(func() (err error) {
		data, err = os.ReadFile(path)
		return err
	})
	return data, err
}
