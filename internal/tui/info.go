package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/castrowithcee/qatlas-cli/internal/helptopics"
	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

const (
	// helpPaneMinWidth is the working width from which the explanation of the focused row stands in a pane at
	// the right of forms and pickers. Below it the info key opens the same text as an overlay. The detail
	// area of the lists uses the same threshold.
	helpPaneMinWidth = 120
	// helpPaneWidth is the width of the pane including its frame and the gap before it.
	helpPaneWidth = 42
	// helpPaneMinRows is the fewest rows a pane keeps.
	helpPaneMinRows = 8
	// infoMark ends the hint line or the row of an entry whose long explanation is not on screen.
	infoMark = " (i)"
)

// infoSubject is what the info key explains: a title, the long explanation of the focused row or entry, and
// the term it relates to.
type infoSubject struct {
	title string
	long  string
	term  helptopics.Term
	// doc is the address of the user page of the provider the subject is about, or empty.
	doc string
}

func (s infoSubject) empty() bool {
	return strings.TrimSpace(s.long) == "" && s.term.ID == "" && s.doc == ""
}

// withTerm sets the term with the given ID.
func (s infoSubject) withTerm(id string) infoSubject {
	s.term, _ = helptopics.LookupTerm(id)
	return s
}

// paragraphs is the text of the subject: the long explanation, then the term.
func (s infoSubject) paragraphs() []string {
	var out []string
	if long := strings.TrimSpace(s.long); long != "" {
		out = append(out, long)
	}
	if s.term.ID != "" {
		out = append(out, termName(s.term)+": "+termText(s.term))
	}
	return out
}

// lines wraps the text of the subject into width cells, a blank line between its paragraphs.
func (s infoSubject) lines(width int) []string {
	var out []string
	for i, p := range s.paragraphs() {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, strings.Split(helptopics.Wrap(p, width), "\n")...)
	}
	if lines := docLines(s.doc, width); len(lines) > 0 {
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, lines...)
	}
	return out
}

// labelTerms relates the label of a form row to the term it is about.
var labelTerms = map[string]string{
	"service": "term.service", "credential": "term.credential", "connection": "term.connection",
	targetsLabel: "term.target", profileLabel: "term.profile", effectsLabel: "term.effect",
	accessLabel: "term.effect",
}

// sectionTerms relates an area part to its term.
var sectionTerms = map[section]string{
	sectionServices: "term.service", sectionCredentials: "term.credential", sectionConnections: "term.connection",
	sectionVault: "term.vault", sectionApprovals: "term.approval", sectionTokens: "term.token",
}

// fieldTerm is the term of a form row. A secret row is named after a provider's role, which is no term of
// this editor.
func (m *Model) fieldTerm(f field) string {
	switch f.kind {
	case fieldSecret, fieldMasked, fieldPayloadNew:
		return ""
	case fieldVaultState, fieldVaultAction:
		return "term.vault"
	}
	return labelTerms[f.label]
}

// infoSubject is what the info key explains on the current screen: the focused row of a form, the marked
// entry of a picker, or the area on a tab line and a list. An empty subject means the key has nothing to show.
func (m *Model) infoSubject() infoSubject {
	switch m.screen {
	case screenForm:
		if m.focus < 0 || m.focus >= len(m.fields) || m.fields[m.focus].readOnly || m.fields[m.focus].hidden {
			return infoSubject{}
		}
		f := m.fields[m.focus]
		s := infoSubject{title: labelText(f.label), long: m.fieldDetail(f)}.withTerm(m.fieldTerm(f))
		if f.kind == fieldProvider {
			s.doc = m.docURL(f.value())
		}
		return s
	case screenProviders:
		return m.providerSubject()
	case screenPicker:
		return m.pickerSubject()
	case screenTargets:
		return m.targetsSubject()
	case screenLogs:
		return m.logs.compactSubject()
	case screenNav, screenList:
		if m.section == sectionLogs {
			if s := m.logs.compactSubject(); !s.empty() {
				return s
			}
		}
		return infoSubject{title: m.section.title()}.withTerm(sectionTerms[m.section])
	}
	return infoSubject{}
}

