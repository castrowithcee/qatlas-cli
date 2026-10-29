package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// offerSettingsTakeover is the one-time question of the first unlock after the settings moved into the vault
// (see manage.Service.PrepareSettingsTakeover, which decides whether it is asked and does the work). It
// shows the old values on stderr, reads the answer from the terminal, and reports the backup of the
// configuration file on stderr. Nothing goes to stdout, and nothing is a failure of the unlock: a problem is
// a warning. Without a terminal to answer on nothing is asked and nothing is changed.
func offerSettingsTakeover(c *cobra.Command, opts *Options, reg *capability.Registry, v *vault.Vault) {
	stderr := c.ErrOrStderr()
	warn := func(text string) {
		fmt.Fprintf(stderr, "qatlas: warning: %s\n", opts.Redactor.Apply(text))
	}
	path, err := config.Path(opts.Config)
	if err != nil {
		return
	}
	take, notice, err := vaultService(opts, config.NewStore(path, reg), v).PrepareSettingsTakeover()
	if err != nil {
		warn(err.Error())
		return
	}
	if notice != "" {
		warn(notice)
	}
	if take == nil {
		return
	}
	yes := false
	if take.Ask() {
		fmt.Fprintln(stderr, "qatlas: the configuration file still holds settings that now live in the vault:")
		for _, o := range take.Offered {
			fmt.Fprintf(stderr, "  %s: %s\n", o.Field, o.Value)
		}
		yes, err = readVaultConfirm("take these values over into the vault? [y/N] ")
		if errors.Is(err, vault.ErrNoTerminal) {
			return
		}
		if err != nil {
			warn(err.Error())
			return
		}
	}
	res, err := take.Apply(yes)
	if err != nil {
		warn(err.Error())
		return
	}
	if res.Warning != "" {
		warn(res.Warning)
	}
	switch {
	case res.Backup != "" && yes:
		fmt.Fprintf(stderr, "qatlas: took the values over and removed them from the configuration file; backup: %s\n", res.Backup)
	case res.Backup != "":
		fmt.Fprintf(stderr, "qatlas: removed the old values from the configuration file; backup: %s\n", res.Backup)
	}
}
