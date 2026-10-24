package tui

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/i18n"
)

// viewSet collects the views of one run through the screens of the forms, each checked against the size.
type viewSet struct {
	t     *testing.T
	w, h  int
	views []string
}

// The editor is driven at this size, where every form is usable, and drawn at the size under test.
const navWidth, navHeight = 140, 40

// see draws the editor at the size under test and checks that the view fits.
func (v *viewSet) see(m *Model) string {
	v.t.Helper()
	sizedModel(m, v.w, v.h)
	view := m.View()
	sizedModel(m, navWidth, navHeight)
	assertViewFits(v.t, view, v.w, v.h)
	v.views = append(v.views, view)
	return view
}

// walkRows draws the form once with every row focused, so each hint and each row is seen.
func (v *viewSet) walkRows(m *Model) {
	v.t.Helper()
	v.see(m)
	for i := 0; i < len(m.fields) && m.screen == screenForm; i++ {
		press(v.t, m, "tab")
		v.see(m)
	}
}

// formsBase is an editor over a BookStack service with two connections, one of them disabled, and a
// Telegram service of the same configuration.
func formsBase(t *testing.T, w, h int) *Model {
	t.Helper()
	m, _ := toolsModel(t, wikiRegistry(t), map[string]config.Connection{
		"docs": {Service: "wiki", Credential: "reader", Description: "team wiki", Target: "book/12",
			Tools: []string{"bookstack.pages.list"}},
		"docs-off":  {Service: "wiki", Credential: "reader", Disabled: true},
		"docs-wide": {Service: "wiki", Credential: "reader", Paths: []string{"/tmp"}},
	})
	mustNoError(t, m.cfg.SetService("bot", config.Service{Provider: "telegram", BaseURL: "https://bot.example.invalid"}))
	mustNoError(t, m.cfg.SetCredential("billing", config.Credential{Type: config.CredentialTypeKeyring, Forward: true,
		Fields: []string{"login"}}))
	m.list.reset(m.entryNames(m.section))
	return sizedModel(m, w, h)
}

func (v *viewSet) lists() {
	t := v.t
	for _, s := range []section{sectionConnections, sectionServices, sectionCredentials, sectionDefaults} {
		m := formsBase(t, navWidth, navHeight)
		openSectionByName(t, m, s)
		v.see(m)
		press(t, m, "down")
		v.see(m)
		press(t, m, "left")
		v.see(m)
		press(t, m, "right")
		press(t, m, "/")
		v.see(m)
		typeText(t, m, "zzzzzz")
		v.see(m)
		press(t, m, "esc", "esc")
		if groupedSection(s) {
			press(t, m, "tab")
			v.see(m)
			press(t, m, "tab")
			v.see(m)
		}
		// Empty lists.
		e, _, _ := newModel(t)
		sizedModel(e, navWidth, navHeight)
		openSectionByName(t, e, s)
		v.see(e)
	}
}

func (v *viewSet) forms() {
	t := v.t
	// A new connection, its sections and its validation error.
	m := formsBase(t, navWidth, navHeight)
	openSectionByName(t, m, sectionConnections)
	pressNew(t, m)
	v.walkRows(m)
	press(t, m, "pgdown", "pgdown", "pgdown", "pgdown", "pgdown")
	v.see(m)
	focusField(t, m, activeForLabel)
	v.see(m)
	pump(t, m, "f2")
	v.see(m)
	// An existing connection: tools, profile and access rows.
	m = formsBase(t, navWidth, navHeight)
	openConnection(t, m, "docs")
	v.walkRows(m)
	focusField(t, m, profileLabel)
	press(t, m, "right")
	v.see(m)
	press(t, m, "y")
	v.see(m)
	selectAccess(t, m, accessEffects)
	v.see(m)
	press(t, m, "i")
	m = formsBase(t, navWidth, navHeight)
	openConnection(t, m, "docs-off")
	v.walkRows(m)
	// A copy of a connection.
	m = formsBase(t, navWidth, navHeight)
	openSectionByName(t, m, sectionConnections)
	m.list.selectName("docs")
	press(t, m, "p")
	v.see(m)
	if m.screen == screenForm {
		v.walkRows(m)
	}
	// Services and credentials, new and existing.
	for _, s := range []section{sectionServices, sectionCredentials} {
		m = formsBase(t, navWidth, navHeight)
		openSectionByName(t, m, s)
		pressNew(t, m)
		v.walkRows(m)
		pump(t, m, "f2")
		v.see(m)
		press(t, m, "esc")
		v.see(m)
		press(t, m, "d")
		m = formsBase(t, navWidth, navHeight)
		openSectionByName(t, m, s)
		pump(t, m, "enter")
		v.walkRows(m)
		press(t, m, "i")
		v.see(m)
		press(t, m, "esc", "esc")
		// The delete question.
		m = formsBase(t, navWidth, navHeight)
		openSectionByName(t, m, s)
		press(t, m, "d")
		press(t, m, "d")
		v.see(m)
	}
	// A credential whose secret row opens the masked prompt.
	m = formsBase(t, navWidth, navHeight)
	openSectionByName(t, m, sectionCredentials)
	m.list.selectName("reader")
	pump(t, m, "enter")
	focusRole(t, m, "token-id")
	v.see(m)
	press(t, m, "enter")
	v.see(m)
	press(t, m, "esc")
	press(t, m, "d")
	v.see(m)
	// The Defaults form.
	m = formsBase(t, navWidth, navHeight)
	openSectionByName(t, m, sectionDefaults)
	pressNew(t, m)
	v.see(m)
	// A new credential in the payload form.
	m = formsBase(t, navWidth, navHeight)
	openSectionByName(t, m, sectionCredentials)
	press(t, m, "s")
	if m.screen == screenForm {
		v.walkRows(m)
		focusField(t, m, payloadNewLabel)
		typeText(t, m, "login")
		press(t, m, "enter")
		v.walkRows(m)
		m.Update(ctrlD())
		v.see(m)
	}
	m = formsBase(t, navWidth, navHeight)
	openSectionByName(t, m, sectionCredentials)
	m.list.selectName("billing")
	pump(t, m, "enter")
	if m.screen == screenForm {
		v.walkRows(m)
	}
}

