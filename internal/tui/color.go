package tui

import "github.com/charmbracelet/lipgloss"

// colour is a palette colour: a hex string or an ANSI index, empty for none.
type colour string

// lip is the only place that turns a colour into a Lip Gloss colour.
func (c colour) lip() lipgloss.TerminalColor { return lipgloss.Color(string(c)) }
