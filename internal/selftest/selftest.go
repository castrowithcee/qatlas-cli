// Package selftest measures the local storage of an installation from the release binary, without a Go
// toolchain: reads while a writer replaces the configuration, the lock wait of several writer processes, and
// appends to the invoke log with many day files. It has no surface of its own; a command calls Run and
// renders the Report.
//
// Everything happens on synthetic fixtures below one fresh directory under a base directory the caller
// names. The package never resolves QATLAS_CONFIG, QATLAS_CLI_HOME, or the home directory, and never reads
// or writes a real configuration, vault, or log.
package selftest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/fileretry"
)

// Value is one named number of a measurement. The unit is part of the name, such as "read-p95-ms".
type Value struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

// Measurement is the outcome of one case of a scenario, such as "entries-1000/readers-4" of the group
// "read-under-write". Values keep their order.
type Measurement struct {
	Group  string  `json:"group"`
	Case   string  `json:"case"`
	Values []Value `json:"values"`
}

// Report is the machine-readable result of one run.
type Report struct {
	Scenario string `json:"scenario"`
	Profile  string `json:"profile"`
	// Version is the program version the caller passed; empty when it passed none.
	Version string `json:"version,omitempty"`
	GOOS    string `json:"goos"`
	GOARCH  string `json:"goarch"`
	CPUs    int    `json:"cpus"`
	// TempDir is the scratch directory of the run. It is removed when Run returns.
	TempDir      string        `json:"temp_dir"`
	ElapsedMs    float64       `json:"elapsed_ms"`
	Measurements []Measurement `json:"measurements"`
}

// Options configures Run.
type Options struct {
	// Scenario is a name from Scenarios.
	Scenario string
	Profile  Profile
	// BaseDir is the directory the scratch directory is created in; empty means os.TempDir().
	BaseDir string
	// Version is copied into the Report.
	Version string
	// Worker starts the writer processes of the lock-wait measurement.
	Worker WorkerCommand
	// Progress, if set, receives one short line per step.
	Progress func(string)
}

// Env is what a scenario gets: its scratch directory and the run's settings.
type Env struct {
	Dir      string
	Profile  Profile
	Worker   WorkerCommand
	progress func(string)
}

// Progressf reports one line of progress.
func (e *Env) Progressf(format string, args ...any) {
	if e.progress != nil {
		e.progress(fmt.Sprintf(format, args...))
	}
}

// Scenario is one named group of measurements.
type Scenario struct {
	Name    string
	Summary string
	Run     func(ctx context.Context, env *Env) ([]Measurement, error)
}

// scenarios is the register. A new scenario is added here.
var scenarios = []Scenario{storageScenario}

// Scenarios returns the registered scenarios in name order.
func Scenarios() []Scenario {
	out := append([]Scenario(nil), scenarios...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Run measures one scenario in a fresh scratch directory, which it removes before it returns, also after
// an error or when ctx ends. Worker processes it started are ended and waited for before the removal.
func Run(ctx context.Context, opts Options) (report *Report, err error) {
	var scenario *Scenario
	for i := range scenarios {
		if scenarios[i].Name == opts.Scenario {
			scenario = &scenarios[i]
		}
	}
	if scenario == nil {
		return nil, fmt.Errorf("unknown selftest scenario %q", opts.Scenario)
	}
	if err := opts.Profile.validate(); err != nil {
		return nil, err
	}
	base := opts.BaseDir
	if base == "" {
		base = os.TempDir()
	}
	base, err = filepath.Abs(base)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve the base directory: %w", err)
	}
	dir, err := os.MkdirTemp(base, "qatlas-selftest-")
	if err != nil {
		return nil, fmt.Errorf("cannot create the scratch directory: %w", err)
	}
	defer func() {
		// On Windows a process that just ended may hold a file for a moment, so a sharing violation is retried.
		rmErr := fileretry.Do(func() error { return os.RemoveAll(dir) }, fileretry.Transient, nil)
		if rmErr != nil {
			err = errors.Join(err, fmt.Errorf("cannot remove the scratch directory %s: %w", dir, rmErr))
		}
	}()

	env := &Env{Dir: dir, Profile: opts.Profile, Worker: opts.Worker, progress: opts.Progress}
	start := time.Now()
	measurements, err := scenario.Run(ctx, env)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Report{
		Scenario: scenario.Name, Profile: opts.Profile.Name, Version: opts.Version,
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, CPUs: runtime.NumCPU(),
		TempDir: dir, ElapsedMs: ms(time.Since(start)), Measurements: measurements,
	}, nil
}
