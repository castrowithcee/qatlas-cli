package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
)

// fakeUpdater answers the check and the installation from its fields and counts the installations.
type fakeUpdater struct {
	check     selfupdate.Result
	checkErr  error
	update    selfupdate.Result
	updateErr error
	checks    int
	updates   int
}

func (f *fakeUpdater) Check(ctx context.Context) (selfupdate.Result, error) {
	f.checks++
	if _, ok := ctx.Deadline(); !ok {
		return selfupdate.Result{}, errors.New("the check runs without a deadline")
	}
	return f.check, f.checkErr
}

func (f *fakeUpdater) Update(context.Context) (selfupdate.Result, error) {
	f.updates++
	return f.update, f.updateErr
}

func newerRelease() *fakeUpdater {
	return &fakeUpdater{
		check:  selfupdate.Result{Current: "v0.4.0", Latest: "v0.5.0", UpdateAvailable: true},
		update: selfupdate.Result{Current: "v0.4.0", Latest: "v0.5.0", UpdateAvailable: true, Updated: true},
	}
}

// deliver runs a command and hands its messages back to the model, the way the event loop does.
func deliver(m *Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			deliver(m, c)
		}
		return
	}
	if msg != nil {
		_, next := m.Update(msg)
		deliver(m, next)
	}
}

// startedWith builds an editor with the updater and runs its start, the check included.
func startedWith(t *testing.T, updater Updater) *Model {
	t.Helper()
	m, _, _ := newModel(t)
	m.updater = updater
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	deliver(m, m.Init())
	return m
}

const fullBanner = "Update available v0.4.0 → v0.5.0 🎉 · u update"

func TestTheBannerNamesOnlyANewerRelease(t *testing.T) {
	fake := newerRelease()
	m := startedWith(t, fake)
	if fake.checks != 1 {
		t.Fatalf("the start ran %d checks, want 1", fake.checks)
	}
	if line := strings.Split(m.View(), "\n")[0]; !strings.Contains(line, fullBanner) {
		t.Errorf("the top line does not name the newer release: %q", line)
	}

	for name, fake := range map[string]*fakeUpdater{
		"current":   {check: selfupdate.Result{Current: "v0.5.0", Latest: "v0.5.0"}},
		"offline":   {checkErr: errors.New("dial tcp: no route to host")},
		"dev build": {checkErr: selfupdate.ErrDevelopmentBuild},
	} {
		m := startedWith(t, fake)
		view := m.View()
		if strings.Contains(view, "Update") || strings.Contains(view, "error:") || m.fail != "" || m.status != "" {
			t.Errorf("%s: the editor is not silent:\n%s", name, view)
		}
		press(t, m, "u")
		if m.screen != screenNav || fake.updates != 0 {
			t.Errorf("%s: u acted without a newer release: screen %v updates %d", name, m.screen, fake.updates)
		}
	}

	// Without an updater, as in a dev build or with the check switched off, nothing is asked at all.
	m = startedWith(t, nil)
	if m.checkUpdate() != nil || strings.Contains(m.View(), "Update") {
		t.Errorf("an editor without an updater checks or shows a banner:\n%s", m.View())
	}
}

