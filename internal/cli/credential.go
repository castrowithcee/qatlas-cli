package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// maxSecretBytes bounds what a piped secret may be, so a wrong redirection cannot pull a whole file into
// the credential store. No API token is anywhere near this large.
const maxSecretBytes = 8 << 10

func newCredentialCommand(opts *Options, reg *capability.Registry) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "credential",
		Short: "Manage the secrets of a keyring or vault credential",
		Long: "A credential of type keyring keeps its secrets in the system keyring, the credential store\n" +
			"the operating system already provides: Secret Service on Linux (for example GNOME Keyring or\n" +
			"KWallet), the macOS Keychain, or the Windows Credential Manager. It is the recommended place\n" +
			"for secrets on a workstation: there is nothing to set up, no variable to export, and nothing\n" +
			"lands in the configuration file.\n\n" +
			"A credential of type vault keeps its secrets in the vault instead: a directory beside the\n" +
			"configuration, unencrypted or encrypted to a passphrase, for a machine without a usable system\n" +
			"keyring. 'qatlas vault status' shows whether it exists, is encrypted, and is unlocked; 'qatlas\n" +
			"vault encrypt', 'qatlas vault passphrase', and 'qatlas vault decrypt' switch its encryption on,\n" +
			"change its passphrase, and switch it off again.\n\n" +
			"The other sources stay separate. A set variable QATLAS_<CREDENTIAL>_<ROLE> overrides a stored\n" +
			"secret, which suits CI and containers. QATLAS_CREDENTIAL_STORE=none switches the system keyring\n" +
			"off for a run.\n\n" +
			"These commands write and remove the entries. No command ever shows a stored secret back, not\n" +
			"even masked; 'qatlas config validate --secrets' shows which source delivers each role.\n\n" +
			"Both commands manage a credential's secret and run only from an interactive terminal, keyring\n" +
			"credentials included; where the vault this configuration uses is encrypted they also ask for its\n" +
			"passphrase there, once per command and before doing anything, whatever credential they target. No\n" +
			"terminal fails with the code admin-required, a wrong passphrase with usage, both before anything\n" +
			"is touched; an agent never runs them.\n\n" +
			"Every call into the system keyring ends within 10 seconds, and within the 60 seconds of an\n" +
			"invoke. On Linux a session without a desktop, such as SSH, or the MCP broker never waits for\n" +
			"an unlock prompt: a locked keyring ends at once as locked, and one without a reachable session\n" +
			"bus as unavailable, without starting one. After such a failure the keyring is not asked again\n" +
			"for 60 seconds, so an invoke asks it once and the MCP broker asks again after that. The message\n" +
			"names the next step: unlock the keyring in a desktop session of the same user, or export\n" +
			"QATLAS_<CREDENTIAL>_<ROLE> for the session.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error { return c.Help() },
	}

	set := &cobra.Command{
		Use:   "set <credential> <role>",
		Short: "Store the secret of one credential role",
		Long: "The secret is read from standard input, so it never appears in the command line or in the\n" +
			"shell history:\n\n" +
			"    printf %s \"$TOKEN\" | qatlas credential set wiki-reader token-id\n" +
			"    qatlas credential set wiki-reader token-id < token.txt\n\n" +
			"A keyring credential's secret goes into the system keyring. When the keyring is locked, cannot\n" +
			"be reached, or is switched off, the command fails rather than falling back silently, and says\n" +
			"what to do on this platform, or points at the vault as the way out on a machine without one.\n\n" +
			"A vault credential's secret goes into the vault. Storing the very first secret the vault ever\n" +
			"holds asks, on the terminal, for a passphrase to encrypt it with; leaving that empty keeps the\n" +
			"vault unencrypted. Every later secret still needs that passphrase to store, asked once by the\n" +
			"admin check every management command runs before it does anything, and goes straight into the\n" +
			"unlocked vault rather than a pending entry. Where a vault process holds the vault unlocked, it\n" +
			"is handed the new secret too, only once it proved that it holds the vault's key; should that\n" +
			"fail, the secret stays stored and a warning says how to reload the process.\n\n" +
			"Success is silent, except for a warning when an environment variable would override what was\n" +
			"just stored.",
		Args: exactlyTwoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			return setCredential(c, opts, reg, args[0], args[1])
		},
	}

	remove := &cobra.Command{
		Use:   "delete <credential> <role>",
		Short: "Remove the secret of one credential role",
		Long: "For a keyring credential, the entry is removed from the system keyring, and from a plaintext\n" +
			"credentials.yaml left over from an earlier version, if any. For a vault credential, it is removed\n" +
			"from the vault, unlocked first by this command's own admin check if it was encrypted and locked,\n" +
			"and from a vault process that holds the vault unlocked.\n" +
			"An environment variable is not touched: it belongs to the shell, not to qatlas. When the keyring\n" +
			"is locked or cannot be reached, the command says so and what to do, and never reports a secret\n" +
			"as removed that may still be stored.",
		Args: exactlyTwoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			return deleteCredential(c, opts, reg, args[0], args[1])
		},
	}

	cmd.AddCommand(set, remove)
	return cmd
}

