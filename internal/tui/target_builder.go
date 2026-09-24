package tui

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Adding a target opens a menu instead of an empty line whenever there is something to offer: the targets
// other connections of the same service use, a builder for every kind of target the provider names, and
// the typed target itself. The builder is fed by the forms of a kind alone: a placeholder in upper case,
// such as OWNER, is asked for with the values the same service already uses there as suggestions, * is
// offered as all, and fixed parts are taken without asking unless the forms differ in them. What it
// builds, and what is picked, is taken like a typed target: the provider checks it, and a refused one stays
// typed with the reason. A provider without kinds and without known targets opens the empty line at once.

// Row keys of the add menu and the builder. The part after the colon is the value of the row.
const (
	addKnown = "known:"
	addKind  = "kind:"
	addValue = "value:"
	addTyped = "typed"
	addDone  = "done"
)

// targetAdd is the add menu while it is open, and the builder once a kind was chosen in it.
type targetAdd struct {
	choices filterList
	text    map[string]string
	// known are the targets other connections of the service use, with the connections that use them.
	known map[string][]string
	// kind is the chosen kind, or -1 in the menu. forms are the forms of the kind, split into segments,
	// that still fit what was chosen; chosen are the segments so far, and history their number before each
	// choice, so backspace on an empty line can step back.
	kind    int
	forms   [][]string
	chosen  []string
	history []int
}

