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
	for _, leak := range []string{"config.yaml", "services.", "connections.", "credentials.", ".base_url", "base_url", "idle_timeout", "vault.idle", "vault.admin"} {
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
	if !strings.Contains(view, "Correct the base URL") {
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

// In the guided setup a problem of the open step stands under its row; one of another step names the step.
func TestSetupProblemNamesItsStep(t *testing.T) {
	m, _, _, _, _ := newStoreModel(t)
	walkSetup(t, m, stepScope)

	err := errors.Join(
		config.Problem{Section: config.SectionConnections, Name: "personal", Field: "description",
			Code: config.CodeInvalidDescription},
		config.Problem{Section: config.SectionConnections, Name: "personal", Field: "service",
			Code: config.CodeUnknownService},
		config.Problem{Section: config.SectionConnections, Name: "personal", Field: "permissions",
			Code: config.CodeUnknownPermission},
	)
	m.reportSaveError(err, "", true)

	if m.fail != "3 problems; nothing was saved" {
		t.Errorf("status = %q", m.fail)
	}
	if len(m.problems.rows["description"]) != 1 || len(m.problems.rows) != 1 {
		t.Errorf("rows = %v, want only the description of the open step", m.problems.rows)
	}
	view := screenOf(m)
	for _, want := range []string{"Shorten the text", "step service: ", "Choose a service that exists",
		"step permissions: ", "Use one of the supported permissions"} {
		if !strings.Contains(view, want) {
			t.Errorf("the view lacks %q:\n%s", want, view)
		}
	}
	assertNoLocation(t, view)
}

// A problem found when the setup is saved shows on the summary with the step that can fix it.
func TestSetupSaveProblemNamesItsStepOnTheSummary(t *testing.T) {
	m, _, _, _, _ := newStoreModel(t)
	walkSetup(t, m, stepSummary)

	m.setupSaved(setupSavedMsg{err: errors.Join(config.Problem{
		Section: config.SectionServices, Name: "wiki", Field: "base_url", Code: config.CodeInvalidBaseURL,
	})})

	view := screenOf(m)
	if !strings.Contains(view, "step service: ") || !strings.Contains(view, "Correct the base URL") {
		t.Errorf("the step is not named:\n%s", view)
	}
	if m.fail != "1 problem; nothing was saved" {
		t.Errorf("status = %q", m.fail)
	}
	assertNoLocation(t, view)
}

// The payload form shows a refused save like every other form: under the row, with a resolution, gone once
// the row changes.
func TestPayloadFormShowsProblemUnderItsRow(t *testing.T) {
	h := newPayloadHarness(t, false)
	h.startPayload(t, "bad name", storageKeyring, "")
	h.addField(t, "pin", "zq9-valid-value")
	h.press(t, "f2")

	if h.m.fail != "1 problem; nothing was saved" || h.m.screen != screenForm {
		t.Fatalf("status = %q, screen %v", h.m.fail, h.m.screen)
	}
	view := screenOf(h.m)
	if len(h.m.problems.rows["name"]) != 1 || !strings.Contains(view, "[!] ") ||
		!strings.Contains(view, "Rename the entry") {
		t.Fatalf("no problem with its resolution under the name row:\n%s", view)
	}
	assertNoLocation(t, view)
	h.neverShown(t, "zq9-valid-value")

	h.m.focus = 0
	h.m.applyFocus()
	typeText(t, h.m, "x")
	if strings.Contains(screenOf(h.m), "[!] ") {
		t.Errorf("the problem stayed after the row changed:\n%s", screenOf(h.m))
	}
}

// A problem reported after the payload was handed to the store stands the same way.
func TestPayloadSavedErrorShowsProblemUnderItsRow(t *testing.T) {
	h := newPayloadHarness(t, false)
	h.startPayload(t, "billing", storageKeyring, "")
	h.addField(t, "pin", "zq9-valid-value")

	h.m.handlePayloadSaved(payloadSavedMsg{name: "billing", err: errors.Join(config.Problem{
		Section: config.SectionCredentials, Name: "billing", Field: "description",
		Code: config.CodeInvalidDescription,
	})})

	view := screenOf(h.m)
	if len(h.m.problems.rows["description"]) != 1 || !strings.Contains(view, "Shorten the text") {
		t.Errorf("no problem under the description row:\n%s", view)
	}
	if strings.Contains(h.m.fail, "not changed") {
		t.Errorf("status = %q", h.m.fail)
	}
}

// A refused deletion without a form names the kind and the entry the problem concerns.
func TestRefusedDeletionNamesKindAndEntry(t *testing.T) {
	m, _, _ := newModel(t)
	openSectionByName(t, m, sectionServices)

	m.reportSaveError(errors.Join(config.Problem{
		Section: config.SectionConnections, Name: "work", Field: "service", Code: config.CodeUnknownService,
	}), "wiki", false)

	view := screenOf(m)
	if !strings.Contains(view, "connection work: ") || !strings.Contains(view, "Choose a service that exists") {
		t.Errorf("the entry is not named:\n%s", view)
	}
	assertNoLocation(t, view)
}
