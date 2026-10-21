package config_test

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/selftest"
)

// lockWaitMode makes the test binary a writer process of the selftest lock-wait measurement.
const lockWaitMode = "lockwait"

// TestMain turns the test binary into a worker process when the mode variable is set; the transaction
// tests and the lock-wait benchmark use it to run real transactions from freshly started processes.
func TestMain(m *testing.M) {
	mode := os.Getenv(config.WorkerModeEnv)
	if mode == "" {
		os.Exit(m.Run())
	}
	if mode == lockWaitMode {
		os.Exit(selftest.RunWorker(os.Stdin, os.Stdout, os.Stderr))
	}
	store := config.NewStore(os.Getenv(config.WorkerPathEnv), config.TestProviders)
	name := os.Getenv(config.WorkerNameEnv)
	add := func(cfg *config.Config) error {
		return cfg.SetService(name, config.Service{Provider: "bookstack", BaseURL: "https://" + name + ".example.invalid"})
	}
	var err error
	switch mode {
	case "update":
		err = store.Update(add)
	case "cas":
		// Retry on conflict like a caller of the optimistic API would.
		for {
			var cfg *config.Config
			var rev config.Revision
			cfg, rev, err = store.LoadVersioned()
			if err != nil {
				break
			}
			if err = add(cfg); err != nil {
				break
			}
			if err = store.SaveIfUnchanged(cfg, rev); !errors.Is(err, config.ErrConflict) {
				break
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