// providerSubject explains the marked provider of the provider table.
func (m *Model) providerSubject() infoSubject {
	id, ok := m.providers.list.selected()
	if !ok || id == "" {
		return infoSubject{}
	}
	metadata, _ := m.cfg.ProviderMetadata(id)
	return infoSubject{title: m.providers.name(id), long: metadata.Description, doc: m.docURL(id)}
}

// pickerSubject explains the marked entry of the value picker.
func (m *Model) pickerSubject() infoSubject {
	if m.focus < 0 || m.focus >= len(m.fields) {
		return infoSubject{}
	}
	f := m.fields[m.focus]
	choice, ok := m.picker.selected()
	if !ok {
		return infoSubject{title: labelText(f.label)}.withTerm(m.fieldTerm(f))
	}
	return m.pickerChoiceSubject(f, choice)
}

// pickerChoiceSubject explains one entry of the picker of f.
func (m *Model) pickerChoiceSubject(f field, choice string) infoSubject {
	metadata, _ := m.cfg.ProviderMetadata(m.formProvider())
	s := infoSubject{title: choiceText(valueText(f.label, choice))}
	switch {
	case m.pickerEffects != nil && isEffectRow(choice):
		s.title = strings.TrimPrefix(choice, effectRowPrefix)
		return s.withTerm("term.effect")
	case m.pickerGroups != nil && m.pickerGroups.isRow(choice):
		id := strings.TrimPrefix(choice, groupRowPrefix)
		for _, group := range metadata.Groups {
			if group.ID == id {
				s.title, s.long = group.Title, group.Description
			}
		}
		return s
	case f.kind == fieldToolList && f.label == toolListLabel:
		for _, tool := range metadata.Tools {
			if tool.ID == choice {
				s.title, s.long = tool.ID, tool.Description
				if s.long == "" {
					s.long = tool.Title
				}
			}
		}
		return s
	case f.label == iconsLabel && choice == uiprefs.IconsNerdFont:
		s.long = iconsDetail()
		return s.withTerm(m.fieldTerm(f))
	case f.label == profileLabel:
		if profile, ok := findProfile(metadata, choice); ok {
			s.title, s.long = profile.Title, profileText(metadata, profile)
		}
		return s.withTerm("term.profile")
	}
	s.title = labelText(f.label)
	return s.withTerm(m.fieldTerm(f))
}

// targetsSubject explains the target screens: the kind being picked, or the targets of the provider.
func (m *Model) targetsSubject() infoSubject {
	metadata := m.targetMetadata()
	s := infoSubject{title: metadata.Label, long: metadata.Description}.withTerm("term.target")
	if m.targetPick != nil {
		kind := m.targetPick.top().kind()
		for _, k := range metadata.Kinds {
			if k.Name == kind {
				s.title, s.long = k.Name, k.Description
			}
		}
	}
	return s
}

// infoReachable reports whether the info key is a key on the current screen: wherever nothing is typed, and
// on a search line only while it is empty. A form keeps it from its text rows only.
func (m *Model) infoReachable() bool {
	if m.infoOpen {
		return false
	}
	switch m.screen {
	case screenForm:
		return !m.typing()
	case screenPicker:
		return m.picker.query() == ""
	}
	return m.helpReachable()
}

// infoPressed reports whether the key press asks for the explanation. The letter works only where the
// screen reads no text; the extra key is none, so it works wherever there is something to explain.
func (m *Model) infoPressed(key tea.KeyPressMsg) bool {
	switch key.String() {
	case keyOf(actInfo):
		return m.infoReachable()
	case infoExtra:
		return infoExtra != "" && !m.infoOpen && !m.keyCapture
	}
	return false
}

// infoWorks reports whether an info key shows something right now, which is when footers name it.
func (m *Model) infoWorks() bool {
	if m.keyCapture || m.infoSubject().empty() {
		return false
	}
	return m.infoReachable() || (infoExtra != "" && !m.infoOpen)
}

