package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// dialogRows returns the first and last row of the modal frame, and the column of its left edge; the title
// in its top border tells it from the frame of the workspace behind it.
func dialogRows(view string, title string) (top, bottom, left int, ok bool) {
	top = -1
	for i, line := range strings.Split(view, "\n") {
		if at := strings.Index(line, "╭─ "+title+" "); at >= 0 {
			top, left = i, lipgloss.Width(line[:at])
		} else if at := strings.Index(line, "╰"); at >= 0 && top >= 0 && i > top && lipgloss.Width(line[:at]) == left {
			return top, i, left, true
		}
	}
	return 0, 0, 0, false
}

func TestQuestionIsCentredOverItsBackground(t *testing.T) {
	m, _, _ := newModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")
	sizedModel(m, 100, 30)
	openSectionByName(t, m, sectionServices)
	press(t, m, "d")
	if m.screen != screenConfirm {
		t.Fatalf("screen = %v, want the confirmation", m.screen)
	}

	view := m.View()
	assertViewFits(t, view, 100, 30)
	if !strings.Contains(view, `Delete "wiki"?`) || !strings.Contains(view, "y remove") {
		t.Fatalf("the question or its keys are missing:\n%s", view)
	}
	top, bottom, left, ok := dialogRows(view, "Confirm")
	if !ok {
		t.Fatalf("no modal frame found:\n%s", view)
	}
	lines := strings.Split(view, "\n")
	if above, below := top, 30-1-bottom; above-below > 1 || below-above > 1 {
		t.Errorf("modal spans rows %d-%d, not centred in 30 rows", top, bottom)
	}
	edge := lines[top][strings.Index(lines[top], "╭"):]
	width := lipgloss.Width(edge[:strings.Index(edge, "╮")+len("╮")])
	if right := 100 - left - width; right-left > 1 || left-right > 1 {
		t.Errorf("modal at column %d with width %d is not centred in 100 columns", left, width)
	}
	if !strings.Contains(lines[0], "Qatlas") {
		t.Errorf("the background header is gone:\n%s", view)
	}
	if !strings.Contains(view, "[5 Library]") {
		t.Errorf("the background tab line is gone:\n%s", view)
	}
	for _, line := range lines {
		if lipgloss.Width(line) != 100 {
			t.Fatalf("line has %d cells, want 100: %q", lipgloss.Width(line), line)
		}
	}
}

func TestModalBackgroundDoesNotChangeScrollState(t *testing.T) {
	m, _, _ := newModel(t)
	sizedModel(m, 100, 30)
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = "line"
	}
	huge := 1 << 20
	m.export = &exportState{step: exportPreview, from: screenList, lines: lines, offset: huge}
	m.export.rows.offset = huge
	m.screen, m.leaveFrom = screenLeave, screenTransfer

	view := m.View()
	if !strings.Contains(view, "warning: the export is not finished") {
		t.Fatalf("the leave question is not shown:\n%s", view)
	}
	if !strings.Contains(view, "line") {
		t.Errorf("the background export preview is not visible:\n%s", view)
	}
	if m.export.offset != huge || m.export.rows.offset != huge {
		t.Errorf("drawing the background clamped the scroll state: offset %d, rows offset %d",
			m.export.offset, m.export.rows.offset)
	}
	if m.screen != screenLeave || m.width != m.termWidth {
		t.Errorf("drawing the background left screen %v, width %d", m.screen, m.width)
	}
}

func TestQuestionTakesTheWholeWorkspaceBelowTheModalSize(t *testing.T) {
	for _, size := range []struct{ width, height int }{{40, minimumHeight}, {59, 30}, {100, 19}} {
		m, _, _ := newModel(t)
		addService(t, m, "wiki", "https://wiki.example.invalid")
		sizedModel(m, size.width, size.height)
		openSectionByName(t, m, sectionServices)
		press(t, m, "d")
		view := m.View()
		assertViewFits(t, view, size.width, size.height)
		if _, _, _, ok := dialogRows(view, "Confirm"); ok {
			t.Errorf("%dx%d shows a modal:\n%s", size.width, size.height, view)
		}
		if !strings.Contains(view, `Delete "wiki"?`) || !strings.Contains(view, "y remove") {
			t.Errorf("%dx%d lacks the question:\n%s", size.width, size.height, view)
		}
	}
}

