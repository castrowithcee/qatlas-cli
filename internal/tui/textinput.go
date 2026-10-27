package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// inputStyles are the styles of every text field: plain prompt and text, the placeholder and suggestion in
// grey 240, and a static reverse cursor in the text colour. The library's own defaults colour the prompt and
// a blurred text and let the cursor blink. Blinking would need the blink command threaded through Init,
// through every focus change and through the timer messages this event loop drops, and a missed tick would
// leave the cursor invisible.
func inputStyles() textinput.Styles {
	state := textinput.StyleState{
		Placeholder: lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		Suggestion:  lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
	}
	return textinput.Styles{Focused: state, Blurred: state}
}

// inputKeys are the keys of every text field: the library's, without the word deletion on ctrl+backspace and
// ctrl+delete it added, so a field edits as it always did.
func inputKeys() textinput.KeyMap {
	keys := textinput.DefaultKeyMap()
	keys.DeleteWordBackward = key.NewBinding(key.WithKeys("alt+backspace", "ctrl+w"))
	keys.DeleteWordForward = key.NewBinding(key.WithKeys("alt+delete", "alt+d"))
	return keys
}

// inputBinds reports whether a text field binds k to an edit of its own.
func inputBinds(k tea.KeyPressMsg) bool {
	keys := inputKeys()
	return key.Matches(k, keys.CharacterForward, keys.CharacterBackward, keys.WordForward, keys.WordBackward,
		keys.DeleteWordBackward, keys.DeleteWordForward, keys.DeleteAfterCursor, keys.DeleteBeforeCursor,
		keys.DeleteCharacterBackward, keys.DeleteCharacterForward, keys.LineStart, keys.LineEnd, keys.Paste,
		keys.AcceptSuggestion, keys.NextSuggestion, keys.PrevSuggestion)
}

// newTextInput is the only place text fields are created, so library defaults are set once.
func newTextInput() textinput.Model {
	in := textinput.New()
	in.SetStyles(inputStyles())
	in.KeyMap = inputKeys()
	return in
}

// setInputWidth is the only place a text field width is set.
func setInputWidth(in *textinput.Model, width int) {
	in.SetWidth(width)
}

// inputView draws a text field. An empty field shows its placeholder whole, cut with "…" only at a set width:
// the library's own placeholder keeps no more than the first character of a field without a width.
func inputView(in textinput.Model) string {
	if in.Value() != "" || in.Placeholder == "" {
		return in.View()
	}
	style := in.Styles().Focused.Placeholder.Inline(true)
	prompt := in.Styles().Focused.Prompt.Render(in.Prompt)
	first, _ := ansi.FirstGraphemeCluster(in.Placeholder, ansi.GraphemeWidth)
	rest := in.Placeholder[len(first):]
	cursor := style.Render(first)
	if in.Focused() {
		cursor = lipgloss.NewStyle().Inline(true).Reverse(true).Render(first)
	}
	if in.Width() < 1 {
		return prompt + cursor + style.Render(rest)
	}
	width := in.Width() - lipgloss.Width(prompt) - lipgloss.Width(cursor)
	rest = ansi.Truncate(rest, width, "…")
	return prompt + cursor + style.Render(rest) + strings.Repeat(" ", max(0, width-lipgloss.Width(rest)))
}