// infoLabel names the info keys that work right now: the extra key alone where the letter is typed text.
func (m *Model) infoLabel() string {
	if infoExtra != "" && !m.infoReachable() {
		return keyName(infoExtra)
	}
	return keyLabel(actInfo)
}

// openInfo opens the explanation as an overlay and reports whether there was one to open.
func (m *Model) openInfo() bool {
	s := m.infoSubject()
	if s.empty() {
		return false
	}
	m.infoOpen, m.infoShown = true, s
	m.info.reset()
	return true
}

// updateInfo handles the keys while the overlay is open: the key that closes it, and the keys that scroll a
// text longer than the overlay.
func (m *Model) updateInfo(msg tea.KeyPressMsg) {
	key := msg.String()
	if key == keyOf(actBack) || key == keyOf(actInfo) || (infoExtra != "" && key == infoExtra) {
		m.infoOpen = false
		return
	}
	_, _, room := m.infoLayout()
	m.info.scroll(msg, room)
}

// infoLayout is the text of the overlay wrapped into its inner width, and the rows it shows of it: all of them
// while they fit, otherwise one less, which the position line takes.
func (m *Model) infoLayout() ([]string, int, int) {
	inner := min(modalMaxInner, max(m.termWidth-8, 10))
	lines := m.infoShown.lines(inner)
	room := max(m.termHeight-8, 3)
	if len(lines) > room {
		room--
	}
	return lines, inner, room
}

// paneScreen reports whether the current screen is a form or a picker, which carry the pane.
func (m *Model) paneScreen() bool {
	switch m.screen {
	case screenForm, screenPicker:
		return true
	case screenTargets:
		return m.targetPick != nil
	}
	return false
}

// paneShown reports whether the pane stands beside the current screen.
func (m *Model) paneShown() bool { return m.inPane || (m.width >= helpPaneMinWidth && m.paneScreen()) }

// screenView is editorView, and from helpPaneMinWidth on, with the pane of the focused row at its right.
func (m *Model) screenView() string {
	if !m.backdrop {
		// Every drawing of the screen starts its targets anew; the last one is the one that is shown.
		m.hits, m.zones = nil, nil
	}
	if !m.paneShown() {
		return m.editorView()
	}
	full := m.width
	m.width, m.inPane = full-helpPaneWidth, true
	left := m.editorView()
	m.width, m.inPane = full, false
	// A form's pane starts level with its rows, below the title.
	off := 0
	if m.screen == screenForm {
		off = max(min(m.formTop-m.workspaceTop(), m.height-helpPaneMinRows), 0)
	}
	pane := strings.Repeat("\n", off) + m.pane(helpPaneWidth-2, max(m.height-off, helpPaneMinRows))
	top := m.workspaceTop() + off
	m.infoPane.x0, m.infoPane.y0, m.infoPane.x1, m.infoPane.y1 = full-helpPaneWidth+2, top, full, top+strings.Count(pane, "\n")+1-off
	return lipgloss.JoinHorizontal(lipgloss.Top, padBlock(left, full-helpPaneWidth), "  ", pane)
}

// padBlock pads every line of block to width, so a block beside it starts in one column.
func padBlock(block string, width int) string {
	lines := strings.Split(block, "\n")
	for i, l := range lines {
		lines[i] = padLine(l, width)
	}
	return strings.Join(lines, "\n")
}

// pane draws the explanation of the focused row in a frame of width cells and at most rows rows; text that
// does not fit scrolls with the wheel over the pane, and the info key shows it in an overlay with keys.
func (m *Model) pane(width, rows int) string {
	s := m.infoSubject()
	inner := width - 4
	lines := s.lines(inner)
	if s.empty() {
		lines = infoSubject{long: tr(tInfoNoFurtherExplanationForThis)}.lines(inner)
	}
	key := s.title + "\n" + s.long
	rows = max(rows-2, 1)
	title := truncateDots(s.title, inner-2)
	if m.screen == screenForm {
		// A form's pane is as high as its text.
		rows, title = min(rows, max(len(lines), 1)), tr(tFormHelpTitle)
	}
	lines, position := m.infoPane.fit(key, lines, rows)
	if position != "" {
		lines = append(slices.Clone(lines), position)
	}
	if len(lines) > rows {
		// A pane of one row has no room for the position line and cuts.
		lines = slices.Clone(lines[:rows])
		lines[rows-1] = truncateDots(lines[rows-1]+" ...", inner)
	}
	for i, l := range lines {
		lines[i] = styles.hint.Render(l)
	}
	return strings.Join(panel(title, lines, inner, rows, panelOpts{}), "\n")
}