func setCredential(c *cobra.Command, opts *Options, reg *capability.Registry, name, role string) error {
	if err := requireAdmin(opts); err != nil {
		return err
	}
	cfg, cred, err := storableCredential(opts, reg, name, role)
	if err != nil {
		return err
	}
	secrets, err := opts.resolver()
	if err != nil {
		return err
	}

	value, err := readSecret(c)
	if err != nil {
		return err
	}
	// The value is registered before anything can fail, so no later message can carry it.
	opts.Redactor.Add(value)

	out, err := credentialService(opts, secrets).SetSecret(contextOrBackground(c.Context()), cfg, manage.SecretWrite{
		Name: name, Entry: cred, Role: role, Value: value, Offer: offerVaultPassphrase,
		Approval: manage.ApprovalNone,
	})
	if err != nil {
		var invalid *manage.ValueError
		if errors.As(err, &invalid) {
			return &UsageError{err}
		}
		if cred.Type != config.CredentialTypeVault &&
			(errors.Is(err, secret.ErrUnavailable) || errors.Is(err, secret.ErrDisabled)) {
			// The advice is built from the class of the failure, never from what the platform said.
			return &UsageError{fmt.Errorf("cannot store the secret for %s.%s: %s, then run the command "+
				"again; or export %s; or, on a machine without a usable keyring, change this credential to "+
				"type vault in 'qatlas tui' or config.yaml and store it there", name, role,
				secret.StoreAdvice(secret.StoreStateOf(err), runtime.GOOS), secret.DerivedEnvName(name, role))}
		}
		return classifyUserError(err)
	}
	if warning := withQatlasPrefix(out.ProcessWarning); warning != "" {
		fmt.Fprintln(c.ErrOrStderr(), warning)
	}

	// Overriding is allowed, so a shadowing variable must be named the moment it starts shadowing. A forward
	// credential reads no variable, so nothing shadows it.
	if env := secret.DerivedEnvName(name, role); !cred.Forward && secrets.Lookup(env) {
		fmt.Fprintf(c.ErrOrStderr(),
			"qatlas: warning: %s is set and overrides what was just stored for %s.%s\n", env, name, role)
	}
	return nil
}

// credentialService is the management service that stores and removes the secrets of 'credential set' and
// 'credential delete' in secrets. It needs no configuration store: the command loaded the configuration
// itself. The vault process switch is read when the service is built, so a test lever set before the
// command runs is honoured.
func credentialService(opts *Options, secrets *secret.Resolver) *manage.Service {
	return manage.New(nil, secrets, connlog.SurfaceCLI, opts.Redactor).WithVaultProcessSupport(vaultProcessSupported)
}

// offerVaultPassphrase is the offer 'credential set' makes for a vault's very first secret: a passphrase
// entered twice becomes the vault's encryption; an empty one, or no terminal to ask on, leaves the vault
// unencrypted.
func offerVaultPassphrase(prompt string) (string, error) {
	passphrase, err := readVaultPassphrase(prompt)
	if err != nil {
		if errors.Is(err, vault.ErrNoTerminal) {
			return "", nil
		}
		return "", err
	}
	if passphrase == "" {
		return "", nil
	}
	confirm, err := readVaultPassphrase("confirm the passphrase: ")
	if err != nil {
		return "", err
	}
	if confirm != passphrase {
		return "", &UsageError{errors.New("the two passphrases did not match; run the command again")}
	}
	return passphrase, nil
}