func (v *viewSet) template() {
	t := v.t
	m := formsBase(t, navWidth, navHeight)
	openSectionByName(t, m, sectionServices)
	press(t, m, "n")
	v.see(m)
	press(t, m, "down")
	v.see(m)
	press(t, m, "enter")
	v.see(m)
	typeText(t, m, "zzzz")
	v.see(m)
	press(t, m, "esc")
	// n in Connections is the guided setup; it asks for a template first.
	m = formsBase(t, navWidth, navHeight)
	openSectionByName(t, m, sectionConnections)
	press(t, m, "n")
	v.see(m)
}

func (v *viewSet) setup() {
	t := v.t
	for until := 0; until <= stepSummary; until++ {
		m, _, _ := newModel(t)
		sizedModel(m, navWidth, navHeight)
		walkSetup(t, m, until)
		v.see(m)
		if until > stepProvider && until < stepSummary {
			v.walkRows(m)
		}
		if until == stepSummary {
			pump(t, m, "f2")
			v.see(m)
			press(t, m, "t")
			v.see(m)
		}
	}
	// The setup's filled steps with a problem under a row.
	m, _, _ := newModel(t)
	sizedModel(m, navWidth, navHeight)
	press(t, m, "c")
	v.see(m)
	typeText(t, m, "zzzz")
	v.see(m)
	press(t, m, "esc", "esc")
	v.see(m)
}

func (v *viewSet) pickers() {
	t := v.t
	// The provider table.
	m, _ := providerModel(t, providerRegistry(t, 8))
	sizedModel(m, navWidth, navHeight)
	openProviderRow(t, m, sectionConnections)
	press(t, m, "enter")
	v.see(m)
	typeText(t, m, "zzzzzz")
	v.see(m)
	// The tool picker, flat and grouped, and the effects picker.
	for _, reg := range []string{"wiki", "providers"} {
		var base *Model
		if reg == "wiki" {
			base = formsBase(t, navWidth, navHeight)
		} else {
			base, _ = providerModel(t, providerRegistry(t, 8))
			sizedModel(base, navWidth, navHeight)
		}
		openSectionByName(t, base, sectionConnections)
		pressNew(t, base)
		if reg == "providers" {
			focusField(t, base, providerLabel)
		}
		selectAccess(t, base, accessTools)
		focusField(t, base, toolListLabel)
		press(t, base, "enter")
		v.see(base)
		press(t, base, "down", " ", "right", "down", " ")
		v.see(base)
		typeText(t, base, "zzzzzz")
		v.see(base)
		press(t, base, "esc")
		if base.screen == screenLeave {
			v.see(base)
			press(t, base, "d")
		}
		selectAccess(t, base, accessEffects)
		focusField(t, base, effectsLabel)
		press(t, base, "enter")
		v.see(base)
		press(t, base, "esc")
		// A service and a credential row open a plain picker.
		focusField(t, base, "service")
		press(t, base, "enter")
		v.see(base)
		press(t, base, "esc")
	}
}