// truncateDots cuts text to width cells, ending in "..." when it was cut.
func truncateDots(text string, width int) string {
	if lipgloss.Width(text) <= width {
		return text
	}
	if width <= 3 {
		return strings.Repeat(".", max(width, 0))
	}
	return strings.TrimRight(ansi.Truncate(text, width-3, ""), " ") + "..."
}

// hintLine is the one-line hint under the focused row: cut to the workspace with "..." at its right and,
// where the pane is not shown, ending in the info mark when a longer explanation is behind the info key.
func (m *Model) hintLine(hint string, more bool) string {
	indent, width := m.fit(m.hintIndent())
	hint = strings.Join(strings.Fields(hint), " ")
	mark := ""
	if more && !m.paneShown() && m.infoReachable() {
		mark = infoMark
	}
	return indent + styles.hint.Render(truncateDots(hint, width-lipgloss.Width(mark))+mark)
}

// moreBehind reports whether the long explanation of f is more than the one line under the row: it is a
// text of its own, it does not fit the line, or a term belongs to the row.
func (m *Model) moreBehind(f field, hint string) bool {
	s := infoSubject{long: m.fieldDetail(f)}.withTerm(m.fieldTerm(f))
	_, width := m.fit(m.hintIndent())
	one := strings.Join(strings.Fields(hint), " ")
	return s.term.ID != "" || (strings.TrimSpace(s.long) != "" &&
		(f.detail != "" || lipgloss.Width(one) > width-lipgloss.Width(infoMark)))
}

// infoView is the overlay over the current screen, or false when the terminal is too small for one and the
// explanation takes the whole workspace.
func (m *Model) infoView() string {
	s := m.infoShown
	text, inner, room := m.infoLayout()
	lines := m.info.window(text, room, false)
	position := m.info.position(len(text), room)
	if s.term.ID != "" {
		// The term the text ends with is named in bold where its paragraph starts.
		head := termName(s.term) + ": "
		for i, l := range lines {
			if strings.HasPrefix(l, head) {
				lines[i] = styles.title.Render(termName(s.term)) + l[len(head)-2:]
			}
		}
	}
	keys := footerKeys("{back} close")
	scroll := ""
	if position != "" {
		lines = append(lines, styles.hint.Render(position))
		scroll = footerText("up/down scroll") + " · "
	}
	// The copy key is named only where it fits beside the others on the line.
	if withCopy := scroll + footerKeys("{copy} copy") + " · " + keys; lipgloss.Width(withCopy) <= inner {
		keys = withCopy
	} else {
		keys = scroll + keys
	}
	lines = append(lines, "", keyHintGroupFoot(keys))
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, inner, "")
	}
	dialog := panel(truncateDots(s.title, inner-2), lines, inner, len(lines), panelOpts{focused: true})
	background := m.frame(m.workspaceView())
	top, left := dialogOrigin(dialog, m.termWidth, m.termHeight)
	m.markDialog(dialog, top, left)
	return overlay(background, dialog, m.termWidth, m.termHeight)
}

// pickerMark is the info mark of a picker entry that has a long explanation, where the pane is not shown.
func (m *Model) pickerMark(f field, choice string) string {
	if m.paneShown() {
		return ""
	}
	if s := m.pickerChoiceSubject(f, choice); strings.TrimSpace(s.long) != "" ||
		(m.pickerEffects != nil && isEffectRow(choice)) {
		return infoMark
	}
	return ""
}
