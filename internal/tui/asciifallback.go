package tui

import "strings"

// asciiGlyphs maps every non-ASCII symbol the screens draw to one ASCII character, so a frame keeps its
// width cell for cell. Umlauts and ß are not listed: a locale without UTF-8 still has to show them as they are
// typed. The double frame is not listed either: frameChars picks its ASCII form at the source.
var asciiGlyphs = strings.NewReplacer(
	"─", "-", "│", "|", "╭", "+", "╮", "+", "╰", "+", "╯", "+",
	"▌", "|", "▸", ">", "▾", "v", "■", "#", "●", "o", "⚠", "!", "✓", "v", "✗", "x",
	"░", "-", "▒", "=", "▓", "+", "█", "#",
	"…", "~", "·", ".", "×", "x", "•", "*", "›", ">", "—", "-",
	"←", "<", "→", ">", "↑", "^", "↓", "v",
	"„", "\"", "“", "\"", "‚", "'", "‘", "'", "”", "\"", "’", "'", "�", "?",
)

// asciiOnly is s for a terminal without UTF-8: the symbols replaced by their ASCII form. The replaced
// characters are all outside ASCII, so escape sequences pass unchanged.
func asciiOnly(s string) string {
	if utf8Terminal() {
		return s
	}
	return asciiGlyphs.Replace(s)
}
