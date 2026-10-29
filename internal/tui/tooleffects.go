package tui

import (
	"fmt"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// effectRowPrefix starts the name of an effect row of the tool picker. Like the group rows it contains a NUL
// byte, which no tool ID does, so a row never clashes with a tool and never reaches the ticks.
const effectRowPrefix = "\x00e:"

// toolEffects is the membership of the effect rows of the tool picker: for each effect the registered tools
// of the picker that have it, taken from the provider's metadata.
type toolEffects struct {
	effects    []config.Effect
	members    map[config.Effect][]string
	listedOnly map[string]bool
}

// newToolEffects builds the effect rows of a picker over choices, in the order of config.Effects. A choice
// the provider does not register has no effect and belongs to no row. It returns nil when no tool has one.
func newToolEffects(metadata config.ProviderMetadata, choices []string) *toolEffects {
	of := map[string]config.ToolMetadata{}
	for _, tool := range metadata.Tools {
		of[tool.ID] = tool
	}
	e := &toolEffects{members: map[config.Effect][]string{}, listedOnly: map[string]bool{}}
	for _, choice := range choices {
		if tool, ok := of[choice]; ok {
			e.members[tool.Effect] = append(e.members[tool.Effect], choice)
			e.listedOnly[choice] = tool.RequiresToolAllowList
		}
	}
	for _, effect := range config.Effects() {
		if len(e.members[effect]) > 0 {
			e.effects = append(e.effects, effect)
		}
	}
	if len(e.effects) == 0 {
		return nil
	}
	return e
}

func isEffectRow(name string) bool { return strings.HasPrefix(name, effectRowPrefix) }

// rows are the effect rows in display order, to put above the other entries.
func (e *toolEffects) rows() []string {
	rows := make([]string, 0, len(e.effects))
	for _, effect := range e.effects {
		rows = append(rows, effectRowPrefix+string(effect))
	}
	return rows
}

func (e *toolEffects) tools(row string) []string {
	return e.members[config.Effect(strings.TrimPrefix(row, effectRowPrefix))]
}

// matches wraps the filter of the picker: an effect row shows without a search and, with one, when one of
// its tools matches; every other entry is left to the wrapped filter.
func (e *toolEffects) matches(base func(name, query string) bool) func(name, query string) bool {
	return func(name, query string) bool {
		if !isEffectRow(name) {
			return base(name, query)
		}
		if query == "" {
			return true
		}
		for _, tool := range e.tools(name) {
			if base(tool, query) {
				return true
			}
		}
		return false
	}
}

// toggle ticks the targets of an effect row that are not listed only while one of them is unticked, and
// unticks all targets, listed only ones too, otherwise. A listed only tool is thus ticked one by one. The
// targets are narrowed to the shown tools while a search is typed (visible is then set).
func (e *toolEffects) toggle(row string, marks map[string]bool, visible func(string) bool) {
	var targets []string
	for _, tool := range e.tools(row) {
		if visible == nil || visible(tool) {
			targets = append(targets, tool)
		}
	}
	missing := false
	for _, tool := range targets {
		if !e.listedOnly[tool] && !marks[tool] {
			missing = true
		}
	}
	for _, tool := range targets {
		switch {
		case !missing:
			delete(marks, tool)
		case !e.listedOnly[tool]:
			marks[tool] = true
		}
	}
}

// rowText reads `[x] read 3/12`: all, some or none of the effect's tools ticked, and the count over all
// of its tools, also while a search is typed.
func (e *toolEffects) rowText(row string, marks map[string]bool) string {
	ticked, total := countTicks(e.tools(row), marks)
	box := "[-]"
	switch ticked {
	case 0:
		box = "[ ]"
	case total:
		box = "[x]"
	}
	return fmt.Sprintf("%s %s %d/%d", box, strings.TrimPrefix(row, effectRowPrefix), ticked, total)
}

// styledRow is rowText drawn for the screen: the box coloured, the effect in the accent colour as the title of
// a group, and its counts dimmed. Without colour it reads exactly as rowText does.
func (e *toolEffects) styledRow(row string, marks map[string]bool) string {
	ticked, total := countTicks(e.tools(row), marks)
	box := styles.active.Render("[-]")
	switch ticked {
	case 0:
		box = tickBox(false)
	case total:
		box = tickBox(true)
	}
	return box + " " + styles.active.Render(strings.TrimPrefix(row, effectRowPrefix)) +
		styles.hint.Render(fmt.Sprintf(" %d/%d", ticked, total))
}
