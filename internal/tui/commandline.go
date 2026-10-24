package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// The command line (':') runs the editor's recurring actions by name. It is a register, not a second set of
// keys: every command calls the function its key already calls, and a command that is not possible right
// now says why instead of doing nothing. A further command is one more entry in commands().

// command is one entry of the register.
type command struct {
	name    string
	desc    string
	aliases []string
	// hidden keeps the command out of the empty line's list; it still matches what is typed.
	hidden bool
	// unavailable returns the reason the command cannot run on m.screen now, or "" where it can.
	unavailable func(m *Model) string
	// run performs the action on the screen the command line was opened from.
	run func(m *Model) tea.Cmd
}

// commandLine is the open command line: the screen it covers, the typed text and the chosen match.
type commandLine struct {
	from   screen
	input  textinput.Model
	cursor int
	msg    string
}

// commands is the register, in the order an empty line lists it.
func (m *Model) commands() []command {
	var list []command
	for a := area(0); a < areaCount; a++ {
		title := strings.ToLower(a.key())
		first := areaSections[a][0]
		list = append(list, command{
			name:        "go " + title,
			desc:        tr(tCmdGoArea, a.title(), int(a)+1),
			aliases:     []string{title},
			unavailable: onScreens("switch areas", screenNav, screenList, screenLogs),
			run:         func(m *Model) tea.Cmd { return m.openSection(first) },
		})
		for _, s := range areaSections[a] {
			// The sub-tabs keep the names the sections had before the areas, so older habits still work; the
			// empty line lists only the areas.
			name := strings.ToLower(s.key())
			if name == title {
				continue
			}
			aliases := []string{name}
			if name == "update" {
				// The bare word belongs to the command that installs the release.
				aliases = nil
			}
			list = append(list, command{
				name:        "go " + name,
				desc:        tr(tCmdGoSection, a.title(), s.title(), int(a)+1),
				aliases:     aliases,
				hidden:      true,
				unavailable: onScreens("switch areas", screenNav, screenList, screenLogs),
				run:         func(m *Model) tea.Cmd { return m.openSection(s) },
			})
		}
	}
	list = append(list, m.selftestCommands()...)
	return append(list,
		command{
			name:        "setup",
			desc:        tr(tCmdSetup),
			aliases:     []string{"guided setup", "new connection"},
			unavailable: onScreens("start the setup", screenNav, screenList),
			run:         func(m *Model) tea.Cmd { m.askSetupTemplate(); return nil },
		},
		command{
			name:    "renames",
			desc:    tr(tCmdRenames),
			aliases: []string{"adopt", "rename leftovers"},
			unavailable: func(m *Model) string {
				if r := onScreens("review renames", screenList)(m); r != "" {
					return r
				}
				switch m.section {
				case sectionApprovals, sectionConnections, sectionCredentials:
					return ""
				}
				return tr(tCmdRenamesWhere)
			},
			run: func(m *Model) tea.Cmd { return m.openFindings() },
		},
		command{
			name:    "update",
			desc:    tr(tCmdUpdate),
			aliases: []string{"upgrade"},
			unavailable: func(m *Model) string {
				if r := onScreens("update", screenNav, screenList)(m); r != "" {
					return r
				}
				return m.updateUnavailable()
			},
			run: func(m *Model) tea.Cmd { m.askUpdate(); return nil },
		},
		command{
			name:        "export",
			desc:        tr(tCmdExport),
			aliases:     []string{"export config", "backup"},
			unavailable: onScreens("export", screenNav, screenList),
			run:         func(m *Model) tea.Cmd { return m.askExport() },
		},
		command{
			name:        "import",
			desc:        tr(tCmdImport),
			aliases:     []string{"import config", "restore"},
			unavailable: onScreens("import", screenNav, screenList),
			run:         func(m *Model) tea.Cmd { return m.askImport() },
		},
		command{
			name:        "view heatmap",
			desc:        tr(tCmdHeat),
			aliases:     []string{"heatmap"},
			unavailable: onScreens("change the view", screenLogs),
			run:         func(m *Model) tea.Cmd { return m.setHeat(true) },
		},
		command{
			name:        "view table",
			desc:        tr(tCmdTable),
			aliases:     []string{"table"},
			unavailable: onScreens("change the view", screenLogs),
			run:         func(m *Model) tea.Cmd { return m.setHeat(false) },
		},
		command{
			name:        "lock vault",
			desc:        tr(tCmdLock),
			aliases:     []string{"lock"},
			unavailable: (*Model).lockUnavailable,
			run:         func(m *Model) tea.Cmd { return m.handleVaultLockKey() },
		},
		command{
			name:        "help",
			desc:        tr(tCmdHelp),
			unavailable: func(m *Model) string { return "" },
			run:         func(m *Model) tea.Cmd { m.openHelp(); return nil },
		},
		command{
			name:        "quit",
			desc:        tr(tCmdQuit),
			aliases:     []string{"exit"},
			unavailable: onScreens("quit", screenNav, screenList, screenLogs),
			run:         func(m *Model) tea.Cmd { return m.quit() },
		},
	)
}

