package tui

import (
	"bytes"
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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
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
	lipgloss.SetColorProfile(termenv.TrueColor)
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
func visualModel(t *testing.T, fixtures string, width, height int, theme, background string) *Model {
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
	m.now = func() time.Time { return visualNow }
	m.logs = newLogView(visualNow)
	m.prefs = uiprefs.Default()
	m.prefs.Theme, m.prefs.Background = theme, background
	applyColorScheme(theme, background)
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
	background    string
	noColor       bool
	show          func(t *testing.T, m *Model)
}

func visualShots() []visualShot {
	dark, term := uiprefs.ThemeDark, uiprefs.BackgroundTerminal
	overview := func(*testing.T, *Model) {}
	connections := func(t *testing.T, m *Model) { pump(t, m, "2") }
	return []visualShot{
		{"01-overview", 150, 42, dark, term, false, overview},
		{"02-connections", 150, 42, dark, term, false, connections},
		{"03-connection-form", 150, 42, dark, term, false, func(t *testing.T, m *Model) {
			connections(t, m)
			pump(t, m, "enter")
		}},
		{"04-connection-delete", 150, 42, dark, term, false, func(t *testing.T, m *Model) {
			connections(t, m)
			pump(t, m, "d")
		}},
		{"05-activity-table", 150, 42, dark, term, false, func(t *testing.T, m *Model) {
			pump(t, m, "3")
			for m.logs.mode != logModeAll {
				m.logs.focus = logFocusMode
				pump(t, m, "right")
			}
		}},
		{"06-activity-heatmap", 150, 42, dark, term, false, func(t *testing.T, m *Model) {
			pump(t, m, "3")
			for m.logs.mode != logModeAll {
				m.logs.focus = logFocusMode
				pump(t, m, "right")
			}
			m.logs.focus = logFocusView
			pump(t, m, "right")
		}},
		{"07-palette", 150, 42, dark, term, false, func(t *testing.T, m *Model) { press(t, m, ":") }},
		{"08-library", 150, 42, dark, term, false, func(t *testing.T, m *Model) { openSectionByName(t, m, sectionServices) }},
		{"09-access", 150, 42, dark, term, false, func(t *testing.T, m *Model) { openSectionByName(t, m, sectionTokens) }},
		{"10-settings", 150, 42, dark, term, false, func(t *testing.T, m *Model) { openSectionByName(t, m, sectionGeneral) }},
		{"11-overview", 80, 24, dark, term, false, overview},
		{"12-connections", 80, 24, dark, term, false, connections},
		{"13-overview-light", 150, 42, uiprefs.ThemeLight, term, false, overview},
		{"14-overview-qatlas-background", 150, 42, dark, uiprefs.BackgroundQatlas, false, overview},
		{"15-overview-no-color", 150, 42, dark, term, true, overview},
		{"16-connection-form-compact", 80, 24, dark, term, false, func(t *testing.T, m *Model) {
			connections(t, m)
			pump(t, m, "enter")
		}},
		{"17-service-form", 150, 42, dark, term, false, func(t *testing.T, m *Model) {
			openSectionByName(t, m, sectionServices)
			m.openForm("")
		}},
		{"18-credential-form", 150, 42, dark, term, false, func(t *testing.T, m *Model) {
			openSectionByName(t, m, sectionCredentials)
			m.openForm("")
		}},
		{"19-token-form", 150, 42, dark, term, false, func(t *testing.T, m *Model) {
			openSectionByName(t, m, sectionTokens)
			m.openForm("")
		}},
		{"20-setup-step", 150, 42, dark, term, false, func(t *testing.T, m *Model) {
			connections(t, m)
			m.startSetup()
			pump(t, m, "enter")
		}},
		{"21-connection-form-no-color", 150, 42, dark, term, true, func(t *testing.T, m *Model) {
			connections(t, m)
			pump(t, m, "enter")
		}},
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
	for _, shot := range visualShots() {
		t.Run(shot.name, func(t *testing.T) {
			visualSetup(t, shot.noColor)
			m := visualModel(t, fixtures, shot.width, shot.height, shot.theme, shot.background)
			shot.show(t, m)
			view := m.View()
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
				if shot.theme == uiprefs.ThemeLight {
					pal = htmlLight
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
		"pre{margin:0;display:inline-block;white-space:pre;font:14px/1.2 'DejaVu Sans Mono','Menlo','Consolas',monospace;" +
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