func TestBaseScreenOfEveryDialog(t *testing.T) {
	m, _, _ := newModel(t)
	cases := []struct {
		name  string
		setup func()
		want  screen
	}{
		{"delete", func() { m.screen = screenConfirm }, screenList},
		{"remove role", func() { m.screen, m.confirmRole = screenConfirm, "key" }, screenForm},
		{"secret", func() { m.screen = screenSecret }, screenForm},
		{"vault offer", func() { m.screen = screenVaultOffer }, screenForm},
		{"leave", func() { m.screen, m.leaveFrom = screenLeave, screenPicker }, screenPicker},
		{"lock over leave", func() {
			m.screen, m.vaultLockConfirm, m.vaultLockBack = screenConfirm, true, screenLeave
			m.leaveFrom = screenSummary
		}, screenSummary},
		{"update", func() { m.screen, m.updateFrom = screenUpdate, screenNav }, screenNav},
		{"restart", func() { m.screen, m.restartBack = screenRestart, screenForm }, screenForm},
	}
	for _, tc := range cases {
		*m = *func() *Model { n, _, _ := newModel(t); return n }()
		tc.setup()
		if got := m.baseScreen(); got != tc.want {
			t.Errorf("%s: baseScreen = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestOverlayKeepsTheBackgroundReadableWithoutColour(t *testing.T) {
	out := overlay("aaaaaaaaaa\nbbbbbbbbbb\ncccccccccc", []string{"[X]"}, 10, 3)
	rows := strings.Split(ansi.Strip(out), "\n")
	if rows[0] != "aaaaaaaaaa" || rows[1] != "bbb[X]bbbb" || rows[2] != "cccccccccc" {
		t.Errorf("overlay = %q", rows)
	}
}

// Every question reads question, one sentence on what follows, details, and the keys as its last line,
// in the modal and in the full-screen fallback alike.
func TestQuestionsReadQuestionConsequenceKeys(t *testing.T) {
	over := func(set func(m *Model)) func(t *testing.T) *Model {
		return func(t *testing.T) *Model {
			m, _ := startServiceForm(t)
			m.screen = screenConfirm
			set(m)
			return m
		}
	}
	cases := []struct {
		name, title, consequence, key string
		open                          func(t *testing.T) *Model
	}{
		{"delete", `Delete "wiki"?`, "it is removed from the configuration", "y remove", func(t *testing.T) *Model {
			m, _, _ := newModel(t)
			addService(t, m, "wiki", "https://wiki.example.invalid")
			openSectionByName(t, m, sectionServices)
			press(t, m, "d")
			return m
		}},
		{"remove role", "Remove the stored secret for", "it is removed from", "y remove", over(func(m *Model) {
			m.confirmRole, m.editing = "key", "reader"
		})},
		{"encryption off", "Turn the vault's encryption off?", "warning: every secret it holds", "y turn off",
			over(func(m *Model) { m.decryptConfirm = true })},
		{"lock", "Lock the vault now?", "running invokes stop working", "y lock",
			over(func(m *Model) { m.vaultLockConfirm, m.vaultLockBack = true, screenList })},
		{"migrate", "Migrate credentials.yaml?", "0 entries would move into the vault", "y migrate",
			over(func(m *Model) { m.migratePlanConfirm, m.migrate = true, &migratePlan{} })},
		{"delete credentials.yaml", "Delete credentials.yaml?", "2 entries were migrated", "y delete",
			over(func(m *Model) { m.migrateDeleteConfirm, m.migrateResult = true, "2 entries were migrated" })},
		{"approve all", "Approve 0 connection(s)?", "every connection currently open is approved", "y approve all",
			over(func(m *Model) { m.approveAllConfirm = true })},
		{"revoke", "Revoke agent token agent?", "it stops working at once", "y revoke",
			over(func(m *Model) { m.tokenRevoke = "agent" })},
		{"profile", "Replace the access and tool ticks with profile", "every tick stays changeable", "y replace",
			over(func(m *Model) { m.pendingProfile = "custom-profile" })},
		{"leave", "Leave New service without saving?", "Leaving for the Services list would lose them.", "F2 save",
			func(t *testing.T) *Model {
				m, _ := startServiceForm(t)
				press(t, m, "esc")
				return m
			}},
		{"leave export", "Leave the export?", "Leaving drops the ticks", "F2 write", func(t *testing.T) *Model {
			m, _, _ := newModel(t)
			m.export = &exportState{step: exportPreview, from: screenList}
			m.screen, m.leaveFrom = screenLeave, screenTransfer
			return m
		}},
		{"update", "Update qatlas to v2.0.0?", "Installs the release", "y update", func(t *testing.T) *Model {
			m, _, _ := newModel(t)
			m.release.Latest, m.updateText = "v2.0.0", "v1.0.0 to v2.0.0"
			m.screen, m.updateFrom = screenUpdate, screenList
			return m
		}},
		{"rename", "Rename service wiki to handbook?", "Dry run: nothing is changed", "y rename", func(t *testing.T) *Model {
			m, _, _ := newModel(t)
			seedConnections(t, m, "docs")
			startRename(t, m, sectionServices, "wiki", "handbook")
			pump(t, m, "enter")
			return m
		}},
		{"restart", "Restart qatlas tui into v2.0.0?", "Restarting replaces this editor", "y restart", func(t *testing.T) *Model {
			m, _, _ := newModel(t)
			m.updated = "v2.0.0"
			m.screen, m.restartBack = screenRestart, screenList
			return m
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.open(t)
			lines := strings.Split(strings.TrimSpace(m.questionView()), "\n")
			if !strings.HasPrefix(strings.TrimSpace(lines[0]), tc.title) ||
				!strings.HasSuffix(strings.TrimSpace(lines[0]), "?") && !strings.Contains(lines[0], "secret for") {
				t.Errorf("first line is not the question %q: %q", tc.title, lines[0])
			}
			if !strings.Contains(m.questionView(), tc.consequence) {
				t.Errorf("consequence %q missing:\n%s", tc.consequence, m.questionView())
			}
			if footer := m.footerLine(); !strings.Contains(footer, "help") {
				t.Errorf("the footer is not the key line: %q", footer)
			}
			for name, size := range map[string][2]int{"modal": {100, 30}, "fallback": {50, 30}} {
				sizedModel(m, size[0], size[1])
				view := ansi.Strip(m.View())
				rows := strings.Split(view, "\n")
				find := func(needle string, from int) int {
					for i := max(from, 0); i < len(rows); i++ {
						if strings.Contains(rows[i], needle) {
							return i
						}
					}
					return -1
				}
				// The question is searched from its own row on: the background has key lines too.
				q := find(tc.title, 0)
				c := find(tc.consequence[:min(len(tc.consequence), 16)], q)
				k := find(tc.key, c)
				if q < 0 || c < 0 || k < 0 || q >= c || c >= k {
					t.Errorf("%s: question row %d, consequence row %d, keys row %d, want ascending:\n%s",
						name, q, c, k, view)
				}
				if _, _, _, boxed := dialogRows(view, "Confirm"); name == "modal" && !boxed {
					if _, _, _, boxed = dialogRows(view, "Unsaved changes"); !boxed {
						t.Errorf("%s: no modal frame:\n%s", name, view)
					}
				}
			}
		})
	}
}

// A long dry run does not fit a modal: it takes the whole workspace and scrolls there, with up/down moving
// the same offset as before.
func TestLongRenamePlanFallsBackToTheScrollingWorkspace(t *testing.T) {
	m, _, _ := newModel(t)
	seedConnections(t, m, "docs")
	sizedModel(m, 100, 30)
	startRename(t, m, sectionServices, "wiki", "handbook")
	pump(t, m, "enter")
	if view := m.View(); !strings.Contains(view, "╭─ Confirm ") {
		t.Fatalf("a short plan is not a modal:\n%s", view)
	}

	for i := 0; i < 60; i++ {
		m.rename.plan.References = append(m.rename.plan.References, fmt.Sprintf("connections.c%02d.service", i))
	}
	view := m.View()
	assertViewFits(t, view, 100, 30)
	if strings.Contains(view, "╭─ Confirm ") {
		t.Fatalf("a plan taller than the screen is a modal:\n%s", view)
	}
	if !strings.Contains(view, "connections.c00.service") || strings.Contains(view, "connections.c59.service") {
		t.Fatalf("the full screen does not start at the first line:\n%s", view)
	}
	for _, keys := range []string{"y rename", "n/esc cancel", "up/down scroll"} {
		if !strings.Contains(view, keys) {
			t.Errorf("the key line lacks %q:\n%s", keys, view)
		}
	}
	press(t, m, "end")
	view = m.View()
	if !strings.Contains(view, "connections.c59.service") || strings.Contains(view, "connections.c00.service") {
		t.Errorf("end did not scroll to the last line:\n%s", view)
	}
	end := m.rename.offset
	press(t, m, "up")
	if m.rename.offset != end-1 {
		t.Errorf("up moved the offset from %d to %d", end, m.rename.offset)
	}
	press(t, m, "home")
	if m.rename.offset != 0 || !strings.Contains(m.View(), "connections.c00.service") {
		t.Errorf("home did not return to the first line, offset %d", m.rename.offset)
	}
}
