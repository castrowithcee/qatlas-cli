package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

var tuiNow = time.Date(2030, 1, 2, 9, 0, 0, 0, time.UTC)

func timedModel(t *testing.T, conn config.Connection) (*Model, string) {
	t.Helper()
	conn.Service, conn.Credential = "wiki", "reader"
	conn.Tools = []string{"bookstack.pages.list"}
	m, path := toolsModel(t, wikiRegistry(t), map[string]config.Connection{"wiki": conn})
	m.now = func() time.Time { return tuiNow }
	return m, path
}

func saveForm(t *testing.T, m *Model, path string) config.Connection {
	t.Helper()
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	return savedConfig(t, path).Connections["wiki"]
}

func TestActiveForRowIsShownOnlyWhileActive(t *testing.T) {
	m, _ := timedModel(t, config.Connection{})
	openEntryForm(t, m, sectionConnections, "wiki")
	if f := m.field(activeForLabel); f == nil || f.hidden || f.value() != activeForUnlimited {
		t.Fatalf("active for row = %+v, want it shown on unlimited", f)
	}
	if got := strings.Join(m.field(activeForLabel).choices, " "); got != "unlimited 1h 4h 8h 24h" {
		t.Fatalf("choices = %q", got)
	}
	focusField(t, m, stateLabel)
	press(t, m, "right")
	if !m.field(activeForLabel).hidden {
		t.Fatal("the row stays visible for a disabled connection")
	}
	press(t, m, "left")
	if m.field(activeForLabel).hidden {
		t.Fatal("the row stays hidden for an active connection")
	}
}

func TestDurationBecomesActiveUntilAndUnlimitedRemovesIt(t *testing.T) {
	m, path := timedModel(t, config.Connection{})
	openEntryForm(t, m, sectionConnections, "wiki")
	focusField(t, m, activeForLabel)
	press(t, m, "right", "right")
	if got := m.fieldValue(activeForLabel); got != "4h" {
		t.Fatalf("row = %q, want 4h", got)
	}
	if got := saveForm(t, m, path).ActiveUntil; got != "2030-01-02T13:00:00Z" {
		t.Fatalf("active_until = %q, want now plus 4h", got)
	}

	// The saved limit leads the row; unlimited is the next choice.
	m.now = func() time.Time { return tuiNow.Add(time.Hour) }
	m.screen = screenList
	openEntryForm(t, m, sectionConnections, "wiki")
	focusField(t, m, activeForLabel)
	if got := m.fieldValue(activeForLabel); got != "3h0m left" {
		t.Fatalf("row = %q, want the remaining time, 3h0m left", got)
	}
	press(t, m, "right")
	if got := saveForm(t, m, path).ActiveUntil; got != "" {
		t.Fatalf("active_until = %q after unlimited, want none", got)
	}
}

func TestSavingWithoutChangeKeepsTheInstant(t *testing.T) {
	for name, at := range map[string]time.Time{"running": tuiNow.Add(3*time.Hour + 12*time.Minute + 30*time.Second),
		"expired": tuiNow.Add(-time.Hour)} {
		m, path := timedModel(t, config.Connection{ActiveUntil: config.FormatActiveUntil(at)})
		openEntryForm(t, m, sectionConnections, "wiki")
		want := map[string]string{"running": "3h12m left", "expired": expiredText}[name]
		if got := m.fieldValue(activeForLabel); got != want {
			t.Fatalf("%s: row = %q, want %q", name, got, want)
		}
		if got := saveForm(t, m, path).ActiveUntil; got != config.FormatActiveUntil(at) {
			t.Errorf("%s: active_until = %q, want it kept", name, got)
		}
	}
}

func TestDisabledConnectionSavesWithoutALimit(t *testing.T) {
	m, path := timedModel(t, config.Connection{ActiveUntil: config.FormatActiveUntil(tuiNow.Add(time.Hour))})
	openEntryForm(t, m, sectionConnections, "wiki")
	focusField(t, m, stateLabel)
	press(t, m, "right")
	got := saveForm(t, m, path)
	if !got.Disabled || got.ActiveUntil != "" {
		t.Fatalf("saved = %+v, want disabled without a limit", got)
	}

	// Switching it on again starts a new duration, not the old one.
	m.screen = screenList
	openEntryForm(t, m, sectionConnections, "wiki")
	focusField(t, m, stateLabel)
	press(t, m, "left")
	if f := m.field(activeForLabel); f.hidden || f.value() != activeForUnlimited {
		t.Fatalf("row after switching on = %+v, want it shown on unlimited", f)
	}
}

func TestListShowsTheRemainingTimeOrExpired(t *testing.T) {
	for at, want := range map[time.Duration]string{
		3*time.Hour + 12*time.Minute: "active 3h12m", 90 * time.Second: "active 1m", 30 * time.Second: "active <1m",
		26 * time.Hour: "active 1d2h", 0: expiredText, -time.Hour: expiredText,
	} {
		m, _ := timedModel(t, config.Connection{ActiveUntil: config.FormatActiveUntil(tuiNow.Add(at))})
		if got := stateCell(m); got != want {
			t.Errorf("state cell for %v = %q, want %q", at, got, want)
		}
	}
	m, _ := timedModel(t, config.Connection{})
	if got := stateCell(m); got != stateActive {
		t.Errorf("state cell without a limit = %q", got)
	}
}

// A copy of a connection with a limit carries the same instant; one without a limit starts unlimited.
func TestCopyKeepsTheInstantOfItsOriginal(t *testing.T) {
	limit := config.FormatActiveUntil(tuiNow.Add(5 * time.Hour))
	m, path := timedModel(t, config.Connection{ActiveUntil: limit})
	openEntryForm(t, m, sectionConnections, "wiki")
	m.screen = screenList
	m.list.selectName("wiki")
	pump(t, m, "p")
	if m.copyFrom != "wiki" || m.fieldValue(activeForLabel) != "5h0m left" {
		t.Fatalf("copy form: from %q, row %q, want wiki and 5h0m left", m.copyFrom, m.fieldValue(activeForLabel))
	}
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatal(m.fail)
	}
	if got := savedConfig(t, path).Connections["wiki-copy"].ActiveUntil; got != limit {
		t.Errorf("copy active_until = %q, want %q", got, limit)
	}
}

func stateCell(m *Model) string {
	m.section = sectionConnections
	return m.cells("wiki")[1]
}
