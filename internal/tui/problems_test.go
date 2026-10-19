package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// rowLines returns the lines of the view from the row with the label up to the next row.
func rowLines(view, label string) []string {
	lines := strings.Split(view, "\n")
	for i, l := range lines {
		if strings.Contains(l, label) {
			return lines[i:]
		}
	}
	return nil
}

// refusal is everything the editor says about the last refused save: the status line and the form.
func refusal(m *Model) string { return m.fail + "\n" + screenOf(m) }

func assertNoLocation(t *testing.T, view string) {
	t.Helper()
	for _, leak := range []string{"config.yaml", "services.", "connections.", "credentials.", ".base_url"} {
		if strings.Contains(view, leak) {
			t.Errorf("the view names %q:\n%s", leak, view)
		}
	}
}

// A refused save puts cause and resolution under the row it concerns, counts the problems on the status
// line, and names no path or configuration key.
func TestRefusedSaveShowsProblemUnderItsRow(t *testing.T) {
	m, _, _ := newModel(t)
	addService(t, m, "wiki", "ftp://wiki.example.invalid")

	if m.screen != screenForm {
		t.Fatalf("screen = %v, want the form kept", m.screen)
	}
	if m.fail != "1 problem; nothing was saved" {
		t.Errorf("status = %q", m.fail)
	}
	view := screenOf(m)
	lines := rowLines(view, "base url")
	if len(lines) < 2 || !strings.Contains(strings.Join(lines[:4], "\n"), "[!] ") {
		t.Fatalf("no problem under the base url row:\n%s", view)
	}
	if !strings.Contains(view, "Correct base") && !strings.Contains(view, "leave it out") {
		t.Errorf("the resolution is missing:\n%s", view)
	}
	assertNoLocation(t, view)
}

// Two problems of different rows appear apart, each under its own row.
func TestRefusedSaveSeparatesProblems(t *testing.T) {
	m, _, _ := newModel(t)
	addCredential(t, m, "token", "bad name", "also bad")

	if m.fail != "2 problems; nothing was saved" {
		t.Fatalf("status = %q", m.fail)
	}
	if n := strings.Count(screenOf(m), "[!] "); n != 2 {
		t.Errorf("%d problem lines, want 2:\n%s", n, screenOf(m))
	}
	if len(m.problems.rows) != 2 || len(m.problems.other) != 0 {
		t.Errorf("rows %v, other %v", m.problems.rows, m.problems.other)
	}
}

// Changing the row retires its lines; the other rows keep theirs.
func TestChangingTheRowClearsItsProblem(t *testing.T) {
	m, _, _ := newModel(t)
	addCredential(t, m, "token", "bad name", "also bad")

	focused := m.fields[m.focus].label
	if len(m.problems.rows[focused]) == 0 {
		t.Fatalf("focus is on %q, which has no problem", focused)
	}
	typeText(t, m, "x")
	if n := strings.Count(screenOf(m), "[!] "); n != 1 {
		t.Errorf("%d problem lines after changing the row, want 1:\n%s", n, screenOf(m))
	}
}

// A problem of another entry has no row; it stands above the form with the kind and name of the entry.
func TestProblemWithoutRowNamesTheEntry(t *testing.T) {
	m, _, _ := newModel(t)
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)

	err := errors.Join(config.Problem{
		Section: config.SectionConnections, Name: "work", Field: "service", Code: config.CodeUnknownService,
	})
	m.reportSaveError(err, "wiki", true)

	view := screenOf(m)
	if !strings.Contains(view, "connection work: ") || !strings.Contains(view, "Choose a service that exists") {
		t.Errorf("the entry is not named:\n%s", view)
	}
	assertNoLocation(t, view)
	if m.fail != "1 problem; nothing was saved" {
		t.Errorf("status = %q", m.fail)
	}
}

// Errors that hold no problem keep their text.
func TestErrorWithoutProblemKeepsItsText(t *testing.T) {
	m, _, _ := newModel(t)
	m.reportSaveError(errors.New("disk full"), "", true)
	if m.fail != "disk full" {
		t.Errorf("status = %q", m.fail)
	}
}

// A configuration file made invalid by hand is still reported with its path.
func TestInvalidFileNamesItsPath(t *testing.T) {
	m, _, path := newModel(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("version: 1\nservices:\n  wiki:\n    provider: nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := buildModel(m.store, nil, m.secrets, nil)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("loading an invalid file = %v, want an error naming %s", err, path)
	}
}