func (v *viewSet) targetsAndPaths() {
	t := v.t
	m, _, _ := knownTargetsModel(t)
	sizedModel(m, navWidth, navHeight)
	openConnection(t, m, "gh-a")
	openTargetList(t, m)
	v.see(m)
	press(t, m, "r")
	v.see(m)
	typeText(t, m, "main repo")
	press(t, m, "enter")
	v.see(m)
	press(t, m, "d")
	v.see(m)
	press(t, m, "n")
	press(t, m, "a")
	v.see(m)
	typeText(t, m, "zzzz")
	v.see(m)
	closeAdd(t, m)
	press(t, m, "a")
	pickRow(t, m, newRow("repository"))
	press(t, m, "enter")
	v.see(m)
	press(t, m, "enter")
	v.see(m)
	press(t, m, "f2")
	v.see(m)
	closeAdd(t, m)
	press(t, m, "a")
	pickRow(t, m, tr(tTargetTypeATarget))
	press(t, m, "enter")
	v.see(m)
	press(t, m, "enter")
	v.see(m)
	typeText(t, m, "x")
	press(t, m, "enter")
	v.see(m)
	closeAdd(t, m)
	// The path list and the file lists.
	m = formsBase(t, navWidth, navHeight)
	openConnection(t, m, "docs-wide")
	openPathList(t, m)
	v.see(m)
	press(t, m, "a")
	v.see(m)
	press(t, m, "enter")
	v.see(m)
	typeText(t, m, "/tm")
	v.see(m)
	press(t, m, "esc", "esc")
	press(t, m, "d")
	v.see(m)
	m = formsBase(t, navWidth, navHeight)
	openConnection(t, m, "docs-wide")
	for _, label := range []string{filesReadLabel, filesWriteLabel} {
		if m.field(label) == nil || m.field(label).hidden {
			continue
		}
		focusField(t, m, label)
		press(t, m, "enter")
		v.see(m)
		press(t, m, "esc")
	}
	// The target list of a form without any target.
	m = formsBase(t, navWidth, navHeight)
	openConnection(t, m, "docs-off")
	openTargetList(t, m)
	v.see(m)
}

// closeAdd leaves the add menu and the builder, answering the question that asks about unfinished input.
func closeAdd(t *testing.T, m *Model) {
	t.Helper()
	for i := 0; i < 8 && (m.targetAdd != nil || m.targetEdit >= 0 || m.screen == screenLeave); i++ {
		if m.screen == screenLeave {
			press(t, m, "d")
		} else {
			press(t, m, "esc")
		}
	}
}

// run draws every screen of the forms at the size.
func (v *viewSet) run() {
	v.lists()
	v.forms()
	v.template()
	v.setup()
	v.pickers()
	v.targetsAndPaths()
}

// The screens of the forms fit the terminal in both languages.
func TestFormsFitInBothLanguages(t *testing.T) {
	sizes := []struct{ w, h int }{{minimumWidth, minimumHeight + 1}, {80, 24}, {helpPaneMinWidth, 40}, {140, 40}}
	eachLanguage(t, func(t *testing.T) {
		for _, size := range sizes {
			(&viewSet{t: t, w: size.w, h: size.h}).run()
		}
	})
}

var formatVerb = regexp.MustCompile(`%[-+# 0-9.*]*[a-zA-Z%]`)

// coreTexts are words of the catalog that the core also supplies, so they can stand in a German screen.
var coreTexts = []string{"system keyring", "a name you choose"}

// englishFragments are the runs of words of an English catalog text between its format verbs: those of three
// words or more, and a text of two words that stands whole.
func englishFragments(text string) []string {
	var out []string
	parts := formatVerb.Split(text, -1)
	for _, part := range parts {
		part = strings.TrimSpace(strings.Trim(part, ".:;,()[]'\"…"))
		words := len(strings.Fields(part))
		if slices.Contains(coreTexts, part) {
			continue
		}
		if words >= 3 || (words == 2 && len(parts) == 1) {
			out = append(out, part)
		}
	}
	return out
}

// No screen of the forms shows an English multi-word text of the catalog in German. Texts that the core and the
// provider descriptors supply are not in the catalog and so not looked for.
func TestGermanFormsShowNoEnglishCatalogText(t *testing.T) {
	useLanguage(t, i18n.German)
	var seen strings.Builder
	for _, size := range []struct{ w, h int }{{80, 24}, {140, 40}} {
		v := &viewSet{t: t, w: size.w, h: size.h}
		v.run()
		for _, view := range v.views {
			seen.WriteString(strings.Join(strings.Fields(view), " ") + "\n")
		}
	}
	text := seen.String()
	for id, english := range textEN {
		german := textDE[id]
		if english == german {
			continue
		}
		for _, fragment := range englishFragments(english) {
			if strings.Contains(text, fragment) && !strings.Contains(german, fragment) {
				t.Errorf("German screens show the English text %q (key %q)", fragment, id)
			}
		}
	}
	if !strings.Contains(text, "Verbindung") && !strings.Contains(text, "Connection") {
		t.Errorf("the German screens look empty")
	}
}

// newRow is the text of the add-menu row that builds a new target of the kind.
func newRow(kind string) string {
	return strings.SplitN(tr(tTargetNewStepByStep, kind, ""), ",", 2)[0]
}
