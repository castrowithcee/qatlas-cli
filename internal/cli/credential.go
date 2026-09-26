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
			"keyring. 'qatlas vault status' shows whether it exists, is encrypted, and is unlocked.\n\n" +
			"The other sources stay separate. A set variable QATLAS_<CREDENTIAL>_<ROLE> overrides a stored\n" +
			"secret, which suits CI and containers. The plaintext fallback is an unencrypted file beside\n" +
			"the configuration and is written only when --plaintext asks for it, for a keyring credential.\n" +
			"QATLAS_CREDENTIAL_STORE=none switches the system keyring off for a run.\n\n" +
			"These commands write and remove the entries. No command ever shows a stored secret back, not\n" +
			"even masked; 'qatlas config validate --secrets' shows which source delivers each role.\n\n" +
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

	var plaintext bool
	set := &cobra.Command{
		Use:   "set <credential> <role>",
		Short: "Store the secret of one credential role",
		Long: "The secret is read from standard input, so it never appears in the command line or in the\n" +
			"shell history:\n\n" +
			"    printf %s \"$TOKEN\" | qatlas credential set wiki-reader token-id\n" +
			"    qatlas credential set wiki-reader token-id < token.txt\n\n" +
			"A keyring credential's secret goes into the system keyring, unless --plaintext asks for the\n" +
			"unencrypted fallback file instead. When the keyring is locked, cannot be reached, or is\n" +
			"switched off, the command fails rather than falling back silently, and says what to do on\n" +
			"this platform.\n\n" +
			"A vault credential's secret goes into the vault. Storing the very first secret the vault ever\n" +
			"holds asks, on the terminal, for a passphrase to encrypt it with; leaving that empty keeps the\n" +
			"vault unencrypted. Every later secret needs no passphrase to store, even while the vault is\n" +
			"encrypted and locked: it is queued and merged in on the next 'qatlas vault unlock'.\n\n" +
			"Success is silent, except for a warning when an environment variable would override what was\n" +
			"just stored.",
		Args: exactlyTwoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			return setCredential(c, opts, reg, args[0], args[1], plaintext)
		},
	}
	set.Flags().BoolVar(&plaintext, "plaintext", false,
		"for a keyring credential, store the secret unencrypted in the plaintext fallback file beside the configuration instead, and switch that fallback on")

	remove := &cobra.Command{
		Use:   "delete <credential> <role>",
		Short: "Remove the secret of one credential role",
		Long: "For a keyring credential, the entry is removed from the system keyring and from the\n" +
			"plaintext fallback. For a vault credential, it is removed from the vault, which needs the\n" +
			"vault unlocked the same way reading it does. An environment variable is not touched: it\n" +
			"belongs to the shell, not to qatlas. When the keyring is locked or cannot be reached, or the\n" +
			"vault is locked, the command says so and what to do, and never reports a secret as removed\n" +
			"that may still be stored.",
		Args: exactlyTwoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			return deleteCredential(c, opts, reg, args[0], args[1])
		},
	}

	cmd.AddCommand(set, remove)
	return cmd
}

func setCredential(c *cobra.Command, opts *Options, reg *capability.Registry, name, role string, plaintext bool) error {
	cred, err := storableCredential(opts, reg, name, role)
	if err != nil {
		return err
	}
	if plaintext && cred.Type != config.CredentialTypeKeyring {
		return &UsageError{fmt.Errorf(
			"credential %q has type %q: --plaintext applies only to a keyring credential", name, cred.Type)}
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

	switch {
	case cred.Type == config.CredentialTypeVault:
		if err := secrets.SetVault(name, role, value, offerVaultPassphrase); err != nil {
			return classifyUserError(err)
		}
	case plaintext:
		if err := secrets.SetPlaintext(name, role, value); err != nil {
			return classifyUserError(err)
		}
	default:
		if err := secrets.Set(name, role, value); err != nil {
			if errors.Is(err, secret.ErrUnavailable) || errors.Is(err, secret.ErrDisabled) {
				// The advice is built from the class of the failure, never from what the platform said.
				return &UsageError{fmt.Errorf("cannot store the secret for %s.%s: %s, then run the command "+
					"again; or export %s; or, only if an unencrypted file is acceptable, store it in %s with "+
					"--plaintext", name, role, secret.StoreAdvice(secret.StoreStateOf(err), runtime.GOOS),
					secret.DerivedEnvName(name, role), fallbackPath(secrets))}
			}
			return classifyUserError(err)
		}
	}

	// Overriding is allowed, so a shadowing variable must be named the moment it starts shadowing.
	if env := secret.DerivedEnvName(name, role); secrets.Lookup(env) {
		fmt.Fprintf(c.ErrOrStderr(),
			"qatlas: warning: %s is set and overrides what was just stored for %s.%s\n", env, name, role)
	}
	return nil
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
	cred, err := storableCredential(opts, reg, name, role)
	if err != nil {
		return err
	}
	secrets, err := opts.resolver()
	if err != nil {
		return err
	}

	if cred.Type == config.CredentialTypeVault {
		if err := secrets.DeleteVault(name, role); err != nil {
			if errors.Is(err, secret.ErrNoEntry) {
				return &UsageError{fmt.Errorf("no stored secret for %s.%s", name, role)}
			}
			return classifyUserError(err)
		}
		if env := secret.DerivedEnvName(name, role); secrets.Lookup(env) {
			fmt.Fprintf(c.ErrOrStderr(),
				"qatlas: warning: %s is still set and keeps delivering the secret for %s.%s\n", env, name, role)
		}
		return nil
	}

	if _, err := secrets.Delete(name, role); err != nil {
		if errors.Is(err, secret.ErrNoEntry) {
			return &UsageError{fmt.Errorf("no stored secret for %s.%s", name, role)}
		}
		// A delete that could not clear every place says so, including what it did clear. It is never a
		// silent success.
		if errors.Is(err, secret.ErrUnavailable) {
			err = fmt.Errorf("%w; %s, then run the command again", err,
				secret.StoreAdvice(secret.StoreStateOf(err), runtime.GOOS))
		}
		return classifyUserError(err)
	}
	// A switched-off store was never consulted, so the delete says nothing about what may sit in it. Left
	// unsaid, a silent success reads as "the secret is gone everywhere", which is the one thing it does
	// not mean.
	if secrets.StoreSkipped() {
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

// storableCredential verifies that the pair names something that can hold a stored secret at all, keyring
// or vault, and returns it, so the caller branches on its type without loading the configuration again. A
// typo would otherwise leave an entry nothing ever reads.
func storableCredential(opts *Options, reg *capability.Registry, name, role string) (config.Credential, error) {
	path, err := config.Path(opts.Config)
	if err != nil {
		return config.Credential{}, err
	}
	cfg, err := config.Load(path, reg)
	if err != nil {
		return config.Credential{}, classifyUserError(err)
	}

	cred, ok := cfg.Credentials[name]
	if !ok {
		return config.Credential{}, &UsageError{&config.NotThereError{Kind: "credential", Name: name}}
	}
	if cred.Type != config.CredentialTypeKeyring && cred.Type != config.CredentialTypeVault {
		return config.Credential{}, &UsageError{fmt.Errorf(
			"credential %q has type %q: its secrets come from the environment variables it names, so there "+
				"is nothing to store", name, cred.Type)}
	}
	if !contains(cfg.SecretRoles(), role) {
		return config.Credential{}, &UsageError{fmt.Errorf("unknown secret role %q, known roles are %s",
			role, strings.Join(cfg.SecretRoles(), ", "))}
	}
	return cred, nil
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
