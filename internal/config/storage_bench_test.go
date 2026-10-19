package config

import (
	"bufio"
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Measurements for the storage architecture: reading while another writer replaces the file, and how long
// writer processes wait for the cross-process lock. Fixtures are generated into b.TempDir() (TMPDIR picks
// the file system) and never touch a real configuration. The benchmarks run only with -bench.
//
//	go test ./internal/config -run '^$' -bench 'BenchmarkReadUnderWrite' -benchtime 200x -count 3
//	go test ./internal/config -run '^$' -bench 'BenchmarkUpdateLockWait' -benchtime 5x -count 3
//
// As a prebuilt test executable (Windows has no Go on the measuring machine):
//
//	go test -c -o config.test.exe ./internal/config
//	config.test.exe -test.run '^$' -test.bench BenchmarkReadUnderWrite -test.benchtime 200x -test.count 3

// storageSizes are the total entry counts (services, credentials, and connections together, one third
// each) of the fixtures, the probe points of the load profiles.
var storageSizes = []int{1000, 5000, 10000}

// storageYAML renders a balanced configuration with total entries of the bookstack provider.
func storageYAML(total int) []byte {
	third := total / 3
	var b strings.Builder
	b.WriteString("version: 1\nservices:\n")
	for i := 0; i < third; i++ {
		fmt.Fprintf(&b, "  svc-%05d:\n    provider: bookstack\n    base_url: https://wiki-%d.example.invalid\n", i, i)
	}
	b.WriteString("credentials:\n")
	for i := 0; i < third; i++ {
		fmt.Fprintf(&b, "  cred-%05d:\n    provider: bookstack\n    type: env\n    values:\n"+
			"      token-id: SCALE_CRED_%05d_TOKEN_ID\n      token-secret: SCALE_CRED_%05d_TOKEN_SECRET\n", i, i, i)
	}
	b.WriteString("connections:\n")
	for i := 0; i < total-2*third; i++ {
		fmt.Fprintf(&b, "  conn-%05d:\n    service: svc-%05d\n    credential: cred-%05d\n"+
			"    description: synthetic route %d\n    permissions: [read]\n", i, i%third, i%third, i)
	}
	b.WriteString("defaults: {}\n")
	return []byte(b.String())
}

// storageFixture returns the bytes of a store-canonical configuration of the given size: the generated
// document goes through the store once, so the first measured save does not take the hand-edit backup path.
func storageFixture(tb testing.TB, total int) []byte {
	tb.Helper()
	cfg, err := Decode(bytes.NewReader(storageYAML(total)), testProviders)
	if err != nil {
		tb.Fatalf("fixture does not load: %v", err)
	}
	path := filepath.Join(tb.TempDir(), "canonical.yaml")
	if err := NewStore(path, testProviders).Save(cfg); err != nil {
		tb.Fatalf("fixture does not save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	return data
}

// isolateStorageEnv keeps anything that forgets its path away from a real configuration.
func isolateStorageEnv(tb testing.TB) {
	tb.Helper()
	dir := tb.TempDir()
	tb.Setenv("HOME", dir)
	tb.Setenv("USERPROFILE", dir)
	tb.Setenv("QATLAS_CLI_HOME", dir)
	tb.Setenv("QATLAS_CONFIG", "")
}

func writeStorageFixture(tb testing.TB, path string, data []byte) {
	tb.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		tb.Fatal(err)
	}
}

// percentile returns the p quantile (0..1) of sorted values by nearest rank.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(p*float64(len(sorted)))) - 1
	if i < 0 {
		i = 0
	}
	return sorted[i]
}

func sortDurations(d []time.Duration) { sort.Slice(d, func(i, j int) bool { return d[i] < d[j] }) }

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// BenchmarkReadUnderWrite measures Load while one writer calls Update in a loop. Each of the readers loads
// b.N times, so the latency percentiles cover readers*b.N samples; reader errors are counted, not fatal.
// "retries" counts the waits of the file retry, which stand for reads that hit a replace in progress.
func BenchmarkReadUnderWrite(b *testing.B) {
	isolateStorageEnv(b)
	for _, size := range storageSizes {
		data := storageFixture(b, size)
		for _, readers := range []int{1, 4} {
			b.Run(fmt.Sprintf("entries-%d/readers-%d", size, readers), func(b *testing.B) {
				path := filepath.Join(b.TempDir(), "qatlas", "config.yaml")
				writeStorageFixture(b, path, data)
				store := NewStore(path, testProviders)

				var retries atomic.Int64
				oldSleep := sleepFile
				b.Cleanup(func() { sleepFile = oldSleep })
				sleepFile = func(d time.Duration) {
					retries.Add(1)
					time.Sleep(d)
				}

				var writes, writeErrors atomic.Int64
				stop := make(chan struct{})
				var writer sync.WaitGroup
				writer.Add(1)
				go func() {
					defer writer.Done()
					for i := 0; ; i++ {
						select {
						case <-stop:
							return
						default:
						}
						url := "https://churn-" + strconv.Itoa(i%2) + ".example.invalid"
						err := store.Update(func(c *Config) error {
							return c.SetService("churn", Service{Provider: "bookstack", BaseURL: url})
						})
						writes.Add(1)
						if err != nil {
							writeErrors.Add(1)
						}
					}
				}()

				latencies := make([][]time.Duration, readers)
				var readErrors atomic.Int64
				var wg sync.WaitGroup
				b.ResetTimer()
				for r := 0; r < readers; r++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						lat := make([]time.Duration, 0, b.N)
						for i := 0; i < b.N; i++ {
							start := time.Now()
							_, err := store.Load()
							lat = append(lat, time.Since(start))
							if err != nil {
								readErrors.Add(1)
							}
						}
						latencies[r] = lat
					}()
				}
				wg.Wait()
				b.StopTimer()
				close(stop)
				writer.Wait()

				var all []time.Duration
				for _, lat := range latencies {
					all = append(all, lat...)
				}
				sortDurations(all)
				b.ReportMetric(ms(percentile(all, 0.50)), "read-p50-ms")
				b.ReportMetric(ms(percentile(all, 0.95)), "read-p95-ms")
				b.ReportMetric(float64(retries.Load()), "retries")
				b.ReportMetric(float64(readErrors.Load()), "read-errors")
				b.ReportMetric(float64(writes.Load()), "writes")
				b.ReportMetric(float64(writeErrors.Load()), "write-errors")
			})
		}
	}
}

