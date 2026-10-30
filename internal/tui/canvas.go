package tui

import (
	"regexp"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// canvasPrefix is the escape sequence that sets the base background and the text colour under the current
// colour profile; empty where the terminal shows no colour.
func (t theme) canvasPrefix() string {
	if noColour() {
		return ""
	}
	const probe = "x"
	out := lipgloss.NewStyle().Foreground(t.palette.text.lip()).Background(t.palette.base.lip()).Render(probe)
	prefix, _, _ := strings.Cut(out, probe)
	return prefix
}

// resetCodes are the SGR parameters that drop a colour; extendedCodes open a colour with arguments.
var (
	resetCodes    = []string{"", "0", "39", "49"}
	extendedCodes = []string{"38", "48"}
)

// sgrIndexed is the mode parameter that makes 38 and 48 take one more argument (a palette index); any other
// mode takes four (a true colour).
const sgrIndexed = "5"

var sgrSequence = regexp.MustCompile("\x1b\\[[0-9;]*m")

// resetsColour reports whether an SGR sequence drops the background or the foreground: a full reset (also
// the empty form), or the default-colour codes. The extended colour codes carry arguments that may be 0.
func resetsColour(seq string) bool {
	params := strings.Split(seq[2:len(seq)-1], ";")
	for i := 0; i < len(params); i++ {
		p := params[i]
		if slices.Contains(resetCodes, p) {
			return true
		}
		if slices.Contains(extendedCodes, p) {
			if i+1 < len(params) && params[i+1] == sgrIndexed {
				i += 2
			} else {
				i += 4
			}
		}
	}
	return false
}

// paintCanvas gives every cell of the width by height screen the base colours: the prefix opens each line
// and follows every inner reset, lines are padded with spaces to width, and missing lines are added. Only
// escape sequences and spaces are added, so no cell moves.
func paintCanvas(view, prefix string, width, height int) string {
	if prefix == "" {
		return view
	}
	lines := strings.Split(view, "\n")
	for height > len(lines) {
		lines = append(lines, "")
	}
	for i, line := range lines {
		line = sgrSequence.ReplaceAllStringFunc(line, func(seq string) string {
			if resetsColour(seq) {
				return seq + prefix
			}
			return seq
		})
		if pad := width - ansi.StringWidth(line); pad > 0 {
			line += prefix + strings.Repeat(" ", pad)
		}
		lines[i] = prefix + line + "\x1b[0m"
	}
	return strings.Join(lines, "\n")
}
