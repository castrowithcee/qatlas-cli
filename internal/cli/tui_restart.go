package cli

import "github.com/castrowithcee/qatlas-cli/internal/tui"

// tuiRestart is how the editor restarts itself after an update, the way an MCP server does; nil where the
// platform has no such restart.
func tuiRestart() *tui.Restart {
	r := platformMCPRestart()
	if r.replaced == nil || r.exec == nil {
		return nil
	}
	return &tui.Restart{Replaced: r.replaced, Exec: r.exec}
}
