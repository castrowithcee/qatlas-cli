package tui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/selftest"
	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// The visual snapshots render the main screens of the editor from a fixed demo setup and compare the raw
// terminal output byte for byte with the files under testdata/visual. Run with -update to rewrite them, and
// with QATLAS_VISUAL_HTML=<dir> to also write one HTML page per snapshot for looking at them.
var updateVisual = flag.Bool("update", false, "rewrite the visual snapshots under testdata/visual")

const (
	visualDir        = "testdata/visual"
	visualPassphrase = "correct horse battery staple"
)

// visualNow is the clock of every snapshot: a Wednesday at noon UTC.
var visualNow = time.Date(2026, time.March, 18, 12, 0, 0, 0, time.UTC)

// visualCall is one line of testdata/visual/invokes.json.
type visualCall struct {
	DaysAgo    int    `json:"days_ago"`
	Time       string `json:"time"`
	Path       string `json:"path"`
	Operation  string `json:"operation"`
	Connection string `json:"connection"`
	Effect     string `json:"effect"`
	Result     string `json:"result"`
	DurationMS int64  `json:"duration_ms"`
}

// writeVisualLog writes the demo invokes as an unsigned log with an intact hash chain in the vault below dir.
func writeVisualLog(t *testing.T, fixtures, dir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtures, "invokes.json"))
	mustNoError(t, err)
	var calls []visualCall
	mustNoError(t, json.Unmarshal(raw, &calls))
	logs := filepath.Join(dir, vault.DirName, "logs")
	mustNoError(t, os.MkdirAll(logs, 0o700))
	previous := sha256.Sum256(nil)
	for i, c := range calls {
		clock, err := time.Parse("15:04", c.Time)
		mustNoError(t, err)
		day := visualNow.AddDate(0, 0, -c.DaysAgo)
		when := time.Date(day.Year(), day.Month(), day.Day(), clock.Hour(), clock.Minute(), 0, 0, time.UTC)
		entry := invokelog.Entry{Seq: uint64(i + 1), Time: when, Path: c.Path, Operation: c.Operation,
			Connection: c.Connection, Effect: c.Effect, Result: c.Result, DurationMS: c.DurationMS,
			PrevHash: hex.EncodeToString(previous[:])}
		line, err := json.Marshal(entry)
		mustNoError(t, err)
		name := filepath.Join(logs, when.Format(logDateLayout)+".jsonl")
		file, err := os.OpenFile(name, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		mustNoError(t, err)
		_, err = file.Write(append(line, '\n'))
		mustNoError(t, err)
		mustNoError(t, file.Close())
		previous = sha256.Sum256(line)
	}
}

// visualSetup fixes everything a snapshot depends on besides the model: colour profile, language, local
// time zone, working directory and the environment. The configuration lives at the relative path
// config.yaml of a temporary working directory, so the header shows the same path on every machine.
func visualSetup(t *testing.T, noColor bool) {
	t.Helper()
	restoreRenderer(t)
	lang, local := uiLang, time.Local
	t.Cleanup(func() { uiLang, time.Local = lang, local })
	setLanguage(uiprefs.LanguageEnglish)
	time.Local = time.UTC
	setProfile(profileTrueColor)
	termFg, termBg = nil, nil
	// Icons and block characters depend on the locale; the snapshots must not depend on the machine.
	t.Setenv("LC_ALL", "C.UTF-8")
	t.Setenv("TERM", "xterm-256color")
	if noColor {
		t.Setenv("NO_COLOR", "1")
	} else {
		t.Setenv("NO_COLOR", "")
	}
	t.Chdir(t.TempDir())
}

// visualRoles are the secret roles every demo credential holds in the vault.
var visualRoles = map[string][]string{
	"wiki-reader": {"token-id", "token-secret"}, "code-reader": {"token"}, "cloud-user": {"user-id", "app-password"},
	"tables-token": {"api-token"}, "crm-key": {"api-key"}, "tasks-token": {"token"}, "alerts-bot": {"bot-token"},
}

