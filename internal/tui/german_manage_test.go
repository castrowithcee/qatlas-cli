package tui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/selftest"
)

// logsGermanScreens are the Logs section with its table, bar, dialogs, summaries, and the heatmap.
func logsGermanScreens() []germanScreen {
	filled := func(t *testing.T) *logFixture {
		f := newLogFixture(t)
		f.append("mcp", "codex", "wiki.pages.get", "wiki", "success")
		f.append("mcp", "claude-code", "wiki.pages.create", "wiki", "policy-denied")
		f.append("cli", "", "chat.send", "alerts", "success")
		return f
	}
	sessions := func(t *testing.T) *Model {
		f := newLogFixture(t)
		f.appendSession(sessionNew, "wiki.pages.get", "success", 120, 480)
		f.appendSession(sessionNew, "wiki.pages.list", "success", 80, 900)
		m := f.model(true, f.localToday())
		pump(t, m, "M")
		return m
	}
	return []germanScreen{
		{"logs table", func(t *testing.T) *Model { f := filled(t); return f.model(true, f.localToday()) }},
		{"logs locked", func(t *testing.T) *Model { f := filled(t); return f.model(false, f.localToday()) }},
		{"logs empty day", func(t *testing.T) *Model { return filled(t).model(true, "2021-05-05") }},
		{"logs filters", func(t *testing.T) *Model {
			f := filled(t)
			m := f.model(true, f.localToday())
			pump(t, m, "f")
			return m
		}},
		{"logs filter values", func(t *testing.T) *Model {
			f := filled(t)
			m := f.model(true, f.localToday())
			pump(t, m, "f")
			typeText(t, m, "client")
			pump(t, m, "enter")
			return m
		}},
		{"logs search", func(t *testing.T) *Model {
			f := filled(t)
			m := f.model(true, f.localToday())
			pump(t, m, "/")
			typeText(t, m, "denied")
			return m
		}},
		{"logs nothing matches", func(t *testing.T) *Model {
			f := filled(t)
			m := f.model(true, f.localToday())
			pump(t, m, "/")
			typeText(t, m, "no-such-entry")
			pump(t, m, "enter")
			return m
		}},
		{"logs day dialog", func(t *testing.T) *Model {
			f := filled(t)
			m := f.model(true, f.localToday())
			m.logs.focus = logFocusDate
			pump(t, m, "enter")
			typeText(t, m, "x")
			pump(t, m, "enter")
			return m
		}},
		{"logs range dialog", func(t *testing.T) *Model {
			f := filled(t)
			m := f.model(true, f.localToday())
			m.logs.mode = logModeRange
			m.logs.focus = logFocusDate
			pump(t, m, "enter")
			return m
		}},
		{"logs detail", func(t *testing.T) *Model {
			f := filled(t)
			m := f.model(true, f.localToday())
			pump(t, m, "enter")
			return m
		}},
		{"logs altered detail", func(t *testing.T) *Model {
			f := newLogFixture(t)
			for _, op := range []string{"op.one", "op.two", "op.three", "op.four", "op.five"} {
				f.append("cli", "", op, "wiki", "success")
			}
			f.edit(f.today(), func(lines [][]byte) [][]byte {
				lines[1] = bytes.Replace(lines[1], []byte(`"op.two"`), []byte(`"op.hid"`), 1)
				return append(append(lines[:3], lines[4]), []byte(`{"seq": not json`))
			})
			m := f.model(true, f.localToday())
			pump(t, m, "down", "enter")
			return m
		}},
		{"logs altered table", func(t *testing.T) *Model {
			f := newLogFixture(t)
			for _, op := range []string{"op.one", "op.two", "op.three", "op.four", "op.five"} {
				f.append("cli", "", op, "wiki", "success")
			}
			f.edit(f.today(), func(lines [][]byte) [][]byte {
				lines[1] = bytes.Replace(lines[1], []byte(`"op.two"`), []byte(`"op.hid"`), 1)
				return append(append(lines[:3], lines[4]), []byte(`{"seq": not json`))
			})
			return f.model(true, f.localToday())
		}},
		{"logs summary", func(t *testing.T) *Model {
			f := filled(t)
			m := f.model(true, f.localToday())
			pump(t, m, "s")
			return m
		}},
		{"logs sessions", sessions},
		{"logs without sessions", func(t *testing.T) *Model {
			f := newLogFixture(t)
			f.append("cli", "", "cli.only", "wiki", "success")
			m := f.model(true, f.localToday())
			pump(t, m, "M")
			return m
		}},
		{"logs heatmap", func(t *testing.T) *Model { return heatModel(t, activityLog(t)) }},
	}
}

