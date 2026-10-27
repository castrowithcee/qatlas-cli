package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/selftest"
)

func fakeScenarios(names ...string) func() []selftest.Scenario {
	return func() []selftest.Scenario {
		var list []selftest.Scenario
		for _, n := range names {
			list = append(list, selftest.Scenario{Name: n, Summary: "fake " + n})
		}
		return list
	}
}

func fakeReport(scenario string) *selftest.Report {
	return &selftest.Report{
		Scenario: scenario, Profile: "standard", Version: "v9.9.9", GOOS: "testos", GOARCH: "testarch", CPUs: 3,
		TempDir: "/scratch/qatlas-selftest-1", ElapsedMs: 12,
		Measurements: []selftest.Measurement{{Group: "append", Case: "days-1",
			Values: []selftest.Value{{Name: "append-p50-ms", Value: 1.5}, {Name: "n", Value: 4}}}},
	}
}

// startSelftestCommand types the command and returns the command that follows the run.
func startSelftestCommand(t *testing.T, m *Model, name string) tea.Cmd {
	t.Helper()
	press(t, m, ":")
	typeText(t, m, name)
	_, cmd := m.Update(keyMsg("enter"))
	return cmd
}

// drainSelftest hands the events of a run to the model until the run is over.
func drainSelftest(m *Model, cmd tea.Cmd) {
	for cmd != nil {
		_, cmd = m.Update(cmd())
	}
}

func TestSelftestScenarioIsACommand(t *testing.T) {
	m := cmdModel(t)
	if !strings.Contains(strings.Join(commandNames(m.commands()), ","), "selftest storage") {
		t.Fatalf("the register lacks selftest storage: %v", commandNames(m.commands()))
	}
	m.selftest.scenarios = fakeScenarios("alpha", "beta")
	names := commandNames(m.commands())
	for _, want := range []string{"selftest alpha", "selftest beta"} {
		if !strings.Contains(strings.Join(names, ","), want) {
			t.Errorf("the register lacks %q: %v", want, names)
		}
	}
}

func TestSelftestShowsProgressAndTheResult(t *testing.T) {
	m := cmdModel(t)
	m.selftest.scenarios = fakeScenarios("alpha")
	m.selftest.Version = "v9.9.9"
	release := make(chan struct{})
	var got selftest.Options
	m.selftest.run = func(ctx context.Context, o selftest.Options) (*selftest.Report, error) {
		got = o
		o.Progress("step one")
		<-release
		return fakeReport(o.Scenario), nil
	}
	cmd := startSelftestCommand(t, m, "selftest alpha")
	if m.screen != screenSelftest {
		t.Fatalf("screen = %v, want the selftest", m.screen)
	}
	_, cmd = m.Update(cmd())
	if view := screenOf(m); !strings.Contains(view, "step one") || !strings.Contains(view, "Running") {
		t.Fatalf("no progress:\n%s", view)
	}
	close(release)
	drainSelftest(m, cmd)
	view := screenOf(m)
	for _, want := range []string{"selftest alpha (profile standard)", "version:   v9.9.9", "testos/testarch",
		"/scratch/qatlas-selftest-1", "append-p50-ms=1.50", "n=4"} {
		if !strings.Contains(view, want) {
			t.Errorf("result lacks %q:\n%s", want, view)
		}
	}
	if got.Profile.Name != selftest.Standard().Name || got.Version != "v9.9.9" {
		t.Errorf("options = %+v, want the CLI default profile and the version", got)
	}
	press(t, m, "esc")
	if m.screen != screenNav || m.st != nil {
		t.Errorf("esc left screen %v state %v", m.screen, m.st)
	}
}

func TestSelftestResultIsSavedAsJSONInTheTempDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	m := cmdModel(t)
	m.selftest.scenarios = fakeScenarios("alpha")
	m.selftest.run = func(_ context.Context, o selftest.Options) (*selftest.Report, error) {
		return fakeReport(o.Scenario), nil
	}
	drainSelftest(m, startSelftestCommand(t, m, "selftest alpha"))
	press(t, m, "w")
	if m.st.saved == "" || filepath.Dir(m.st.saved) != tmp {
		t.Fatalf("saved = %q, want a file in %s", m.st.saved, tmp)
	}
	data, err := os.ReadFile(m.st.saved)
	if err != nil {
		t.Fatal(err)
	}
	var back selftest.Report
	if err := json.Unmarshal(data, &back); err != nil || back.Scenario != "alpha" || len(back.Measurements) != 1 {
		t.Fatalf("file = %s (%v)", data, err)
	}
	if !strings.Contains(screenOf(m), "saved to") {
		t.Errorf("the path is not shown:\n%s", screenOf(m))
	}
}

