package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// inputStyles are the styles of every text field: plain prompt and text, and the placeholder and suggestion in
// the hint style of the active theme. The library's own defaults colour the prompt and a blurred text. The
// cursor is the terminal's own (see cursorMark) and static: blinking would need the blink command threaded
// through Init, through every focus change and through the timer messages this event loop drops.
func inputStyles() textinput.Styles {
	state := textinput.StyleState{
		Placeholder: styles.hint,
		Suggestion:  styles.hint,
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
	in.SetVirtualCursor(false)
	in.KeyMap = inputKeys()
	return in
}

// setInputWidth is the only place a text field width is set.
func setInputWidth(in *textinput.Model, width int) {
	in.SetWidth(width)
}

// cursorMark is a zero-width escape sequence that inputView leaves in the cell where the terminal cursor
// belongs. Every layer that places a field (prefixes, panels, dialogs, the dimmed backdrop) keeps or drops it
// like any escape sequence, so the position found in the finished frame is the drawn one with no offset of
// its own to keep in step.
const cursorMark = "\x1b_qatlas-cursor\x1b\\"

// takeCursor removes the cursor marks from frame and returns the cell of the first one, or nil without a mark.
func takeCursor(frame string) (string, *tea.Cursor) {
	if !strings.Contains(frame, cursorMark) {
		return frame, nil
	}
	var found *tea.Cursor
	for y, line := range strings.Split(frame, "\n") {
		if before, _, ok := strings.Cut(line, cursorMark); ok {
			found = tea.NewCursor(ansi.StringWidth(before), y)
			found.Blink = false
			break
		}
	}
	return strings.ReplaceAll(frame, cursorMark, ""), found
}

// cursorColumn is the cell of the line in.View draws where the cursor stands, found by drawing the field once
// with the library's reverse cursor: the library's own Cursor ignores wide characters and a scrolled line.
func cursorColumn(in textinput.Model) int {
	in.SetVirtualCursor(true)
	line := in.View()
	buf := uv.NewScreenBuffer(max(ansi.StringWidth(line), 1), 1)
	uv.NewStyledString(line).Draw(buf, buf.Bounds())
	for x := range buf.Width() {
		if cell := buf.CellAt(x, 0); cell != nil && cell.Style.Attrs&uv.AttrReverse != 0 {
			return x
		}
	}
	return 0
}

// inputView draws a text field. An empty field shows its placeholder whole, cut with "…" only at a set width:
// the library's own placeholder keeps no more than the first character of a field without a width. A focused
// field carries the cursorMark.
func inputView(in textinput.Model) string {
	// The styles are set again here so a theme change reaches fields built before it.
	in.SetStyles(inputStyles())
	if in.Value() != "" || in.Placeholder == "" {
		view := in.View()
		if !in.Focused() {
			return view
		}
		x := cursorColumn(in)
		return ansi.Truncate(view, x, "") + cursorMark + ansi.TruncateLeft(view, x, "")
	}
	style := in.Styles().Focused.Placeholder.Inline(true)
	prompt := in.Styles().Focused.Prompt.Render(in.Prompt)
	first, _ := ansi.FirstGraphemeCluster(in.Placeholder, ansi.GraphemeWidth)
	rest := in.Placeholder[len(first):]
	cursor := style.Render(first)
	if in.Focused() {
		prompt += cursorMark
	}
	if in.Width() < 1 {
		return prompt + cursor + style.Render(rest)
	}
	width := in.Width() - lipgloss.Width(prompt) - lipgloss.Width(cursor)
	rest = ansi.Truncate(rest, width, "…")
	return prompt + cursor + style.Render(rest) + strings.Repeat(" ", max(0, width-lipgloss.Width(rest)))
}