const lockWaitMode = "lockwait"

// lockWaitWorker is the body of a writer process of the lock-wait benchmark. It reports "ready", waits for
// a line on stdin so all writers ask for the lock together, runs one Update, and prints the time the lock
// request blocked and the whole Update took, both in nanoseconds.
func lockWaitWorker(store *Store, name string) int {
	var waited time.Duration
	lockWaited = func(d time.Duration) { waited = d }
	fmt.Println("ready")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	start := time.Now()
	err := store.Update(func(cfg *Config) error {
		return cfg.SetService(name, Service{Provider: "bookstack", BaseURL: "https://" + name + ".example.invalid"})
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("%d %d\n", waited.Nanoseconds(), time.Since(start).Nanoseconds())
	return 0
}

// lockWaitRound starts n writer processes on path, releases them together, and returns each one's lock
// wait and the wall time from release until the last one finished. The timer, if any, runs only for that
// last span.
func lockWaitRound(tb testing.TB, path string, n int) ([]time.Duration, time.Duration) {
	tb.Helper()
	type worker struct {
		cmd    *exec.Cmd
		stdin  interface{ Write([]byte) (int, error) }
		out    *bufio.Reader
		stderr bytes.Buffer
	}
	workers := make([]*worker, n)
	for i := range workers {
		w := &worker{cmd: exec.Command(os.Args[0])}
		w.cmd.Env = append(os.Environ(), workerModeEnv+"="+lockWaitMode, workerPathEnv+"="+path,
			workerNameEnv+"="+fmt.Sprintf("svc%d", i))
		w.cmd.Stderr = &w.stderr
		in, err := w.cmd.StdinPipe()
		if err != nil {
			tb.Fatal(err)
		}
		out, err := w.cmd.StdoutPipe()
		if err != nil {
			tb.Fatal(err)
		}
		w.stdin, w.out = in, bufio.NewReader(out)
		if err := w.cmd.Start(); err != nil {
			tb.Fatal(err)
		}
		workers[i] = w
	}
	for _, w := range workers {
		if line, err := w.out.ReadString('\n'); err != nil || strings.TrimSpace(line) != "ready" {
			tb.Fatalf("worker not ready: %q, %v: %s", line, err, w.stderr.String())
		}
	}
	if b, ok := tb.(*testing.B); ok {
		b.StartTimer()
	}
	start := time.Now()
	for _, w := range workers {
		if _, err := w.stdin.Write([]byte("go\n")); err != nil {
			tb.Fatal(err)
		}
	}
	waits := make([]time.Duration, 0, n)
	for _, w := range workers {
		line, err := w.out.ReadString('\n')
		if err != nil {
			tb.Fatalf("worker failed: %v: %s", err, w.stderr.String())
		}
		fields := strings.Fields(line)
		wait, perr := strconv.ParseInt(fields[0], 10, 64)
		if len(fields) != 2 || perr != nil {
			tb.Fatalf("unexpected worker output %q", line)
		}
		waits = append(waits, time.Duration(wait))
	}
	total := time.Since(start)
	if b, ok := tb.(*testing.B); ok {
		b.StopTimer()
	}
	for _, w := range workers {
		if err := w.cmd.Wait(); err != nil {
			tb.Fatalf("worker exit: %v: %s", err, w.stderr.String())
		}
	}
	return waits, total
}

// BenchmarkUpdateLockWait starts 5 or 20 writer processes that call Update on the same configuration at
// once. Per process it records the time from the lock request until it holds the lock; the metrics are
// taken over all processes of all iterations. "total-ms" is the span until the last writer is done.
func BenchmarkUpdateLockWait(b *testing.B) {
	isolateStorageEnv(b)
	for _, size := range []int{1000, 10000} {
		data := storageFixture(b, size)
		for _, writers := range []int{5, 20} {
			b.Run(fmt.Sprintf("entries-%d/writers-%d", size, writers), func(b *testing.B) {
				path := filepath.Join(b.TempDir(), "qatlas", "config.yaml")
				var waits []time.Duration
				var total time.Duration
				b.StopTimer()
				for i := 0; i < b.N; i++ {
					// Every round starts from the same fixture, so the file does not grow across rounds.
					writeStorageFixture(b, path, data)
					w, t := lockWaitRound(b, path, writers)
					waits = append(waits, w...)
					total += t
				}
				sortDurations(waits)
				b.ReportMetric(ms(percentile(waits, 0.50)), "wait-p50-ms")
				b.ReportMetric(ms(percentile(waits, 0.95)), "wait-p95-ms")
				b.ReportMetric(ms(waits[len(waits)-1]), "wait-max-ms")
				b.ReportMetric(ms(total)/float64(b.N), "total-ms")
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
	isolateStorageEnv(t)
	data := storageFixture(t, 30)
	path := filepath.Join(t.TempDir(), "qatlas", "config.yaml")
	writeStorageFixture(t, path, data)

	cfg, err := NewStore(path, testProviders).Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if len(cfg.Services) != 10 || len(cfg.Credentials) != 10 || len(cfg.Connections) != 10 {
		t.Fatalf("counts = %d/%d/%d, want 10 each", len(cfg.Services), len(cfg.Credentials), len(cfg.Connections))
	}

	const n = 3
	waits, total := lockWaitRound(t, path, n)
	if len(waits) != n || total <= 0 {
		t.Fatalf("waits = %v, total = %v", waits, total)
	}
	cfg, err = NewStore(path, testProviders).Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	for i := 0; i < n; i++ {
		if _, ok := cfg.Services[fmt.Sprintf("svc%d", i)]; !ok {
			t.Errorf("service svc%d was lost", i)
		}
	}
}
