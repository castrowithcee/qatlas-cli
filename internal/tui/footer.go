package tui

import (
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The footer is the last line of every screen: the keys of the screen at the left, its navigation at the
// right. A screen hands its key text over through keyHint; the frame draws it.

// footerItem is one group of the footer ("enter edit") with the rank that decides which groups stay when the
// width runs short.
type footerItem struct {
	text  string
	rank  int
	right bool
	// keep marks a group that is cut short before it is dropped.
	keep bool
	// plain is text that names no key: shown dimmed as a whole.
	plain bool
}

// Ranks of the footer groups, lowest stays longest. Groups that match no rule rank by position in the
// screen's own text, earlier first; every rank below footerOthers is kept ahead of them, every rank above
// is dropped before them.
const (
	footerHelp     = 0 // never dropped
	footerBack     = 1
	footerLock     = 2 // the only way to learn the lock key
	footerPrimary  = 3 // enter, y, n, the delete key, the digits of the areas and the key that reviews a finding
	footerSave     = 4
	footerCommand  = 5
	footerQuit     = 6
	footerOthers   = 10
	footerInfo     = 700
	footerPosition = 900  // right, before the navigation
	footerNavigate = 1000 // navigation stands right and goes first
)

// footerGroupRank is the rank of a group. Order of the rules is the order of importance.
func footerGroupRank(group string, index int) (rank int, right bool) {
	key, _, _ := strings.Cut(group, " ")
	switch {
	case key == keyLabel(actHelp) && key != "":
		return footerHelp, false
	case isNavigation(key):
		return footerNavigate + index, true
	case slices.Contains(strings.Split(key, "/"), keyName(keyOf(actBack))):
		return footerBack, false
	case key == keyName(keyOf(actSave)) || key == keyLabel(actSave):
		return footerSave, false
	case key == "q":
		return footerQuit, false
	case key == "enter" || key == "y" || key == "n" || key == "f" || key == keyName(keyOf(actDelete)) || isSectionRange(key):
		return footerPrimary, false
	case key == keyName(keyOf(actLock)):
		return footerLock, false
	case key == keyName(keyOf(actCommand)):
		return footerCommand, false
	case key == keyLabel(actInfo) || key == keyName(keyOf(actInfo)):
		return footerInfo, false
	}
	return footerOthers + index, false
}

// isSectionRange reports whether a key is a digit range ("1-6"), which opens an area.
func isSectionRange(key string) bool {
	return len(key) >= 3 && key[0] >= '0' && key[0] <= '9' && key[1] == '-'
}

// isNavigation reports whether the first word of a group names moving through the screen rather than
// acting on it.
func isNavigation(key string) bool {
	switch key {
	case "up/down", "left/right", "←/→", "↑/↓", "tab", "pgup/pgdown", "home/end":
		return true
	}
	return false
}

// footerItems splits the key text of a screen into its groups and ranks them. A screen that names no keys
// at all gets the help key.
func (m *Model) footerItems(text string) []footerItem {
	text = keyPlaceholders(strings.ReplaceAll(footerText(text), "{info}", m.infoLabel()))
	var items []footerItem
	hasHelp := false
	answering := m.questionScreen() || m.promptScreen()
	seen := map[string]bool{}
	for i, group := range strings.Split(text, " · ") {
		if group == "" || seen[group] {
			continue
		}
		seen[group] = true
		rank, right := footerGroupRank(group, i)
		// Every answer of a question stays, cut short if need be; only the keys of the editor around it give way.
		keep := answering && !right && rank != footerHelp && rank != footerCommand && rank != footerQuit && rank != footerInfo
		hasHelp = hasHelp || rank == footerHelp
		items = append(items, footerItem{text: group, rank: rank, right: right, keep: keep})
	}
	// A screen that names its keys without help does so on purpose: help is not reachable where text is typed.
	if !hasHelp && len(items) == 0 && keyLabel(actHelp) != "" {
		items = append(items, footerItem{text: keyLabel(actHelp) + " " + tr(tHeaderHelp), rank: footerHelp})
	}
	if m.screenPos != "" {
		items = append(items, footerItem{text: m.screenPos, rank: footerPosition, right: true, plain: true})
	}
	return items
}

// fitFooter drops groups, highest rank first and later before earlier, until the line fits width; groups
// that must stay are cut short instead, the longest first. The remaining groups keep their order.
func fitFooter(items []footerItem, width int) []footerItem {
	for footerWidth(items) > width {
		drop := -1
		for i, item := range items {
			if item.rank > footerHelp && !item.keep && (drop < 0 || item.rank >= items[drop].rank) {
				drop = i
			}
		}
		if drop >= 0 {
			items = append(items[:drop:drop], items[drop+1:]...)
			continue
		}
		cut := -1
		for i, item := range items {
			if item.keep && lipgloss.Width(item.text) > 6 && (cut < 0 || lipgloss.Width(item.text) > lipgloss.Width(items[cut].text)) {
				cut = i
			}
		}
		if cut < 0 {
			break
		}
		items = append([]footerItem{}, items...)
		items[cut].text = truncateCells(items[cut].text, lipgloss.Width(items[cut].text)-1)
	}
	return items
}

const footerGap = "  "

func footerWidth(items []footerItem) int {
	total, left, right := 0, 0, 0
	for _, item := range items {
		w := lipgloss.Width(item.text)
		if item.right {
			right += w + len(footerGap)
		} else {
			left += w + len(footerGap)
		}
	}
	if left > 0 {
		left -= len(footerGap)
	}
	if right > 0 {
		right -= len(footerGap)
	}
	total = left + right
	if left > 0 && right > 0 {
		total += len(footerGap)
	}
	return total
}

// footerLine draws the footer: the key of a group in the accent colour, its action dimmed. It notes where the
// groups stand, for the dialogs whose keys are buttons.
func (m *Model) footerLine() string {
	width := max(m.termWidth, 1)
	items := fitFooter(m.footerItems(m.screenKeys), width)
	m.footerSpans = nil
	var left, right []footerItem
	for _, item := range items {
		if item.right {
			right = append(right, item)
		} else {
			left = append(left, item)
		}
	}
	rightWidth := footerWidth(right)
	x := 0
	var b strings.Builder
	place := func(group []footerItem) {
		for i, item := range group {
			if i > 0 {
				b.WriteString(footerGap)
				x += len(footerGap)
			}
			w := lipgloss.Width(item.text)
			m.footerSpans = append(m.footerSpans, footerSpan{x, min(x+w, width), footerKey(item.text)})
			if item.plain {
				b.WriteString(styles.hint.Render(item.text))
			} else {
				b.WriteString(keyHintGroupFoot(item.text))
			}
			x += w
		}
	}
	place(left)
	if len(right) > 0 {
		pad := max(width-rightWidth-x, 1)
		b.WriteString(strings.Repeat(" ", pad))
		x += pad
		place(right)
	}
	return clipCells(b.String(), width)
}

// keyHintGroupFoot styles one "key action" group of the footer.
func keyHintGroupFoot(group string) string {
	key, action, ok := strings.Cut(group, " ")
	if !ok {
		return styles.footKey.Render(key)
	}
	return styles.footKey.Render(key) + styles.hint.Render(" "+action)
}
