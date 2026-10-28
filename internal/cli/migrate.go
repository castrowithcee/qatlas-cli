package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// migrateConfig moves a configuration file of an older schema to the current one before any command reads
// it, so every way in migrates, 'qatlas mcp' included (see manage.Service.MigrateSchema). It never fails the
// command and writes only to stderr: stdout carries the command's answer, for 'qatlas mcp' its JSON-RPC.
// A hidden command is a process qatlas starts itself, such as the vault process, and leaves the file alone;
// so does every self-contained command, the selftest, which must never touch the installation.
func migrateConfig(c *cobra.Command, opts *Options, reg *capability.Registry) {
	if c.Hidden || selfContained(c) {
		return
	}
	path, err := config.Path(opts.Config)
	if err != nil {
		return
	}
	// Even under 'qatlas mcp' the migration runs before any MCP session exists, and the log keeps the mcp
	// path for the entries of a session.
	surface := connlog.SurfaceCLI
	// The vault beside the file, not the run's resolver: building that here would fix its prompting before
	// the command could make it unattended.
	v := vault.New(filepath.Dir(path))
	if opts.Secrets != nil {
		v = opts.Secrets.Vault()
	}
	svc := manage.ForVault(config.NewStore(path, reg), v, surface, opts.Redactor).WithVaultProcessSupport(vaultProcessSupported)
	m, err := svc.MigrateSchema()
	stderr := c.ErrOrStderr()
	// A file that does not load stays as it is; the command's own diagnostic names its problems and stays
	// the first line on stderr, as the stream contract wants of a failure.
	var invalid *config.InvalidError
	if errors.As(err, &invalid) {
		return
	}
	if err != nil {
		fmt.Fprintf(stderr, "qatlas: warning: %s\n", opts.Redactor.Apply(err.Error()))
		return
	}
	if m == nil {
		return
	}
	fmt.Fprintf(stderr, "qatlas: migrated %s to configuration schema %d: %s\n", m.Path, config.Version, migrationChange(m.From))
	fmt.Fprintf(stderr, "qatlas: the previous file is backed up as %s\n", m.Backup)
	if len(m.VaultCopies) > 0 {
		fmt.Fprintf(stderr, "qatlas: the vault files are backed up as %s\n", strings.Join(m.VaultCopies, ", "))
	}
	switch {
	case m.VaultIDs > 0 && m.Pending:
		fmt.Fprintf(stderr, "qatlas: the locked vault takes the ids of %d %s on its next unlock\n", m.VaultIDs,
			plural(m.VaultIDs, "credential", "credentials"))
	case m.VaultIDs > 0:
		fmt.Fprintf(stderr, "qatlas: %d vault %s now %s its credential's id\n", m.VaultIDs,
			plural(m.VaultIDs, "entry", "entries"), plural(m.VaultIDs, "carries", "carry"))
	}
	for _, r := range m.Renamings {
		fmt.Fprintf(stderr, "qatlas: %s.%s: the former %s %q is now written as %q\n", r.Section, r.Name, r.Kind, r.Old, r.New)
	}
	if len(m.Reapproval) > 0 {
		fmt.Fprintf(stderr, "qatlas: the effects of these connections now follow their tools, so each needs its vault "+
			"approval once more ('qatlas vault approve' or 'qatlas tui'): %s\n", strings.Join(m.Reapproval, ", "))
	}
	if len(m.Retargeted) > 0 {
		fmt.Fprintf(stderr, "qatlas: the targets of these connections are now written in their current form with the "+
			"same reach, so each needs its vault approval once more ('qatlas vault approve' or 'qatlas tui'): %s\n",
			strings.Join(m.Retargeted, ", "))
	}
	if len(m.OutdatedTargets) > 0 {
		fmt.Fprintf(stderr, "qatlas: no current form keeps the reach of the targets of these connections, so each "+
			"stays as it was and offers no tool until its targets are chosen again ('qatlas tui'); the rest of "+
			"the configuration stays usable: %s\n",
			strings.Join(m.OutdatedTargets, ", "))
	}
	fmt.Fprintln(stderr, "qatlas: to go back, copy the backups back in place and install the previous qatlas version")
	if m.Warning != "" {
		fmt.Fprintln(stderr, withQatlasPrefix(m.Warning))
	}
}

// migrationChange says what a migration from the schema from changed, one clause per schema step it passed.
func migrationChange(from int) string {
	steps := []string{
		2: "services, credentials, and connections now have stable ids",
		3: "a connection offers its tools by effects or by a tools list, and permissions is now called effects",
		4: "the targets of providers whose target forms changed meaning are now written in their current form",
	}
	var passed []string
	for to := max(from+1, 2); to < len(steps); to++ {
		passed = append(passed, steps[to])
	}
	return strings.Join(passed, "; ")
}