// transferGermanScreens are the questions, previews, and results of the export and the import.
func transferGermanScreens() []germanScreen {
	plainExport := func(t *testing.T) (*exportViews, string) {
		v, path := plainExportModel(t)
		v.startExport("")
		return v, path
	}
	vaultExport := func(t *testing.T) (*exportViews, string) {
		v, path := vaultExportModel(t)
		v.startExport(testExportVaultPass)
		return v, path
	}
	plainImport := func(t *testing.T, kind exportKind) (*exportViews, string) {
		path := importFixture(t, kind)
		v := plainImportTarget(t)
		v.startImport("")
		return v, path
	}
	vaultImport := func(t *testing.T) (*exportViews, string) {
		path := importFixture(t, exportEncrypted)
		v := vaultImportTarget(t)
		v.startImport(testExportVaultPass)
		return v, path
	}
	return []germanScreen{
		{"export select", func(t *testing.T) *Model { v, _ := plainExport(t); return v.m }},
		{"export all ticked", func(t *testing.T) *Model { v, _ := plainExport(t); v.pump(" "); return v.m }},
		{"export tick first", func(t *testing.T) *Model { v, _ := plainExport(t); v.pump("enter"); return v.m }},
		{"export mode", func(t *testing.T) *Model { v, _ := plainExport(t); v.pump(" ", "enter"); return v.m }},
		{"export mode without vault", func(t *testing.T) *Model {
			v, _ := plainExport(t)
			v.pump(" ", "enter", "down", "enter")
			return v.m
		}},
		{"export file", func(t *testing.T) *Model {
			v, path := plainExport(t)
			v.advance(exportTarget, exportWithout, path)
			return v.m
		}},
		{"export preview", func(t *testing.T) *Model {
			v, path := plainExport(t)
			v.advance(exportPreview, exportWithout, path)
			return v.m
		}},
		{"export result", func(t *testing.T) *Model {
			v, path := plainExport(t)
			v.advance(exportPreview, exportWithout, path)
			v.pump("f2")
			return v.m
		}},
		{"export file exists", func(t *testing.T) *Model {
			v, path := plainExport(t)
			v.advance(exportPreview, exportWithout, path)
			mustNoError(t, os.WriteFile(path, []byte("x"), 0o600))
			v.pump("f2")
			return v.m
		}},
		{"export leave question", func(t *testing.T) *Model {
			v, _ := plainExport(t)
			v.pump(" ", "esc")
			return v.m
		}},
		{"export passphrase", func(t *testing.T) *Model {
			v, _ := vaultExport(t)
			v.pump(" ", "enter", "down", "enter")
			return v.m
		}},
		{"export passphrase confirmation", func(t *testing.T) *Model {
			v, _ := vaultExport(t)
			v.pump(" ", "enter", "down", "enter")
			v.type_(testExportPass)
			v.pump("enter")
			return v.m
		}},
		{"export passphrase mismatch", func(t *testing.T) *Model {
			v, _ := vaultExport(t)
			v.pump(" ", "enter", "down", "enter")
			v.type_(testExportPass)
			v.pump("enter")
			v.type_("something else")
			v.pump("enter")
			return v.m
		}},
		{"export plain question", func(t *testing.T) *Model {
			v, _ := vaultExport(t)
			v.pump(" ", "enter", "down", "down", "enter")
			return v.m
		}},
		{"export encrypted preview", func(t *testing.T) *Model {
			v, path := vaultExport(t)
			v.advance(exportPreview, exportEncrypted, path)
			return v.m
		}},
		{"export encrypted result", func(t *testing.T) *Model {
			v, path := vaultExport(t)
			v.advance(exportPreview, exportEncrypted, path)
			v.pump("f2")
			return v.m
		}},
		{"export plain preview", func(t *testing.T) *Model {
			v, path := vaultExport(t)
			v.advance(exportPreview, exportUnencrypted, path)
			return v.m
		}},
		{"export plain result", func(t *testing.T) *Model {
			v, path := vaultExport(t)
			v.advance(exportPreview, exportUnencrypted, path)
			v.pump("f2")
			return v.m
		}},
		{"import file", func(t *testing.T) *Model { v, _ := plainImport(t, exportWithout); return v.m }},
		{"import file missing", func(t *testing.T) *Model {
			v, path := plainImport(t, exportWithout)
			v.typePath(filepath.Join(filepath.Dir(path), "missing.yaml"))
			v.pump("enter")
			return v.m
		}},
		{"import file leave question", func(t *testing.T) *Model {
			v, path := plainImport(t, exportWithout)
			v.typePath(path)
			v.pump("esc")
			return v.m
		}},
		{"import mode", func(t *testing.T) *Model {
			v, path := plainImport(t, exportWithout)
			v.importTo(importMode, path, false, 0)
			return v.m
		}},
		{"import preview", func(t *testing.T) *Model {
			v, path := plainImport(t, exportWithout)
			v.importTo(importPreview, path, false, 0)
			return v.m
		}},
		{"import preview overwrite", func(t *testing.T) *Model {
			path := importFixture(t, exportWithout)
			v := plainImportTarget(t)
			seedTarget(t, v.m)
			v.startImport("")
			v.importTo(importPreview, path, false, 2)
			return v.m
		}},
		{"import preview merge", func(t *testing.T) *Model {
			path := importFixture(t, exportWithout)
			v := plainImportTarget(t)
			seedTarget(t, v.m)
			v.startImport("")
			v.importTo(importPreview, path, false, 1)
			return v.m
		}},
		{"import result", func(t *testing.T) *Model {
			v, path := plainImport(t, exportWithout)
			v.importTo(importPreview, path, false, 0)
			v.pump("f2")
			return v.m
		}},
		{"import conflict", func(t *testing.T) *Model {
			v, path := plainImport(t, exportWithout)
			v.importTo(importPreview, path, false, 0)
			other := newTestConfig(t)
			mustNoError(t, other.SetService("other", config.Service{Provider: "bookstack", BaseURL: "https://other.example.invalid"}))
			mustNoError(t, v.m.store.Save(other))
			v.pump("f2")
			return v.m
		}},
		{"import passphrase", func(t *testing.T) *Model {
			v, path := plainImport(t, exportEncrypted)
			v.typePath(path)
			v.pump("enter")
			return v.m
		}},
		{"import wrong passphrase", func(t *testing.T) *Model {
			v, path := plainImport(t, exportEncrypted)
			v.typePath(path)
			v.pump("enter")
			v.type_(testImportWrongPass)
			v.pump("enter")
			return v.m
		}},
		{"import encrypted preview", func(t *testing.T) *Model {
			v, path := plainImport(t, exportEncrypted)
			v.importTo(importPreview, path, true, 0)
			return v.m
		}},
		{"import encrypted result", func(t *testing.T) *Model {
			v, path := plainImport(t, exportEncrypted)
			v.importTo(importPreview, path, true, 0)
			v.pump("f2")
			return v.m
		}},
		{"import plain secrets preview", func(t *testing.T) *Model {
			v, path := plainImport(t, exportUnencrypted)
			v.importTo(importPreview, path, false, 0)
			return v.m
		}},
		{"import delete question", func(t *testing.T) *Model {
			v, path := plainImport(t, exportUnencrypted)
			v.importTo(importPreview, path, false, 0)
			v.pump("f2", "enter")
			return v.m
		}},
		{"import file kept", func(t *testing.T) *Model {
			v, path := plainImport(t, exportUnencrypted)
			v.importTo(importPreview, path, false, 0)
			v.pump("f2", "enter", "n")
			return v.m
		}},
		{"import file deleted", func(t *testing.T) *Model {
			v, path := plainImport(t, exportUnencrypted)
			v.importTo(importPreview, path, false, 0)
			v.pump("f2", "enter", "y")
			return v.m
		}},
		{"import approve question", func(t *testing.T) *Model {
			v, path := vaultImport(t)
			v.importTo(importPreview, path, true, 0)
			v.pump("f2", "enter")
			return v.m
		}},
		{"import approved", func(t *testing.T) *Model {
			v, path := vaultImport(t)
			v.importTo(importPreview, path, true, 0)
			v.pump("f2", "enter", "y")
			return v.m
		}},
		{"import left open", func(t *testing.T) *Model {
			v, path := vaultImport(t)
			v.importTo(importPreview, path, true, 0)
			v.pump("f2", "enter", "n")
			return v.m
		}},
		{"import without vault approval", func(t *testing.T) *Model {
			v, path := plainImport(t, exportWithout)
			v.importTo(importPreview, path, false, 0)
			v.pump("f2")
			return v.m
		}},
	}
}