// visualModel builds the demo editor in the current working directory: an encrypted, unlocked vault holding
// a placeholder secret for every credential of testdata/visual/config.yaml, and the demo invoke log.
func visualModel(t *testing.T, fixtures string, width, height int, theme string) *Model {
	t.Helper()
	cfg, err := os.ReadFile(filepath.Join(fixtures, "config.yaml"))
	mustNoError(t, err)
	mustNoError(t, os.WriteFile("config.yaml", cfg, 0o600))
	offer := func(string) (string, error) { return visualPassphrase, nil }
	secrets, _ := newVaultResolver(t, ".")
	for credential, roles := range visualRoles {
		for _, role := range roles {
			mustNoError(t, secrets.SetVault(credential, role, "demo-"+credential+"-"+role, offer))
		}
	}
	writeVisualLog(t, fixtures, ".")

	store := config.NewStore("config.yaml", providerRegistry(t, 8))
	m, err := New(testService(store, secrets, nil), store, nil, secrets, nil)
	mustNoError(t, err)
	m.version = "1.2.3"
	m.now = func() time.Time { return visualNow }
	m.logs = newLogView(visualNow)
	m.prefs = uiprefs.Default()
	m.prefs.Theme = theme
	applyTheme(theme)
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m.section = sectionOverview
	m.screen = screenNav
	deliver(m, m.loadOverviewActivity())
	return m
}

// visualShot is one snapshot: the terminal size and preferences it is rendered under, and the keys that
// bring the editor to the screen.
type visualShot struct {
	name          string
	width, height int
	theme         string
	noColor       bool
	show          func(t *testing.T, m *Model)
}

// activityHeatmap opens the Activity tab over every recorded day.
func activityHeatmap(t *testing.T, m *Model) {
	pump(t, m, "3")
	for m.logs.mode != logModeAll {
		m.logs.focus = logFocusMode
		pump(t, m, "right")
	}
	m.logs.focus = logFocusTabs
	pump(t, m, "right")
}