func TestUAsksBeforeItInstalls(t *testing.T) {
	for _, no := range []string{"n", "esc"} {
		fake := newerRelease()
		m := startedWith(t, fake)
		press(t, m, "u")
		if m.screen != screenUpdate || !strings.Contains(screenOf(m), "Update qatlas to v0.5.0?") ||
			!strings.Contains(screenOf(m), "y update · n/esc cancel") {
			t.Fatalf("u did not ask:\n%s", m.View())
		}
		pump(t, m, no)
		if m.screen != screenNav || fake.updates != 0 || !strings.Contains(m.View(), fullBanner) {
			t.Errorf("%s installed or lost the release: screen %v updates %d\n%s", no, m.screen, fake.updates, m.View())
		}
	}

	fake := newerRelease()
	m := startedWith(t, fake)
	press(t, m, "enter")
	_, cmd := m.Update(keyMsg("u"))
	if cmd != nil || m.screen != screenUpdate {
		t.Fatalf("u in the list did not ask first: screen %v", m.screen)
	}
	_, cmd = m.Update(keyMsg("y"))
	if m.screen != screenList || !m.updating {
		t.Fatalf("y did not start the update in the background: screen %v updating %v", m.screen, m.updating)
	}
	view := m.View()
	if !strings.Contains(strings.Split(view, "\n")[0], "Updating to v0.5.0 …") {
		t.Errorf("the running update is not shown in the top line:\n%s", view)
	}
	press(t, m, "u")
	if m.screen != screenList {
		t.Errorf("u asked again while the update runs")
	}
	deliver(m, cmd)
	if fake.updates != 1 {
		t.Fatalf("y ran %d installations, want 1", fake.updates)
	}
	view = m.View()
	for _, want := range []string{"Updated to v0.5.0 · restart qatlas tui", "Updated to v0.5.0. Restart qatlas tui to use it."} {
		if !strings.Contains(view, want) {
			t.Errorf("the finished update does not say %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "u update") {
		t.Errorf("the installed release is still offered:\n%s", view)
	}
	press(t, m, "u")
	if m.screen != screenList || fake.updates != 1 {
		t.Errorf("u offered the installed release again")
	}
}

func TestUpdateFailuresAreReported(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want []string
	}{
		{name: "unsupported installation",
			err: &selfupdate.UnsupportedInstallationError{Reason: "the executable is a symlink"},
			want: []string{"error: cannot self-update this installation: the executable is a symlink",
				"'qatlas update --help'"}},
		{name: "download failure", err: errors.New("GitHub returned 502 for token s3cr3t-value"),
			want: []string{"error: update failed: GitHub returned 502"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newerRelease()
			fake.updateErr = tt.err
			m := startedWith(t, fake)
			m.redactor.Add("s3cr3t-value")
			pump(t, m, "u", "y")
			view := strings.Join(strings.Fields(screenOf(m)), " ")
			for _, want := range tt.want {
				if !strings.Contains(view, want) {
					t.Errorf("the failure does not say %q:\n%s", want, screenOf(m))
				}
			}
			if strings.Contains(m.View(), "s3cr3t-value") {
				t.Errorf("the failure echoed a secret:\n%s", m.View())
			}
			if m.updating || m.updated != "" {
				t.Errorf("a failed update left updating %v updated %q", m.updating, m.updated)
			}
		})
	}
}

// In a form u is a letter like any other.
func TestUIsTextInAForm(t *testing.T) {
	fake := newerRelease()
	m := startedWith(t, fake)
	press(t, m, "1", "n")
	typeText(t, m, "u")
	if m.screen != screenForm || m.fieldValue("name") != "u" || fake.updates != 0 {
		t.Errorf("u in a form was not typed: screen %v name %q", m.screen, m.fieldValue("name"))
	}
}

// The banner shortens with the terminal, and the frame stays in line however wide it is.
func TestTheBannerKeepsTheFrameInLine(t *testing.T) {
	m := startedWith(t, newerRelease())
	for _, tt := range []struct {
		width, height int
		banner        string
		sidebar       bool
	}{
		{width: 100, height: 28, banner: fullBanner, sidebar: true},
		{width: 60, height: 24, banner: "Update v0.5.0 · u"},
		{width: 40, height: 12, banner: "Update v0.5.0 · u"},
	} {
		m.Update(tea.WindowSizeMsg{Width: tt.width, Height: tt.height})
		for _, screen := range []string{"sidebar", "help", "question"} {
			m.screen = screenNav
			switch screen {
			case "help":
				press(t, m, "?")
			case "question":
				press(t, m, "u")
			}
			view := m.View()
			assertViewFits(t, view, tt.width, tt.height)
			lines := strings.Split(view, "\n")
			if !strings.Contains(lines[0], tt.banner) {
				t.Errorf("%dx%d %s: the top line is %q, want %q", tt.width, tt.height, screen, lines[0], tt.banner)
			}
			if tt.width < 100 && strings.Contains(lines[0], "🎉") {
				t.Errorf("%dx%d: a narrow top line keeps the emoji: %q", tt.width, tt.height, lines[0])
			}
			if !tt.sidebar {
				continue
			}
			if len(lines) != tt.height {
				t.Errorf("%dx%d %s: the frame has %d lines", tt.width, tt.height, screen, len(lines))
			}
			for i, line := range lines[1:] {
				if got := lipgloss.Width(line); got != tt.width {
					t.Errorf("%dx%d %s: frame line %d is %d cells wide: %q", tt.width, tt.height, screen, i+1, got, line)
				}
			}
		}
	}
}

func TestTheBannerReadsWithoutColour(t *testing.T) {
	before := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(before) })

	m := startedWith(t, newerRelease())
	colour := before
	colour = 2 // termenv.ANSI
	lipgloss.SetColorProfile(colour)
	if line := strings.Split(m.View(), "\n")[0]; !strings.Contains(line, "\x1b[") {
		t.Errorf("a colour terminal got a plain banner: %q", line)
	}
	colour = 3 // termenv.Ascii
	lipgloss.SetColorProfile(colour)
	view := m.View()
	if strings.Contains(view, "\x1b") {
		t.Errorf("a terminal without colour got escape sequences:\n%q", view)
	}
	if !strings.Contains(strings.Split(view, "\n")[0], fullBanner) {
		t.Errorf("the banner does not read without colour:\n%s", view)
	}
}