// placeholder reports whether a segment of a form is a placeholder such as OWNER.
func placeholder(segment string) bool {
	if segment == "" {
		return false
	}
	for _, r := range segment {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return segment[0] >= 'A' && segment[0] <= 'Z'
}

// fitsForm reports whether the segments of a target are spelled in the form.
func fitsForm(segments, form []string) bool {
	if len(segments) != len(form) {
		return false
	}
	for i, part := range form {
		switch {
		case placeholder(part):
			if segments[i] == "" || segments[i] == "*" {
				return false
			}
		case segments[i] != part:
			return false
		}
	}
	return true
}

// formService is the service the form binds: the service row of a connection form, or the service the
// guided setup chose. A service the setup adds has no connections yet.
func (m *Model) formService() string {
	if m.wizard != nil {
		if len(m.wizard.pages) <= stepService {
			return ""
		}
		if service := pageValue(m.wizard.pages[stepService], "service"); service != newService {
			return service
		}
		return ""
	}
	return m.fieldValue("service")
}

// serviceTargets are the targets the connections of the form's service hold, by target with the
// connections that hold it. The connection being edited is left out when others is set.
func (m *Model) serviceTargets(others bool) map[string][]string {
	service := m.formService()
	targets := map[string][]string{}
	if service == "" {
		return targets
	}
	for _, name := range m.entryNames(sectionConnections) {
		connection := m.cfg.Connections[name]
		if connection.Service != service || (others && name == m.editing) {
			continue
		}
		for _, target := range connection.TargetValues() {
			targets[target] = append(targets[target], name)
		}
	}
	return targets
}

// startAdd opens the add menu, or the empty line when it would offer nothing but typing.
func (m *Model) startAdd() {
	metadata := m.targetMetadata()
	known := m.serviceTargets(true)
	for _, target := range m.targetList.all {
		delete(known, target)
	}
	if len(known) == 0 && len(metadata.Kinds) == 0 {
		m.editTarget(len(m.targetList.all), "")
		return
	}
	a := &targetAdd{known: known, kind: -1, text: map[string]string{}}
	a.choices = newFilterList(func(key string) string { return a.text[key] })
	a.choices.startFilter()
	m.targetAdd = a
	m.clearMessages()
	m.refreshAdd()
}

// refreshAdd rebuilds the rows of the menu or of the builder step for the line typed now. The typed line
// comes first, so enter takes what was typed rather than a longer value that happens to contain it.
func (m *Model) refreshAdd() {
	a := m.targetAdd
	query := a.choices.query()
	a.text = map[string]string{}
	var rows []string
	add := func(key, text string) {
		if _, ok := a.text[key]; !ok {
			a.text[key] = text
			rows = append(rows, key)
		}
	}
	exact := ""
	if a.kind < 0 {
		var targets []string
		for target := range a.known {
			targets = append(targets, target)
		}
		sort.Strings(targets)
		for _, target := range targets {
			add(addKnown+target, target+"  · used by "+strings.Join(a.known[target], ", "))
			if target == query {
				exact = addKnown + target
			}
		}
		for i, kind := range m.targetMetadata().Kinds {
			add(fmt.Sprintf("%s%d", addKind, i), "new "+kind.Name+", step by step: "+kind.Description)
		}
		typed := "type a target"
		if query != "" {
			typed = `use as typed: "` + query + `"`
		}
		if query != "" && exact == "" {
			rows = append([]string{addTyped}, rows...)
			a.text[addTyped] = typed
		} else {
			add(addTyped, typed)
		}
	} else {
		names, fixed, done := a.step()
		for _, value := range m.targetSuggestions(names) {
			add(addValue+value, value)
		}
		for _, value := range fixed {
			if value != "*" {
				add(addValue+value, value)
			}
		}
		if slices.Contains(fixed, "*") {
			add(addValue+"*", "* (all)")
		}
		if done {
			add(addDone, "done: "+strings.Join(a.chosen, "/"))
		}
		if _, ok := a.text[addValue+query]; ok {
			exact = addValue + query
		} else if query != "" && len(names) > 0 {
			rows = append([]string{addTyped}, rows...)
			a.text[addTyped] = `use "` + query + `"`
		} else if query == "" && len(rows) > 0 && rows[0] == addValue+"*" {
			// A pattern widens the list, so it is never what enter takes without a choice: with nothing
			// else to offer first, the selection rests on a line that takes nothing.
			rows = append([]string{addTyped}, rows...)
			a.text[addTyped] = "(type a value, or choose a row)"
		}
	}
	a.choices.setItems(rows)
	if exact == "" || !a.choices.selectName(exact) {
		a.choices.cursor = 0
	}
}

// step is what the builder asks for next: the placeholders the remaining forms have at the next segment,
// the fixed values they have there, and whether one of them is complete already.
func (a *targetAdd) step() (names, fixed []string, done bool) {
	i := len(a.chosen)
	for _, form := range a.forms {
		switch {
		case len(form) == i:
			done = true
		case placeholder(form[i]):
			if !slices.Contains(names, form[i]) {
				names = append(names, form[i])
			}
		case !slices.Contains(fixed, form[i]):
			fixed = append(fixed, form[i])
		}
	}
	return names, fixed, done
}

// choose takes one value for the next segment, or ends the target where done, and keeps the forms that
// allow it.
func (a *targetAdd) choose(value string, done bool) (string, bool) {
	i := len(a.chosen)
	a.history = append(a.history, i)
	var forms [][]string
	if done {
		for _, form := range a.forms {
			if len(form) == i {
				forms = append(forms, form)
			}
		}
	} else {
		_, fixed, _ := a.step()
		literal := slices.Contains(fixed, value)
		for _, form := range a.forms {
			if len(form) > i && ((literal && form[i] == value) || (!literal && placeholder(form[i]))) {
				forms = append(forms, form)
			}
		}
		a.chosen = append(a.chosen, value)
	}
	a.forms = forms
	return a.autofill()
}

// autofill takes the fixed parts that follow without asking, and reports the target once every remaining
// form is complete.
func (a *targetAdd) autofill() (string, bool) {
	for {
		names, fixed, complete := a.step()
		if complete && len(names) == 0 && len(fixed) == 0 {
			return strings.Join(a.chosen, "/"), true
		}
		// * is never taken without asking, even where a form allows nothing else.
		if complete || len(names) > 0 || len(fixed) != 1 || fixed[0] == "*" {
			return "", false
		}
		a.chosen = append(a.chosen, fixed[0])
	}
}

// targetSuggestions are the values the targets of the service already hold where the builder asks for one
// of names next: targets that start with what was chosen and are spelled in a form of the provider with
// such a placeholder at that segment.
func (m *Model) targetSuggestions(names []string) []string {
	a := m.targetAdd
	i := len(a.chosen)
	var forms [][]string
	for _, kind := range m.targetMetadata().Kinds {
		for _, form := range kind.Forms {
			forms = append(forms, strings.Split(form, "/"))
		}
	}
	targets := m.serviceTargets(false)
	for _, target := range m.targetList.all {
		targets[target] = nil
	}
	var values []string
	for target := range targets {
		segments := strings.Split(target, "/")
		if len(segments) <= i || !slices.Equal(segments[:i], a.chosen) || slices.Contains(values, segments[i]) {
			continue
		}
		for _, form := range forms {
			if fitsForm(segments, form) && slices.Contains(names, form[i]) {
				values = append(values, segments[i])
				break
			}
		}
	}
	sort.Strings(values)
	return values
}

// startBuilder opens the builder on one kind of target.
func (m *Model) startBuilder(kind int) {
	a := m.targetAdd
	a.kind, a.forms, a.chosen, a.history = kind, nil, nil, nil
	for _, form := range m.targetMetadata().Kinds[kind].Forms {
		a.forms = append(a.forms, strings.Split(form, "/"))
	}
	// The kind itself is the first choice, so backspace on its first step returns to the menu.
	a.history = []int{-1}
	if target, complete := a.autofill(); complete {
		// A kind without a single choice is complete as soon as it is chosen.
		m.finishAdd(target)
		return
	}
	m.resetAddLine()
}

// resetAddLine empties the typed line of the add menu and shows the rows for it.
func (m *Model) resetAddLine() {
	m.targetAdd.choices.input.SetValue("")
	m.targetAdd.choices.offset = 0
	m.refreshAdd()
}

// stepBack undoes the last choice of the builder, or returns from its first step to the menu.
func (m *Model) stepBack() {
	a := m.targetAdd
	last := a.history[len(a.history)-1]
	a.history = a.history[:len(a.history)-1]
	if last < 0 {
		a.kind = -1
		m.resetAddLine()
		return
	}
	a.chosen = a.chosen[:last]
	a.forms = nil
	for _, form := range m.targetMetadata().Kinds[a.kind].Forms {
		if segments := strings.Split(form, "/"); len(segments) >= last && fitsPrefix(segments[:last], a.chosen) {
			a.forms = append(a.forms, segments)
		}
	}
	m.resetAddLine()
}

// fitsPrefix reports whether chosen values fit the first segments of a form.
func fitsPrefix(form, chosen []string) bool {
	for i, part := range form {
		if placeholder(part) {
			if chosen[i] == "*" {
				return false
			}
		} else if chosen[i] != part {
			return false
		}
	}
	return true
}

// finishAdd closes the menu and takes target like a typed one: a refused target stays typed with the reason.
func (m *Model) finishAdd(target string) {
	m.targetAdd = nil
	m.editTarget(len(m.targetList.all), target)
	m.takeTarget()
}

// pickAdd acts on one row of the menu or the builder.
func (m *Model) pickAdd(key string) {
	a := m.targetAdd
	query := a.choices.query()
	switch {
	case key == addTyped && a.kind < 0:
		if query == "" {
			m.targetAdd = nil
			m.editTarget(len(m.targetList.all), "")
			return
		}
		m.finishAdd(query)
	case strings.HasPrefix(key, addKnown):
		m.finishAdd(strings.TrimPrefix(key, addKnown))
	case strings.HasPrefix(key, addKind):
		kind, _ := strconv.Atoi(strings.TrimPrefix(key, addKind))
		m.startBuilder(kind)
	default:
		value := strings.TrimPrefix(key, addValue)
		if key == addTyped {
			if query == "" {
				m.status = ""
				m.fail = "type " + m.addAsk() + " first, or choose a row with up/down"
				return
			}
			value = query
		}
		if strings.Contains(value, "/") {
			m.fail = "one part of a target cannot contain /; type the parts one by one"
			return
		}
		m.clearMessages()
		if target, complete := a.choose(value, key == addDone); complete {
			m.finishAdd(target)
			return
		}
		m.resetAddLine()
	}
}

// saveAdd is ctrl+s in the menu or the builder. A typed line in the menu is taken like a typed target, and
// a typed value in the builder only when it completes the target; a selected row is never taken. It reports whether the list may
// be saved; otherwise the reason is shown and nothing is dropped.
func (m *Model) saveAdd() bool {
	a := m.targetAdd
	if a.kind < 0 {
		// The typed line is taken as typed; a filter that found a row is not a choice of that row.
		if query := a.choices.query(); query != "" {
			m.finishAdd(query)
			return m.targetEdit < 0
		}
		m.targetAdd = nil
		return true
	}
	// Only a typed value completes a build here; a row that is merely selected is no choice, so a pattern
	// such as * never reaches the list without enter or typing it.
	if query := a.choices.query(); query != "" {
		key := addTyped
		if _, ok := a.text[addValue+query]; ok {
			key = addValue + query
		}
		probe := *a
		probe.chosen = slices.Clone(a.chosen)
		probe.history = slices.Clone(a.history)
		if _, complete := probe.choose(query, false); complete {
			m.pickAdd(key)
			return m.targetAdd == nil && m.targetEdit < 0
		}
	}
	kind := m.targetMetadata().Kinds[a.kind].Name
	m.status = ""
	m.fail = "the new " + kind + " is not complete yet: choose " + m.addAsk() +
		" by typing it or with enter, or esc to cancel it; nothing was saved"
	return false
}

// addAsk names what the builder asks for next.
func (m *Model) addAsk() string {
	names, fixed, _ := m.targetAdd.step()
	parts := slices.Clone(names)
	all := slices.Index(fixed, "*")
	if all >= 0 {
		fixed = slices.Delete(fixed, all, all+1)
	}
	if len(fixed) > 0 {
		parts = append(parts, "one of "+strings.Join(fixed, ", "))
	}
	if all >= 0 {
		parts = append(parts, "* (all)")
	}
	return strings.Join(parts, " or ")
}

// updateAdd handles the menu and the builder. Every printable key belongs to the typed line.
func (m *Model) updateAdd(key tea.KeyMsg) tea.Cmd {
	a := m.targetAdd
	switch key.String() {
	case "esc":
		m.targetAdd = nil
		m.clearMessages()
	case "enter":
		if choice, ok := a.choices.selected(); ok {
			m.pickAdd(choice)
		}
	case "up":
		a.choices.move(-1)
	case "down":
		a.choices.move(1)
	case "pgup", "pgdown", "home", "end":
		start, end := m.addWindow()
		a.choices.page(key.String(), start, end)
	case "backspace":
		if a.kind >= 0 && a.choices.query() == "" {
			m.stepBack()
			return nil
		}
		fallthrough
	default:
		before := a.choices.query()
		cmd := a.choices.updateFilter(key)
		if a.choices.query() != before {
			m.refreshAdd()
		}
		return cmd
	}
	return nil
}

// addFrame is everything of the add menu or the builder except its rows.
func (m *Model) addFrame() (string, string) {
	a := m.targetAdd
	metadata := m.targetMetadata()
	save := "ctrl+s save"
	if m.wizard != nil {
		save = "ctrl+s next step"
	}
	var head strings.Builder
	label := "filter or type: "
	keys := "type to filter or to type a target · up/down move · enter choose · " + save + " · esc cancel entry"
	if a.kind < 0 {
		head.WriteString(m.wrapped(titleStyle, "Add a "+metadata.Label) + "\n")
		hint := "build a new target step by step, or type it"
		if len(a.known) > 0 {
			hint = "take a target another connection of " + m.formService() + " uses, " + hint
		}
		head.WriteString(m.wrapped(hintStyle, hint) + "\n")
	} else {
		kind := metadata.Kinds[a.kind]
		ask := m.addAsk()
		head.WriteString(m.wrapped(titleStyle, "New "+kind.Name+" · "+ask) + "\n")
		so := strings.Join(a.chosen, "/")
		if so != "" {
			so += "/"
		}
		var forms []string
		for _, form := range a.forms {
			forms = append(forms, strings.Join(form, "/"))
		}
		head.WriteString(m.wrapped(hintStyle, "so far: "+so+"   forms: "+strings.Join(forms, ", ")) + "\n")
		label = "value: "
		keys = "type a value or filter · up/down move · enter choose · backspace on an empty line steps back · " +
			save + " · esc cancel entry"
	}
	head.WriteString(m.searchLine(&a.choices, label) + "\n")
	if len(a.choices.matches) == 0 {
		head.WriteString(m.wrapped(hintStyle, "(nothing to choose; type a value)") + "\n")
	}
	return head.String(), m.hint(keys) + m.notes()
}

func (m *Model) addRow(i int) string {
	a := m.targetAdd
	return m.row(i == a.choices.cursor, a.text[a.choices.matches[i]])
}

func (m *Model) addWindow() (int, int) {
	header, footer := m.addFrame()
	return m.windowIn(&m.targetAdd.choices, header, footer, m.addRow)
}

// addView draws the add menu or the builder.
func (m *Model) addView() string {
	header, footer := m.addFrame()
	var b strings.Builder
	b.WriteString(header)
	start, end := m.addWindow()
	for i := start; i < end; i++ {
		b.WriteString(m.addRow(i) + "\n")
	}
	b.WriteString(footer)
	return b.String()
}