func visualShots() []visualShot {
	dark, light := uiprefs.ThemeQatlasDark, uiprefs.ThemeQatlasLight
	overview := func(*testing.T, *Model) {}
	connections := func(t *testing.T, m *Model) { pump(t, m, "2") }
	activityTable := func(t *testing.T, m *Model) {
		pump(t, m, "3")
		for m.logs.mode != logModeAll {
			m.logs.focus = logFocusMode
			pump(t, m, "right")
		}
	}
	activityDetails := func(t *testing.T, m *Model) { activityTable(t, m); pump(t, m, "s") }
	return []visualShot{
		{"01-overview", 150, 42, dark, false, overview},
		{"02-connections", 150, 42, dark, false, connections},
		{"03-connection-form", 150, 42, dark, false, func(t *testing.T, m *Model) {
			connections(t, m)
			pump(t, m, "enter")
		}},
		{"04-connection-delete", 150, 42, dark, false, func(t *testing.T, m *Model) {
			connections(t, m)
			pump(t, m, "d")
		}},
		{"05-activity-table", 150, 42, dark, false, func(t *testing.T, m *Model) {
			pump(t, m, "3")
			for m.logs.mode != logModeAll {
				m.logs.focus = logFocusMode
				pump(t, m, "right")
			}
		}},
		{"06-activity-heatmap", 150, 42, dark, false, activityHeatmap},
		{"07-palette", 150, 42, dark, false, func(t *testing.T, m *Model) { press(t, m, ":") }},
		{"08-library", 150, 42, dark, false, func(t *testing.T, m *Model) { openSectionByName(t, m, sectionServices) }},
		{"09-access", 150, 42, dark, false, func(t *testing.T, m *Model) { openSectionByName(t, m, sectionTokens) }},
		{"10-settings", 150, 42, dark, false, func(t *testing.T, m *Model) { openSectionByName(t, m, sectionGeneral) }},
		{"11-overview", 80, 24, dark, false, overview},
		{"12-connections", 80, 24, dark, false, connections},
		{"13-overview-light", 150, 42, light, false, overview},
		{"14-overview-qatlas-background", 150, 42, dark, false, overview},
		{"15-overview-no-color", 150, 42, dark, true, overview},
		{"16-connection-form-compact", 80, 24, dark, false, func(t *testing.T, m *Model) {
			connections(t, m)
			pump(t, m, "enter")
		}},
		{"17-service-form", 150, 42, dark, false, func(t *testing.T, m *Model) {
			openSectionByName(t, m, sectionServices)
			m.openForm("")
		}},
		{"18-credential-form", 150, 42, dark, false, func(t *testing.T, m *Model) {
			openSectionByName(t, m, sectionCredentials)
			m.openForm("")
		}},
		{"19-token-form", 150, 42, dark, false, func(t *testing.T, m *Model) {
			openSectionByName(t, m, sectionTokens)
			m.openForm("")
		}},
		{"20-setup-step", 150, 42, dark, false, func(t *testing.T, m *Model) {
			connections(t, m)
			m.startSetup()
			pump(t, m, "enter")
		}},
		{"21-connection-form-no-color", 150, 42, dark, true, func(t *testing.T, m *Model) {
			connections(t, m)
			pump(t, m, "enter")
		}},
		{"22-connection-details", 150, 42, dark, false, func(t *testing.T, m *Model) {
			connections(t, m)
			pump(t, m, "down")
			pump(t, m, "down")
			pump(t, m, "tab")
			pump(t, m, "right")
		}},
		{"23-connection-raw", 150, 42, dark, false, func(t *testing.T, m *Model) {
			connections(t, m)
			pump(t, m, "down")
			pump(t, m, "down")
			pump(t, m, "tab")
			pump(t, m, "right")
			pump(t, m, "right")
		}},
		{"30-activity-details", 150, 42, dark, false, activityDetails},
		{"31-activity-table-no-color", 150, 42, dark, true, activityTable},
		{"32-activity-heatmap-no-color", 150, 42, dark, true, activityHeatmap},
		{"33-activity-table", 80, 24, dark, false, activityTable},
		{"34-activity-heatmap", 80, 24, dark, false, activityHeatmap},
		{"35-activity-empty", 150, 42, dark, false, func(t *testing.T, m *Model) { pump(t, m, "3") }},
		{"50-provider-picker", 150, 42, dark, false, func(t *testing.T, m *Model) {
			openSectionByName(t, m, sectionConnections)
			m.openForm("")
			focusField(t, m, providerLabel)
			pump(t, m, "enter")
			pump(t, m, "down", "down")
		}},
		{"51-target-picker", 150, 42, dark, false, visualTargetPicker},
		{"52-tool-picker", 150, 42, dark, false, func(t *testing.T, m *Model) {
			openEntryForm(t, m, sectionConnections, "wiki-main")
			focusField(t, m, accessLabel)
			selectChoice(t, m, accessTools)
			focusField(t, m, toolListLabel)
			pump(t, m, "enter")
			pump(t, m, " ", "down", "down", "down", "down", "down", "right", "down", " ", "down", " ")
		}},
		{"53-setup-summary", 150, 42, dark, false, visualSetupSummary},
		{"54-template-picker", 150, 42, dark, false, func(t *testing.T, m *Model) {
			openSectionByName(t, m, sectionConnections)
			m.askSetupTemplate()
			pump(t, m, "down", "enter")
		}},
		{"24-connection-details-no-color", 150, 42, dark, true, func(t *testing.T, m *Model) {
			connections(t, m)
			pump(t, m, "down")
			pump(t, m, "down")
			pump(t, m, "tab")
			pump(t, m, "right")
		}},
		{"40-tokens", 150, 42, dark, false, func(t *testing.T, m *Model) { visualTokens(t, m) }},
		{"41-approvals", 150, 42, dark, false, func(t *testing.T, m *Model) {
			openSectionByName(t, m, sectionApprovals)
		}},
		{"42-approval-detail", 150, 42, dark, false, func(t *testing.T, m *Model) {
			mustNoError(t, os.Chtimes("config.yaml", visualNow, visualNow))
			openSectionByName(t, m, sectionApprovals)
			pump(t, m, "enter")
		}},
		{"44-library-credentials", 150, 42, dark, false, func(t *testing.T, m *Model) {
			openSectionByName(t, m, sectionCredentials)
		}},
		{"45-settings-vault", 150, 42, dark, false, func(t *testing.T, m *Model) { openSectionByName(t, m, sectionVault) }},
		{"46-help", 150, 42, dark, false, func(t *testing.T, m *Model) {
			connections(t, m)
			press(t, m, "?")
		}},
		{"47-info", 150, 42, dark, false, func(t *testing.T, m *Model) {
			openSectionByName(t, m, sectionGeneral)
			press(t, m, "i")
		}},
		{"48-library-no-color", 150, 42, dark, true, func(t *testing.T, m *Model) { openSectionByName(t, m, sectionServices) }},
		{"49-library-compact", 80, 24, dark, false, func(t *testing.T, m *Model) { openSectionByName(t, m, sectionServices) }},
		{"55-export", 150, 42, dark, false, func(t *testing.T, m *Model) {
			press(t, m, ":")
			typeText(t, m, "export")
			pump(t, m, "enter")
			typeText(t, m, visualPassphrase)
			pump(t, m, "enter")
		}},
		{"57-selftest", 150, 42, dark, false, func(t *testing.T, m *Model) {
			m.selftest.scenarios = fakeScenarios("alpha")
			m.selftest.run = func(_ context.Context, o selftest.Options) (*selftest.Report, error) {
				return fakeReport(o.Scenario), nil
			}
			drainSelftest(m, startSelftestCommand(t, m, "selftest alpha"))
		}},
		{"58-targets", 150, 42, dark, false, func(t *testing.T, m *Model) {
			connections(t, m)
			m.list.selectName("code-planning")
			pump(t, m, "enter")
			openTargetList(t, m)
		}},
		{"59-resize", 39, 12, dark, false, overview},
	}
}

