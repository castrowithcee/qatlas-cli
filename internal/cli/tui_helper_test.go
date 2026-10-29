package cli

import (
	"os"

	"golang.org/x/crypto/ssh"

	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
)

// runAsTUIEnv makes the test binary run 'qatlas tui' with an updater that reads a release server of the
// test, and any other command as qatlas itself, so a vault process it starts still works. The program
// that replaces it starts the same binary again with a newer version.
const (
	runAsTUIEnv     = "QATLAS_CLI_TEST_RUN_AS_TUI"
	tuiVersionEnv   = "QATLAS_CLI_TEST_TUI_VERSION"
	tuiReleasesEnv  = "QATLAS_CLI_TEST_TUI_RELEASES"
	tuiKeyEnv       = "QATLAS_CLI_TEST_TUI_KEY"
	tuiPassphraseIn = "canary-passphrase-restart-5d1f"
)

func runAsTUI() int {
	args := os.Args[1:]
	if len(args) == 0 || args[0] != "tui" {
		return Run(args, os.Stdout, os.Stderr)
	}
	version = os.Getenv(tuiVersionEnv)
	var keys []ssh.PublicKey
	if key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(os.Getenv(tuiKeyEnv))); err == nil {
		keys = append(keys, key)
	}
	opts := &Options{Redactor: &redact.Redactor{}, Input: os.Stdin,
		Updater: &selfupdate.Client{Version: version, BaseURL: os.Getenv(tuiReleasesEnv), SigningKeys: keys}}
	return run(newRootCommand(opts, sharedRegistry()), opts, args, os.Stdout, os.Stderr)
}