// onScreens is the availability of a command whose key works on these screens only.
func onScreens(what string, screens ...screen) func(m *Model) string {
	return func(m *Model) string {
		for _, s := range screens {
			if m.screen == s {
				return ""
			}
		}
		return tr(tCmdCannot, trOr(textID("cmdwhat."+what), what))
	}
}

// updateUnavailable is why u would do nothing now.
func (m *Model) updateUnavailable() string {
	switch {
	case m.updater == nil:
		return tr(tUpdateBuildCannot)
	case m.updating:
		return tr(tUpdateRunning)
	case m.updated != "":
		return tr(tUpdateAlreadyDone)
	case !m.release.UpdateAvailable:
		return tr(tUpdateNoneKnown)
	}
	return ""
}

// unavailableFrom evaluates a command as if the editor were on screen from.
func (m *Model) unavailableFrom(c command, from screen) string {
	saved := m.screen
	m.screen = from
	defer func() { m.screen = saved }()
	return c.unavailable(m)
}

// matchCommands returns the commands matching query by name or alias, ignoring case: prefixes first, then
// other word starts, then any part of a word; each group keeps the register's order.
func matchCommands(list []command, query string) []command {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		var shown []command
		for _, c := range list {
			if !c.hidden {
				shown = append(shown, c)
			}
		}
		return shown
	}
	var groups [3][]command
	for _, c := range list {
		rank := 3
		for _, n := range append([]string{c.name}, c.aliases...) {
			n = strings.ToLower(n)
			switch {
			case strings.HasPrefix(n, query):
				rank = min(rank, 0)
			case strings.Contains(n, " "+query):
				rank = min(rank, 1)
			case strings.Contains(n, query):
				rank = min(rank, 2)
			}
		}
		if rank < 3 {
			groups[rank] = append(groups[rank], c)
		}
	}
	return append(append(groups[0], groups[1]...), groups[2]...)
}

// openCommandLine shows the command line over the current screen.
func (m *Model) openCommandLine() {
	in := textinput.New()
	in.Prompt = ": "
	in.Focus()
	m.cmdline = &commandLine{from: m.screen, input: in}
	m.screen = screenCommand
	m.clearMessages()
}

// closeCommandLine returns to the screen it covered, unchanged.
func (m *Model) closeCommandLine() {
	m.screen = m.cmdline.from
	m.cmdline = nil
}

func (m *Model) updateCommandLine(key tea.KeyMsg) tea.Cmd {
	cl := m.cmdline
	matches := matchCommands(m.commands(), cl.input.Value())
	switch key.String() {
	case keyOf(actBack):
		m.closeCommandLine()
		return nil
	case "up", "shift+tab":
		cl.cursor = wrap(cl.cursor-1, max(len(matches), 1))
		cl.msg = ""
		return nil
	case "down", "tab":
		cl.cursor = wrap(cl.cursor+1, max(len(matches), 1))
		cl.msg = ""
		return nil
	case "enter":
		if len(matches) == 0 {
			cl.msg = tr(tCmdUnknown, strings.TrimSpace(cl.input.Value()))
			return nil
		}
		c := matches[min(cl.cursor, len(matches)-1)]
		if reason := m.unavailableFrom(c, cl.from); reason != "" {
			cl.msg = c.name + ": " + reason
			return nil
		}
		m.closeCommandLine()
		return c.run(m)
	}
	before := cl.input.Value()
	cl.input, _ = cl.input.Update(key)
	if cl.input.Value() != before {
		cl.cursor, cl.msg = 0, ""
	}
	return nil
}

// commandLineView draws the line, the matching commands (unavailable ones faint) and the reason of a
// refusal. The matches scroll with the choice where the workspace is short.
func (m *Model) commandLineView() string {
	cl := m.cmdline
	matches := matchCommands(m.commands(), cl.input.Value())
	foot := m.keyHint("type to search · up/down or tab choose · enter run · {back} close")
	if cl.msg != "" {
		foot = "\n" + m.wrapped(styles.fail, tr(tNoteError, cl.msg)) + foot
	}
	head := m.wrapped(styles.title, tr(tCmdHead)) + "\n\n" + cl.input.View() + "\n\n"
	room := max(m.height-strings.Count(head, "\n")-strings.Count(foot, "\n")-1, 1)
	if len(matches) == 0 {
		return head + m.wrapped(styles.hint, tr(tCmdNoMatch)) + "\n" + foot
	}
	cursor := min(cl.cursor, len(matches)-1)
	start := max(min(cursor-room/2, len(matches)-room), 0)
	end := min(start+room, len(matches))
	width := m.usable(2)
	var rows []string
	for i := start; i < end; i++ {
		c := matches[i]
		text := truncateCells(fmt.Sprintf("%-14s %s", c.name, c.desc), width)
		switch {
		case m.unavailableFrom(c, cl.from) != "":
			text = styles.hint.Render(text)
		case i == cursor:
			text = styles.active.Render(text)
		}
		prefix := "  "
		if i == cursor {
			prefix = "> "
		}
		rows = append(rows, prefix+text)
	}
	return head + strings.Join(rows, "\n") + "\n" + foot
}