// visualProfileShot is a snapshot rendered under a forced colour profile of the terminal.
type visualProfileShot struct {
	visualShot
	profile colorProfile
}

// visualProfileShots are the colour-profile variants of the Overview and the Connections list, numbered 60 to 69.
func visualProfileShots() []visualProfileShot {
	dark, light := uiprefs.ThemeQatlasDark, uiprefs.ThemeQatlasLight
	overview := func(*testing.T, *Model) {}
	connections := func(t *testing.T, m *Model) { pump(t, m, "2") }
	return []visualProfileShot{
		{visualShot{"60-overview-256", 150, 42, dark, false, overview}, profile256},
		{visualShot{"61-connections-256", 150, 42, dark, false, connections}, profile256},
		{visualShot{"62-overview-16", 150, 42, dark, false, overview}, profile16},
		{visualShot{"63-connections-16", 150, 42, dark, false, connections}, profile16},
		{visualShot{"64-connections-light", 150, 42, light, false, connections}, profileTrueColor},
		{visualShot{"65-connections-qatlas-background", 150, 42, dark, false, connections}, profileTrueColor},
		{visualShot{"66-connections-no-color", 150, 42, dark, true, connections}, profileTrueColor},
	}
}

// visualTerminalShot is a snapshot of the terminal theme under the answer of a terminal with foreground fg and
// background bg; without them the terminal does not answer.
type visualTerminalShot struct {
	visualProfileShot
	fg, bg string
}

// visualTerminalShots are the snapshots of the terminal theme, numbered 70 to 79: a dark and a light terminal,
// one that does not answer, and a dark one with 256 colours.
func visualTerminalShots() []visualTerminalShot {
	const darkFg, darkBg, lightFg, lightBg = "#cdd6f4", "#1e1e2e", "#24292f", "#ffffff"
	overview := func(*testing.T, *Model) {}
	connections := func(t *testing.T, m *Model) { pump(t, m, "2") }
	details := func(t *testing.T, m *Model) {
		connections(t, m)
		pump(t, m, "down", "down", "tab", "right")
	}
	settings := func(t *testing.T, m *Model) { openSectionByName(t, m, sectionGeneral) }
	shot := func(name string, show func(*testing.T, *Model), profile colorProfile, fg, bg string) visualTerminalShot {
		return visualTerminalShot{visualProfileShot{visualShot{name, 150, 42, uiprefs.ThemeTerminal, false, show}, profile}, fg, bg}
	}
	return []visualTerminalShot{
		shot("70-overview-terminal-dark", overview, profileTrueColor, darkFg, darkBg),
		shot("71-connections-terminal-dark", connections, profileTrueColor, darkFg, darkBg),
		shot("72-connection-details-terminal-dark", details, profileTrueColor, darkFg, darkBg),
		shot("73-settings-terminal-dark", settings, profileTrueColor, darkFg, darkBg),
		shot("74-activity-heatmap-terminal-dark", activityHeatmap, profileTrueColor, darkFg, darkBg),
		shot("75-overview-terminal-light", overview, profileTrueColor, lightFg, lightBg),
		shot("76-connections-terminal-light", connections, profileTrueColor, lightFg, lightBg),
		shot("77-overview-terminal-no-answer", overview, profileTrueColor, "", ""),
		shot("78-connections-terminal-no-answer", connections, profileTrueColor, "", ""),
		shot("79-connections-terminal-256", connections, profile256, darkFg, darkBg),
	}
}

