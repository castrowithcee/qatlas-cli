package config_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/selftest"
)

// Measurements for the storage architecture: reading while another writer replaces the file, and how long
// writer processes wait for the cross-process lock. The measuring code is that of `qatlas selftest
// storage` (internal/selftest); these benchmarks only drive it with b.N. Fixtures are synthetic, generated
// into b.TempDir() (TMPDIR picks the file system), and never touch a real configuration. The benchmarks run
// only with -bench.
//
//	go test ./internal/config -run '^$' -bench 'BenchmarkReadUnderWrite' -benchtime 200x -count 3
//	go test ./internal/config -run '^$' -bench 'BenchmarkUpdateLockWait' -benchtime 5x -count 3
//
// As a prebuilt test executable (Windows has no Go on the measuring machine):
//
//	go test -c -o config.test.exe ./internal/config
//	config.test.exe -test.run '^$' -test.bench BenchmarkReadUnderWrite -test.benchtime 200x -test.count 3

// testWorkerCommand starts this test binary as a lock-wait writer process.
func testWorkerCommand() selftest.WorkerCommand {
	return selftest.WorkerCommand{Program: os.Args[0], Env: []string{config.WorkerModeEnv + "=" + lockWaitMode}}
}

// BenchmarkReadUnderWrite measures Load while one writer calls Update in a loop. Each of the readers loads
// b.N times, so the latency percentiles cover readers*b.N samples; reader errors are counted, not fatal.
// "retries" counts the waits of the file retry, which stand for reads that hit a replace in progress.
func BenchmarkReadUnderWrite(b *testing.B) {
	for _, size := range selftest.Standard().Storage.ReadEntries {
		for _, readers := range []int{1, 4} {
			b.Run(fmt.Sprintf("entries-%d/readers-%d", size, readers), func(b *testing.B) {
				fx, err := selftest.NewStorageFixture(b.TempDir(), size)
				if err != nil {
					b.Fatal(err)
				}
				b.ResetTimer()
				res, err := fx.ReadUnderWrite(context.Background(), readers, b.N)
				b.StopTimer()
				if err != nil {
					b.Fatal(err)
				}
				for _, v := range res.Values() {
					b.ReportMetric(v.Value, v.Name)
				}
			})
		}
	}
}

// BenchmarkUpdateLockWait starts 5 or 20 writer processes that call Update on the same configuration at
// once. Per process it records the time from the lock request until it holds the lock; the metrics are
// taken over all processes of all iterations. "total-ms" is the span until the last writer is done.
func BenchmarkUpdateLockWait(b *testing.B) {
	for _, size := range selftest.Standard().Storage.LockEntries {
		for _, writers := range selftest.Standard().Storage.LockWriters {
			b.Run(fmt.Sprintf("entries-%d/writers-%d", size, writers), func(b *testing.B) {
				fx, err := selftest.NewStorageFixture(b.TempDir(), size)
				if err != nil {
					b.Fatal(err)
				}
				var waits []selftest.LockWaitRound
				b.StopTimer()
				for i := 0; i < b.N; i++ {
					// Every round starts from the same fixture, so the file does not grow across rounds.
					if err := fx.Reset(); err != nil {
						b.Fatal(err)
					}
					round, err := selftest.RunLockWaitRound(context.Background(), testWorkerCommand(), fx.Path(), writers,
						func(running bool) {
							if running {
								b.StartTimer()
							} else {
								b.StopTimer()
							}
						})
					if err != nil {
						b.Fatal(err)
					}
					waits = append(waits, round)
				}
				for _, v := range selftest.LockWaitValues(waits) {
					b.ReportMetric(v.Value, v.Name)
				}
			})
		}
	}
}

// TestStorageBenchmarksSmall keeps the fixture generator and the worker path of the lock-wait benchmark
// honest at a size that costs next to nothing.
func TestStorageBenchmarksSmall(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns subprocesses")
	}
	fx, err := selftest.NewStorageFixture(t.TempDir(), 30)
	if err != nil {
		t.Fatal(err)
	}
	const n = 3
	round, err := selftest.RunLockWaitRound(context.Background(), testWorkerCommand(), fx.Path(), n, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(round.Waits) != n || round.Total <= 0 {
		t.Fatalf("round = %+v", round)
	}
}