func deleteCredential(c *cobra.Command, opts *Options, reg *capability.Registry, name, role string) error {
	if err := requireAdmin(opts); err != nil {
		return err
	}
	cfg, cred, err := storableCredential(opts, reg, name, role)
	if err != nil {
		return err
	}
	secrets, err := opts.resolver()
	if err != nil {
		return err
	}

	out, err := credentialService(opts, secrets).DeleteSecret(contextOrBackground(c.Context()), cfg, manage.SecretWrite{
		Name: name, Entry: cred, Role: role, Approval: manage.ApprovalNone,
	})
	if err != nil {
		if errors.Is(err, secret.ErrNoEntry) {
			return &UsageError{fmt.Errorf("no stored secret for %s.%s", name, role)}
		}
		// A delete that could not clear every place says so, including what it did clear. It is never a
		// silent success.
		if cred.Type != config.CredentialTypeVault && errors.Is(err, secret.ErrUnavailable) {
			err = fmt.Errorf("%w; %s, then run the command again", err,
				secret.StoreAdvice(secret.StoreStateOf(err), runtime.GOOS))
		}
		return classifyUserError(err)
	}
	if warning := withQatlasPrefix(out.ProcessWarning); warning != "" {
		fmt.Fprintln(c.ErrOrStderr(), warning)
	}
	// A switched-off store was never consulted, so the delete says nothing about what may sit in it. Left
	// unsaid, a silent success reads as "the secret is gone everywhere", which is the one thing it does
	// not mean.
	if cred.Type != config.CredentialTypeVault && secrets.StoreSkipped() {
		fmt.Fprintf(c.ErrOrStderr(),
			"qatlas: warning: %s=%s, so the credential store was not touched and may still hold the "+
				"secret for %s.%s\n", secret.StoreSelector, secret.StoreNone, name, role)
	}
	if env := secret.DerivedEnvName(name, role); secrets.Lookup(env) {
		fmt.Fprintf(c.ErrOrStderr(),
			"qatlas: warning: %s is still set and keeps delivering the secret for %s.%s\n", env, name, role)
	}
	return nil
}

// storableCredential loads the configuration and verifies that the pair names something that can hold a
// stored secret at all, keyring or vault (see manage.CheckSecretTarget). It returns the configuration and
// the credential, so the caller branches on its type without loading the configuration again.
func storableCredential(opts *Options, reg *capability.Registry, name, role string) (*config.Config, config.Credential, error) {
	path, err := config.Path(opts.Config)
	if err != nil {
		return nil, config.Credential{}, err
	}
	cfg, err := config.Load(path, reg)
	if err != nil {
		return nil, config.Credential{}, classifyUserError(err)
	}

	cred, err := manage.CheckSecretTarget(cfg, name, role)
	if err != nil {
		return nil, config.Credential{}, &UsageError{err}
	}
	return cfg, cred, nil
}

// readSecret reads the secret from standard input. A terminal is refused: typing a secret there would echo
// it and leave it on the screen. The editor has a masked field for that case, and the message names the way
// to it, because a user sent to an editor without a route types the secret into the first field that looks
// like it takes one.
func readSecret(c *cobra.Command) (string, error) {
	in := c.InOrStdin()
	if f, ok := in.(*os.File); ok {
		info, err := f.Stat()
		if err == nil && info.Mode()&os.ModeCharDevice != 0 {
			return "", &UsageError{errors.New("the secret is read from standard input: pipe it in, or open " +
				"'qatlas tui', go to Credentials, open the keyring credential, and press s on the role")}
		}
	}

	data, err := io.ReadAll(io.LimitReader(in, maxSecretBytes+1))
	if err != nil {
		return "", errors.New("cannot read the secret from standard input")
	}
	if len(data) > maxSecretBytes {
		return "", &UsageError{fmt.Errorf("the secret is longer than %d bytes", maxSecretBytes)}
	}
	// A secret piped from a file or from echo carries the trailing newline of its line, never a real one.
	value := strings.TrimRight(string(data), "\r\n")
	if value == "" {
		return "", &UsageError{errors.New("standard input carried no secret")}
	}
	return value, nil
}

func fallbackPath(secrets *secret.Resolver) string {
	if f := secrets.Plaintext(); f != nil {
		return f.Path()
	}
	return secret.FileName
}

func exactlyTwoArgs(_ *cobra.Command, args []string) error {
	if len(args) != 2 {
		return newSyntaxError(fmt.Errorf("expected a credential name and a secret role, got %d arguments", len(args)))
	}
	return nil
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