// visualBuiltinShots are the snapshots of a dark and a light built-in theme, numbered 80 to 89.
func visualBuiltinShots() []visualProfileShot {
	overview := func(*testing.T, *Model) {}
	connections := func(t *testing.T, m *Model) { pump(t, m, "2") }
	shot := func(name, theme string, show func(*testing.T, *Model)) visualProfileShot {
		return visualProfileShot{visualShot{name, 150, 42, theme, false, show}, profileTrueColor}
	}
	return []visualProfileShot{
		shot("80-overview-catppuccin-mocha", "catppuccin mocha", overview),
		shot("81-connections-catppuccin-mocha", "catppuccin mocha", connections),
		shot("82-overview-catppuccin-latte", "catppuccin latte", overview),
		shot("83-connections-catppuccin-latte", "catppuccin latte", connections),
	}
}

func TestVisualSnapshots(t *testing.T) {
	fixtures, err := filepath.Abs(visualDir)
	mustNoError(t, err)
	htmlDir := os.Getenv("QATLAS_VISUAL_HTML")
	if htmlDir != "" {
		htmlDir, err = filepath.Abs(htmlDir)
		mustNoError(t, err)
		mustNoError(t, os.MkdirAll(htmlDir, 0o755))
	}
	shots := make([]visualTerminalShot, 0, 90)
	for _, shot := range visualShots() {
		shots = append(shots, visualTerminalShot{visualProfileShot: visualProfileShot{shot, profileTrueColor}})
	}
	for _, shot := range visualProfileShots() {
		shots = append(shots, visualTerminalShot{visualProfileShot: shot})
	}
	shots = append(shots, visualTerminalShots()...)
	for _, shot := range visualBuiltinShots() {
		shots = append(shots, visualTerminalShot{visualProfileShot: shot})
	}
	for _, ts := range shots {
		shot := ts.visualShot
		t.Run(shot.name, func(t *testing.T) {
			visualSetup(t, shot.noColor)
			setProfile(ts.profile)
			m := visualModel(t, fixtures, shot.width, shot.height, shot.theme)
			if ts.fg != "" {
				answerColours(t, m, ts.fg, ts.bg)
			}
			shot.show(t, m)
			view := m.render()
			if strings.Contains(view, "\r") {
				t.Fatal("the view holds a carriage return")
			}
			name := fmt.Sprintf("%s-%dx%d", shot.name, shot.width, shot.height)
			if cwd, _ := os.Getwd(); cwd != "" && strings.Contains(view, cwd) {
				t.Fatalf("the view shows the temporary directory %q", cwd)
			}
			golden := filepath.Join(fixtures, name+".ans")
			if *updateVisual {
				mustNoError(t, os.WriteFile(golden, []byte(view), 0o644))
			} else {
				want, err := os.ReadFile(golden)
				if err != nil {
					t.Fatalf("%s: %v (run with -update to create it)", name, err)
				}
				if !bytes.Equal(want, []byte(view)) {
					t.Errorf("snapshot %s differs from %s%s", name, golden, firstDifference(string(want), view))
				}
			}
			if htmlDir != "" {
				pal := htmlDark
				switch {
				case ts.fg != "":
					pal = htmlPalette{htmlDark.page, ts.bg, ts.fg}
				case shot.theme == uiprefs.ThemeQatlasLight:
					pal = htmlLight
				default:
					if d, ok := themeNamed(shot.theme); ok && d.paints && styles.palette.base != "" {
						pal = htmlPalette{htmlDark.page, string(styles.palette.base), string(styles.palette.text)}
					}
				}
				page := ansiToHTML(view, name, pal)
				mustNoError(t, os.WriteFile(filepath.Join(htmlDir, name+".html"), []byte(page), 0o644))
			}
		})
	}
}

