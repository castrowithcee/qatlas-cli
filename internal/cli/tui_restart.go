package cli

import "github.com/castrowithcee/qatlas-cli/internal/tui"

// tuiRestart is how the editor restarts itself after an update, the way an MCP server does; nil where the
// platform has no such restart.
func tuiRestart(args []string) *tui.Restart {
	r := platformMCPRestart()
	if r.replaced == nil || r.exec == nil {
		return nil
	}
	return &tui.Restart{Replaced: r.replaced, Args: args, Exec: r.exec}
}

// restartArgs is what the program after a self-restart starts with: the subcommand tui, so it opens the editor
// whatever its bare call does, and --config when this call had it, so it opens the same configuration. Other
// flags are left out: --agent cannot reach the editor, and the rest do not change what it opens.
func restartArgs(opts *Options) []string {
	args := []string{"tui"}
	if opts.Config != "" {
		args = append(args, "--config", opts.Config)
	}
	return args
}