// updateGermanScreens are the update and restart questions and messages.
func updateGermanScreens() []germanScreen {
	return []germanScreen{
		{"update banner", func(t *testing.T) *Model { return startedWith(t, newerRelease()) }},
		{"update question", func(t *testing.T) *Model {
			m := startedWith(t, newerRelease())
			pump(t, m, "U")
			return m
		}},
		{"update waits", func(t *testing.T) *Model {
			m := startedWith(t, newerRelease())
			m.vaultBusy = true
			pump(t, m, "U")
			return m
		}},
		{"update waits after the question", func(t *testing.T) *Model {
			m := startedWith(t, newerRelease())
			pump(t, m, "U")
			m.vaultBusy = true
			pump(t, m, "y")
			return m
		}},
		{"update declined", func(t *testing.T) *Model {
			m := startedWith(t, newerRelease())
			pump(t, m, "U", "n")
			return m
		}},
		{"update installed", func(t *testing.T) *Model {
			m := startedWith(t, newerRelease())
			pump(t, m, "U", "y")
			return m
		}},
		{"update failed", func(t *testing.T) *Model {
			fake := newerRelease()
			fake.updateErr = errors.New("download failed")
			m := startedWith(t, fake)
			pump(t, m, "U", "y")
			return m
		}},
		{"update already current", func(t *testing.T) *Model {
			fake := newerRelease()
			fake.update.Updated = false
			m := startedWith(t, fake)
			pump(t, m, "U", "y")
			return m
		}},
		{"restart question", func(t *testing.T) *Model {
			m := startedWith(t, newerRelease())
			m.updated, m.restartBack, m.screen = "v0.5.0", screenNav, screenRestart
			return m
		}},
		{"restart adopted", func(t *testing.T) *Model {
			m, _, _ := newModel(t)
			m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
			m.adopt(&handoff{Version: handoffVersion, From: "v0.4.0", To: "v0.5.0"})
			return m
		}},
		{"restart adopted with unlock", func(t *testing.T) *Model {
			m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
			m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
			m.adopt(&handoff{Version: handoffVersion, From: "v0.4.0", To: "v0.5.0", Unlocked: true})
			return m
		}},
	}
}