// firstDifference names the first line where got departs from want.
func firstDifference(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < max(len(w), len(g)); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return fmt.Sprintf("\nline %d\nwant %q\ngot  %q", i+1, a, b)
		}
	}
	return ""
}

// sgrState is the text attributes an ANSI stream has set so far; a nil colour is the terminal default.
type sgrState struct {
	fg, bg                *[3]int
	bold, faint, reversed bool
}

var ansiBasic = [16][3]int{
	{0, 0, 0}, {205, 49, 49}, {13, 188, 121}, {229, 229, 16}, {36, 114, 200}, {188, 63, 188}, {17, 168, 205}, {229, 229, 229},
	{102, 102, 102}, {241, 76, 76}, {35, 209, 139}, {245, 245, 67}, {59, 142, 234}, {214, 112, 214}, {41, 184, 219}, {255, 255, 255},
}

// ansi256 is the colour of an index of the 256-colour palette.
func ansi256(n int) [3]int {
	switch {
	case n < 16:
		return ansiBasic[n]
	case n < 232:
		n -= 16
		level := func(v int) int {
			if v == 0 {
				return 0
			}
			return 55 + 40*v
		}
		return [3]int{level(n / 36), level(n / 6 % 6), level(n % 6)}
	}
	v := 8 + 10*(n-232)
	return [3]int{v, v, v}
}

// apply folds the parameters of one SGR sequence into the state.
func (s *sgrState) apply(params []int) {
	if len(params) == 0 {
		params = []int{0}
	}
	for i := 0; i < len(params); i++ {
		p := params[i]
		switch {
		case p == 0:
			*s = sgrState{}
		case p == 1:
			s.bold = true
		case p == 2:
			s.faint = true
		case p == 7:
			s.reversed = true
		case p == 22:
			s.bold, s.faint = false, false
		case p == 27:
			s.reversed = false
		case p == 39:
			s.fg = nil
		case p == 49:
			s.bg = nil
		case p >= 30 && p <= 37:
			c := ansiBasic[p-30]
			s.fg = &c
		case p >= 40 && p <= 47:
			c := ansiBasic[p-40]
			s.bg = &c
		case p >= 90 && p <= 97:
			c := ansiBasic[p-90+8]
			s.fg = &c
		case p >= 100 && p <= 107:
			c := ansiBasic[p-100+8]
			s.bg = &c
		case p == 38 || p == 48:
			var c [3]int
			switch {
			case i+4 < len(params) && params[i+1] == 2:
				c = [3]int{params[i+2], params[i+3], params[i+4]}
				i += 4
			case i+2 < len(params) && params[i+1] == 5:
				c = ansi256(params[i+2])
				i += 2
			default:
				return
			}
			if p == 38 {
				s.fg = &c
			} else {
				s.bg = &c
			}
		}
	}
}

// htmlPalette is the page and terminal-default colours of a snapshot page.
type htmlPalette struct{ page, back, fore string }

var (
	htmlDark  = htmlPalette{"#1e1e1e", "#0c0c0c", "#cccccc"}
	htmlLight = htmlPalette{"#e8e8e8", "#ffffff", "#1a1a1a"}
)

func hexColor(c [3]int) string { return fmt.Sprintf("#%02x%02x%02x", c[0], c[1], c[2]) }

// style is the CSS of the state, with the terminal defaults standing in for unset colours.
func (s sgrState) style(pal htmlPalette) string {
	fg, bg := pal.fore, pal.back
	if s.fg != nil {
		fg = hexColor(*s.fg)
	}
	if s.bg != nil {
		bg = hexColor(*s.bg)
	}
	if s.reversed {
		fg, bg = bg, fg
	}
	css := "color:" + fg + ";background:" + bg
	if s.bold {
		css += ";font-weight:bold"
	}
	if s.faint {
		css += ";opacity:.6"
	}
	return css
}

