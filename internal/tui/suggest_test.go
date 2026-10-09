package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const suggestedOrg = "organization/aa93e8a8-2aa3-470b-b914-caad8a255dd8"

func suggestModel(t *testing.T, class provider.Class, calls *[]string, answer string, answerErr error) *Model {
	t.Helper()
	m := newTestableModel(t, func(context.Context, string) (provider.Class, error) { return class, nil }, nil)
	m.suggester = func(_ context.Context, name string) (string, error) {
		*calls = append(*calls, name)
		return answer, answerErr
	}
	return m
}

func TestSuggestedTargetPrefillsTheFormWithoutSaving(t *testing.T) {
	var calls []string
	m := suggestModel(t, provider.ClassOK, &calls, suggestedOrg, nil)
	pump(t, m, "t")

	if len(calls) != 1 || calls[0] != "wiki" {
		t.Fatalf("suggester calls = %v", calls)
	}
	if !strings.Contains(m.status, suggestedOrg) {
		t.Errorf("status = %q, want it to name the suggestion", m.status)
	}
	if got := m.cfg.Connections["wiki"].TargetValues(); len(got) != 0 {
		t.Fatalf("the suggestion was saved: %v", got)
	}
	if loaded, _, err := m.svc.Load(); err != nil || len(loaded.Connections["wiki"].TargetValues()) != 0 {
		t.Fatalf("stored targets = %v, %v", loaded.Connections["wiki"].TargetValues(), err)
	}

	openEntryForm(t, m, sectionConnections, "wiki")
	if got := targetEntries(m.fields); len(got) != 1 || got[0] != suggestedOrg {
		t.Errorf("form targets = %v, want the suggestion", got)
	}
	if !m.dirty() {
		t.Error("the prefilled form counts as unchanged")
	}
	if got := m.cfg.Connections["wiki"].TargetValues(); len(got) != 0 {
		t.Errorf("opening the form saved the suggestion: %v", got)
	}
}

func TestNoSuggestionAfterFailedTestBoundConnectionOrError(t *testing.T) {
	t.Run("failed test", func(t *testing.T) {
		var calls []string
		m := suggestModel(t, provider.ClassAuth, &calls, suggestedOrg, nil)
		pump(t, m, "t")
		if len(calls) != 0 || m.suggestedTarget("wiki") != "" {
			t.Errorf("calls = %v, suggestion = %q", calls, m.suggestedTarget("wiki"))
		}
	})
	t.Run("bound connection", func(t *testing.T) {
		var calls []string
		m := suggestModel(t, provider.ClassOK, &calls, suggestedOrg, nil)
		conn := m.cfg.Connections["wiki"]
		conn.Target = "space/main"
		m.cfg.Connections["wiki"] = conn
		pump(t, m, "t")
		if len(calls) != 0 {
			t.Errorf("suggester called for a bound connection: %v", calls)
		}
	})
	t.Run("suggester error", func(t *testing.T) {
		var calls []string
		m := suggestModel(t, provider.ClassOK, &calls, suggestedOrg, errors.New("boom"))
		pump(t, m, "t")
		openEntryForm(t, m, sectionConnections, "wiki")
		if len(calls) != 1 || len(targetEntries(m.fields)) != 0 || strings.Contains(m.status, "boom") {
			t.Errorf("calls = %v, targets = %v, status = %q", calls, targetEntries(m.fields), m.status)
		}
	})
}