func TestSelftestEscCancelsAndTheScratchDirectoryIsGone(t *testing.T) {
	base := t.TempDir()
	m := cmdModel(t)
	// The real selftest.Run runs the real storage scenario in a directory below base. The run is held after
	// its first step until it is cancelled, so the cancel always meets a running scenario.
	m.selftest.run = func(ctx context.Context, o selftest.Options) (*selftest.Report, error) {
		o.Profile, o.BaseDir = selftest.Smoke(), base
		progress, once := o.Progress, sync.Once{}
		o.Progress = func(line string) {
			progress(line)
			once.Do(func() { <-ctx.Done() })
		}
		return selftest.Run(ctx, o)
	}
	cmd := startSelftestCommand(t, m, "selftest storage")
	_, cmd = m.Update(cmd())
	if entries, _ := os.ReadDir(base); len(entries) != 1 {
		t.Fatalf("the scratch directory is not there while running: %v", entries)
	}
	press(t, m, "esc")
	if !m.st.cancelling || !strings.Contains(screenOf(m), "Cancelling") || m.st.done {
		t.Fatalf("esc did not start the cancel:\n%s", screenOf(m))
	}
	drainSelftest(m, cmd)
	if !m.st.done || !m.st.cancelled || !strings.Contains(screenOf(m), "Cancelled") {
		t.Fatalf("the cancel is not confirmed:\n%s", screenOf(m))
	}
	if entries, _ := os.ReadDir(base); len(entries) != 0 {
		t.Errorf("the scratch directory is left: %v", entries)
	}
}

func TestSelftestCtrlCWaitsForTheRunBeforeQuitting(t *testing.T) {
	m := cmdModel(t)
	m.selftest.scenarios = fakeScenarios("alpha")
	m.selftest.run = func(ctx context.Context, o selftest.Options) (*selftest.Report, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	cmd := startSelftestCommand(t, m, "selftest alpha")
	if _, quit := m.Update(keyMsg("ctrl+c")); quit != nil || m.quitting {
		t.Fatal("ctrl+c quit before the run returned")
	}
	_, cmd = m.Update(cmd())
	if !m.quitting || cmd == nil {
		t.Errorf("the editor did not quit after the run returned (quitting %v)", m.quitting)
	}
}

func TestSelftestRefusesASecondStartWhileRunning(t *testing.T) {
	m := cmdModel(t)
	m.selftest.scenarios = fakeScenarios("alpha")
	starts := 0
	m.selftest.run = func(ctx context.Context, o selftest.Options) (*selftest.Report, error) {
		starts++
		<-ctx.Done()
		return nil, ctx.Err()
	}
	cmd := startSelftestCommand(t, m, "selftest alpha")
	press(t, m, ":")
	typeText(t, m, "selftest alpha")
	_, again := m.Update(keyMsg("enter"))
	if again != nil || !strings.Contains(screenOf(m), "already running") {
		t.Fatalf("the second start is not refused visibly:\n%s", screenOf(m))
	}
	press(t, m, "esc", "esc")
	drainSelftest(m, cmd)
	if starts != 1 {
		t.Errorf("starts = %d, want 1", starts)
	}
}

func TestSelftestErrorIsRedacted(t *testing.T) {
	m := cmdModel(t)
	m.redactor = &redact.Redactor{}
	m.redactor.Add("hunter2-secret")
	m.selftest.scenarios = fakeScenarios("alpha")
	m.selftest.run = func(context.Context, selftest.Options) (*selftest.Report, error) {
		return nil, errors.New("worker failed with hunter2-secret")
	}
	drainSelftest(m, startSelftestCommand(t, m, "selftest alpha"))
	view := screenOf(m)
	if !strings.Contains(view, "worker failed") || strings.Contains(view, "hunter2-secret") {
		t.Errorf("the error is not shown redacted:\n%s", view)
	}
}

func TestSelftestIsRefusedWithoutAWorker(t *testing.T) {
	m := cmdModel(t)
	press(t, m, ":")
	typeText(t, m, "selftest storage")
	m.Update(keyMsg("enter"))
	if m.screen != screenCommand || !strings.Contains(screenOf(m), "cannot start the selftest") {
		t.Errorf("screen %v:\n%s", m.screen, screenOf(m))
	}
}