// selftestGermanScreens are a selftest while it runs, while it is ended, and after it.
func selftestGermanScreens() []germanScreen {
	start := func(t *testing.T, run func(context.Context, selftest.Options) (*selftest.Report, error)) (*Model, tea.Cmd) {
		t.Setenv("TMPDIR", t.TempDir())
		m := cmdModel(t)
		m.selftest.scenarios = fakeScenarios("alpha")
		m.selftest.Version = "v9.9.9"
		m.selftest.run = run
		cmd := startSelftestCommand(t, m, "selftest alpha")
		return m, cmd
	}
	finished := func(t *testing.T) *Model {
		m, cmd := start(t, func(_ context.Context, o selftest.Options) (*selftest.Report, error) {
			return fakeReport(o.Scenario), nil
		})
		drainSelftest(m, cmd)
		return m
	}
	return []germanScreen{
		{"selftest running", func(t *testing.T) *Model {
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			m, cmd := start(t, func(_ context.Context, o selftest.Options) (*selftest.Report, error) {
				o.Progress("step one")
				<-release
				return fakeReport(o.Scenario), nil
			})
			m.Update(cmd())
			return m
		}},
		{"selftest ending", func(t *testing.T) *Model {
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			m, cmd := start(t, func(_ context.Context, o selftest.Options) (*selftest.Report, error) {
				o.Progress("step one")
				<-release
				return nil, context.Canceled
			})
			m.Update(cmd())
			pump(t, m, "esc")
			return m
		}},
		{"selftest result", finished},
		{"selftest saved", func(t *testing.T) *Model { m := finished(t); pump(t, m, "W"); return m }},
		{"selftest error", func(t *testing.T) *Model {
			m, cmd := start(t, func(context.Context, selftest.Options) (*selftest.Report, error) {
				return nil, errors.New("the worker did not start")
			})
			drainSelftest(m, cmd)
			return m
		}},
		{"selftest cancelled", func(t *testing.T) *Model {
			release := make(chan struct{})
			m, cmd := start(t, func(_ context.Context, o selftest.Options) (*selftest.Report, error) {
				o.Progress("step one")
				<-release
				return nil, context.Canceled
			})
			m.Update(cmd())
			pump(t, m, "esc")
			close(release)
			for m.st != nil && !m.st.done {
				_, cmd = m.Update(nextSelftestEvent(m.st)())
			}
			return m
		}},
	}
}
