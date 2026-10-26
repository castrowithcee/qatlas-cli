package tui

import (
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
)

// newTextInput is the only place text fields are created, so library defaults are set once. The cursor is
// static: it is drawn on every frame the view already paints. Blinking would need the blink command
// threaded through Init, through every focus change and through the timer messages this event loop drops,
// and a missed tick would leave the cursor invisible.
func newTextInput() textinput.Model {
	in := textinput.New()
	in.Cursor.SetMode(cursor.CursorStatic)
	return in
}

// setInputWidth is the only place a text field width is set.
func setInputWidth(in *textinput.Model, width int) {
	in.Width = width
}