// ansiToHTML renders a terminal frame as a page: one monospace block, colours and bold from the SGR codes.
func ansiToHTML(view, title string, pal htmlPalette) string {
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html><head><meta charset=\"utf-8\"><title>" + html.EscapeString(title) + "</title>\n")
	b.WriteString("<style>body{margin:0;padding:16px;background:" + pal.page + "}" +
		"pre{margin:0;display:inline-block;white-space:pre;font:14px/normal 'DejaVu Sans Mono','Menlo','Consolas',monospace;" +
		"color:" + pal.fore + ";background:" + pal.back + "}</style></head><body><pre>")
	var state sgrState
	var text strings.Builder
	flush := func() {
		if text.Len() == 0 {
			return
		}
		b.WriteString("<span style=\"" + state.style(pal) + "\">" + html.EscapeString(text.String()) + "</span>")
		text.Reset()
	}
	for i := 0; i < len(view); {
		if view[i] == 0x1b && i+1 < len(view) && view[i+1] == '[' {
			j := i + 2
			for j < len(view) && (view[j] < 0x40 || view[j] > 0x7e) {
				j++
			}
			if j < len(view) && view[j] == 'm' {
				flush()
				var params []int
				for _, part := range strings.Split(view[i+2:j], ";") {
					n, _ := strconv.Atoi(part)
					params = append(params, n)
				}
				state.apply(params)
			}
			i = j + 1
			continue
		}
		text.WriteByte(view[i])
		i++
	}
	flush()
	b.WriteString("</pre></body></html>\n")
	return b.String()
}

// visualTargetPicker opens the picker of the targets of a connection over a listing the test provides.
func visualTargetPicker(t *testing.T, m *Model) {
	src := &listSource{list: pickPage(
		config.TargetEntry{Target: "Orders", Name: "Orders", Kind: "table", HasChildren: true},
		config.TargetEntry{Target: "Customers", Name: "Customers", Kind: "table", HasChildren: true},
		config.TargetEntry{Target: "Invoices", Name: "Invoices", Kind: "table"},
		config.TargetEntry{Target: "Shipments", Name: "Shipments", Kind: "table"},
	)}
	source := src.targetSource()
	source.Browses = func(string) bool { return true }
	m.svc = m.svc.WithTargetSource(source)
	openEntryForm(t, m, sectionConnections, "tables-ops")
	openTargetList(t, m)
	pump(t, m, "n")
	pickRow(t, m, "from the provider's list")
	pump(t, m, "enter")
	if m.screen == screenAdminAuth {
		typeText(t, m, visualPassphrase)
		pump(t, m, "enter")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	deliver(m, cmd)
	pump(t, m, "tab", "down", "down", "tab")
}

// visualSetupSummary walks the guided setup of a new connection to its summary.
func visualSetupSummary(t *testing.T, m *Model) {
	openSectionByName(t, m, sectionConnections)
	m.startSetup()
	pump(t, m, "enter")
	for m.screen != screenSummary {
		before := m.wizard.step
		pump(t, m, "f2")
		if m.wizard.step == before && m.screen != screenSummary {
			t.Fatalf("the setup stays on step %d:\n%s", before, screenOf(m))
		}
	}
}

// visualTokens opens the Tokens list over two demo tokens, one of them expired.
func visualTokens(t *testing.T, m *Model) {
	t.Helper()
	v := m.secrets.Vault()
	// Fixed dates far from any clock the editor reads, so the state of a token never depends on today.
	past, future := time.Date(2000, time.January, 2, 3, 4, 0, 0, time.UTC), time.Date(2099, time.January, 2, 3, 4, 0, 0, time.UTC)
	_, err := v.CreateToken("ci-agent", []string{"wiki-main", "code-planning"}, &future)
	mustNoError(t, err)
	_, err = v.CreateToken("old-agent", []string{"tasks-personal"}, &past)
	mustNoError(t, err)
	openSectionByName(t, m, sectionTokens)
}
