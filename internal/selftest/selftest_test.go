package selftest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The tests start the test binary itself as the writer process: TestMain turns it into a worker when the
// mode variable is set.
const workerModeEnv = "QATLAS_SELFTEST_TEST_WORKER"

func TestMain(m *testing.M) {
	switch os.Getenv(workerModeEnv) {
	case "":
		os.Exit(m.Run())
	case "worker":
		os.Exit(RunWorker(os.Stdin, os.Stdout, os.Stderr))
	case "hang":
		time.Sleep(time.Minute)
	case "fail":
		os.Stderr.WriteString("selftest worker: boom\n")
		os.Exit(3)
	}
	os.Exit(0)
}

func testWorker(mode string) WorkerCommand {
	return WorkerCommand{Program: os.Args[0], Env: []string{workerModeEnv + "=" + mode}}
}

func skipShort(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("spawns subprocesses")
	}
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range list {
		names = append(names, e.Name())
	}
	return names
}

func TestSmokeProfileMeasuresEverything(t *testing.T) {
	skipShort(t)
	base := t.TempDir()
	var progress []string
	report, err := Run(context.Background(), Options{
		Scenario: "storage", Profile: Smoke(), BaseDir: base, Version: "v-test",
		Worker: testWorker("worker"), Progress: func(s string) { progress = append(progress, s) },
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if report.Version != "v-test" || report.GOOS == "" || report.GOARCH == "" || report.CPUs < 1 ||
		report.Scenario != "storage" || report.Profile != "smoke" {
		t.Errorf("header = %+v", report)
	}
	if filepath.Dir(report.TempDir) != base {
		t.Errorf("TempDir = %s, want a child of %s", report.TempDir, base)
	}
	if _, err := os.Stat(report.TempDir); !os.IsNotExist(err) {
		t.Errorf("the scratch directory still exists: %v", err)
	}
	if got := entries(t, base); len(got) != 0 {
		t.Errorf("base directory holds %v after the run", got)
	}

	want := map[string]int{"read-under-write": 2, "lock-wait": 2, "append": 4}
	got := map[string]int{}
	for _, m := range report.Measurements {
		got[m.Group]++
		if len(m.Values) == 0 || m.Case == "" {
			t.Errorf("empty measurement %+v", m)
		}
		for _, v := range m.Values {
			if v.Value < 0 {
				t.Errorf("%s/%s %s = %v", m.Group, m.Case, v.Name, v.Value)
			}
		}
		if m.Group == "read-under-write" && (m.Values[0].Name != "read-p50-ms" || m.Values[2].Name != "retries") {
			t.Errorf("read values = %+v", m.Values)
		}
	}
	for group, n := range want {
		if got[group] != n {
			t.Errorf("%s measurements = %d, want %d", group, got[group], n)
		}
	}
	if len(progress) != 8 {
		t.Errorf("progress lines = %v", progress)
	}
}

func TestRunnerIgnoresEnvironment(t *testing.T) {
	skipShort(t)
	guard := t.TempDir()
	for _, name := range []string{"HOME", "USERPROFILE", "QATLAS_CLI_HOME", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(name, guard)
	}
	t.Setenv("QATLAS_CONFIG", filepath.Join(guard, "config.yaml"))
	if _, err := Run(context.Background(), Options{
		Scenario: "storage", Profile: Smoke(), BaseDir: t.TempDir(), Worker: testWorker("worker"),
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := entries(t, guard); len(got) != 0 {
		t.Errorf("the guard directory was touched: %v", got)
	}
}

func TestCancelRemovesScratchDirectory(t *testing.T) {
	skipShort(t)
	for _, phase := range []string{"read-under-write", "lock-wait", "append"} {
		t.Run(phase, func(t *testing.T) {
			base := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// hang workers never get ready, so a cancel in the lock-wait phase lands in the middle of the
			// process round.
			mode := "worker"
			if phase == "lock-wait" {
				mode = "hang"
			}
			start := time.Now()
			_, err := Run(ctx, Options{
				Scenario: "storage", Profile: Smoke(), BaseDir: base, Worker: testWorker(mode),
				Progress: func(s string) {
					if strings.HasPrefix(s, phase) {
						if phase == "lock-wait" {
							time.AfterFunc(300*time.Millisecond, cancel)
						} else {
							cancel()
						}
					}
				},
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Run() error = %v, want context.Canceled", err)
			}
			if time.Since(start) > 30*time.Second {
				t.Errorf("Run took %v after cancel", time.Since(start))
			}
			if got := entries(t, base); len(got) != 0 {
				t.Errorf("base directory holds %v after the run", got)
			}
		})
	}
}

func TestWorkerRejectsOtherProtocol(t *testing.T) {
	var out, errOut bytes.Buffer
	in := strings.NewReader(`{"protocol":99,"path":"/x","name":"a"}` + "\n")
	if code := RunWorker(in, &out, &errOut); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "protocol version 99 is not supported") || out.Len() != 0 {
		t.Errorf("stderr = %q, stdout = %q", errOut.String(), out.String())
	}
}

func TestWorkerFailureIsReported(t *testing.T) {
	skipShort(t)
	_, err := RunLockWaitRound(context.Background(), testWorker("fail"), filepath.Join(t.TempDir(), "c.yaml"), 2, nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v, want the worker's message", err)
	}
	if _, err := RunLockWaitRound(context.Background(), WorkerCommand{}, "x", 1, nil); err == nil {
		t.Error("no program: error = nil")
	}
}

func TestRunRejectsUnknownScenarioAndBadProfile(t *testing.T) {
	if _, err := Run(context.Background(), Options{Scenario: "nope", Profile: Smoke()}); err == nil {
		t.Error("unknown scenario: error = nil")
	}
	bad := Smoke()
	bad.Storage.ReadEntries = []int{2}
	if _, err := Run(context.Background(), Options{Scenario: "storage", Profile: bad}); err == nil {
		t.Error("bad profile: error = nil")
	}
	if _, err := ProfileByName("huge"); err == nil {
		t.Error("ProfileByName(huge): error = nil")
	}
	for _, name := range []string{"standard", "smoke"} {
		p, err := ProfileByName(name)
		if err != nil || p.validate() != nil {
			t.Errorf("ProfileByName(%s) = %v, %v", name, err, p.validate())
		}
	}
}

func TestStatsOf(t *testing.T) {
	var samples []time.Duration
	for i := 100; i >= 1; i-- {
		samples = append(samples, time.Duration(i))
	}
	st := StatsOf(samples)
	if st.Samples != 100 || st.P50 != 50 || st.P95 != 95 || st.Max != 100 {
		t.Errorf("StatsOf = %+v", st)
	}
	if samples[0] != 100 {
		t.Error("StatsOf sorted its input")
	}
	if (StatsOf(nil) != Stats{}) {
		t.Error("StatsOf(nil) is not zero")
	}
}

func TestFixturesAreSynthetic(t *testing.T) {
	fx, err := NewStorageFixture(t.TempDir(), 30)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fx.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("provider: synthetic")) || bytes.Contains(data, []byte("bookstack")) {
		t.Error("the fixture names a provider other than the synthetic one")
	}
	af, err := NewAppendFixture(t.TempDir(), 3, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := af.Step(); err != nil {
		t.Fatalf("Step() = %v", err)
	}
}

func TestLockWaitRoundKeepsAllChanges(t *testing.T) {
	skipShort(t)
	fx, err := NewStorageFixture(t.TempDir(), 30)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := fx.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Services) != 10 || len(cfg.Credentials) != 10 || len(cfg.Connections) != 10 {
		t.Fatalf("counts = %d/%d/%d, want 10 each", len(cfg.Services), len(cfg.Credentials), len(cfg.Connections))
	}
	const n = 3
	round, err := RunLockWaitRound(context.Background(), testWorker("worker"), fx.Path(), n, nil)
	if err != nil || len(round.Waits) != n || round.Total <= 0 {
		t.Fatalf("round = %+v, err = %v", round, err)
	}
	cfg, err = fx.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, ok := cfg.Services[fmt.Sprintf("svc%d", i)]; !ok {
			t.Errorf("service svc%d was lost", i)
		}
	}
}
