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
)

// maxSecretBytes bounds what a piped secret may be, so a wrong redirection cannot pull a whole file into
// the credential store. No API token is anywhere near this large.
const maxSecretBytes = 8 << 10

func newCredentialCommand(opts *Options, reg *capability.Registry) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "credential",
		Short: "Manage the secrets of a keyring credential in the system keyring",
		Long: "A credential of type keyring keeps its secrets in the system keyring, the credential store\n" +
			"the operating system already provides: Secret Service on Linux (for example GNOME Keyring or\n" +
			"KWallet), the macOS Keychain, or the Windows Credential Manager. It is the recommended place\n" +
			"for secrets on a workstation: there is nothing to set up, no variable to export, and nothing\n" +
			"lands in the configuration file.\n\n" +
			"The other sources stay separate. A set variable QATLAS_<CREDENTIAL>_<ROLE> overrides a stored\n" +
			"secret, which suits CI and containers. The plaintext fallback is an unencrypted file beside\n" +
			"the configuration and is written only when --plaintext asks for it. QATLAS_CREDENTIAL_STORE=none\n" +
			"switches the system keyring off for a run.\n\n" +
			"These commands write and remove the entries. No command ever shows a stored secret back, not\n" +
			"even masked; 'qatlas config validate --secrets' shows which source delivers each role.",
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
			"Without --plaintext the secret goes into the system keyring. When the keyring is locked,\n" +
			"cannot be reached, or is switched off, the command fails rather than falling back silently,\n" +
			"and says what to do on this platform. Success is silent, except for a warning when an\n" +
			"environment variable would override what was just stored.",
		Args: exactlyTwoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			return setCredential(c, opts, reg, args[0], args[1], plaintext)
		},
	}
	set.Flags().BoolVar(&plaintext, "plaintext", false,
		"store the secret unencrypted in the plaintext fallback file beside the configuration instead, and switch that fallback on")

	remove := &cobra.Command{
		Use:   "delete <credential> <role>",
		Short: "Remove the secret of one credential role",
		Long: "The entry is removed from the system keyring and from the plaintext fallback. An\n" +
			"environment variable is not touched: it belongs to the shell, not to qatlas. When the keyring\n" +
			"is locked or cannot be reached, the command says so and what to do, and never reports a\n" +
			"secret as removed that may still be stored.",
		Args: exactlyTwoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			return deleteCredential(c, opts, reg, args[0], args[1])
		},
	}

	cmd.AddCommand(set, remove)
	return cmd
}

func setCredential(c *cobra.Command, opts *Options, reg *capability.Registry, name, role string, plaintext bool) error {
	if err := checkKeyringRole(opts, reg, name, role); err != nil {
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

	if plaintext {
		if err := secrets.SetPlaintext(name, role, value); err != nil {
			return classifyUserError(err)
		}
	} else if err := secrets.Set(name, role, value); err != nil {
		if errors.Is(err, secret.ErrUnavailable) || errors.Is(err, secret.ErrDisabled) {
			// The advice is built from the class of the failure, never from what the platform said.
			return &UsageError{fmt.Errorf("cannot store the secret for %s.%s: %s, then run the command "+
				"again; or export %s; or, only if an unencrypted file is acceptable, store it in %s with "+
				"--plaintext", name, role, secret.StoreAdvice(secret.StoreStateOf(err), runtime.GOOS),
				secret.DerivedEnvName(name, role), fallbackPath(secrets))}
		}
		return classifyUserError(err)
	}

	// Overriding is allowed, so a shadowing variable must be named the moment it starts shadowing.
	if env := secret.DerivedEnvName(name, role); secrets.Lookup(env) {
		fmt.Fprintf(c.ErrOrStderr(),
			"qatlas: warning: %s is set and overrides what was just stored for %s.%s\n", env, name, role)
	}
	return nil
}

func deleteCredential(c *cobra.Command, opts *Options, reg *capability.Registry, name, role string) error {
	if err := checkKeyringRole(opts, reg, name, role); err != nil {
		return err
	}
	secrets, err := opts.resolver()
	if err != nil {
		return err
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

// checkKeyringRole verifies that the pair names something that can hold a stored secret at all. A typo
// would otherwise leave an entry in the credential store that nothing ever reads.
func checkKeyringRole(opts *Options, reg *capability.Registry, name, role string) error {
	path, err := config.Path(opts.Config)
	if err != nil {
		return err
	}
	cfg, err := config.Load(path, reg)
	if err != nil {
		return classifyUserError(err)
	}

	cred, ok := cfg.Credentials[name]
	if !ok {
		return &UsageError{&config.NotThereError{Kind: "credential", Name: name}}
	}
	if cred.Type != config.CredentialTypeKeyring {
		return &UsageError{fmt.Errorf(
			"credential %q has type %q: its secrets come from the environment variables it names, so there "+
				"is nothing to store", name, cred.Type)}
	}
	if !contains(cfg.SecretRoles(), role) {
		return &UsageError{fmt.Errorf("unknown secret role %q, known roles are %s",
			role, strings.Join(cfg.SecretRoles(), ", "))}
	}
	return nil
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
