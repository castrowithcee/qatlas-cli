package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// lookupAgentToken finds the agent token 'vault approve' presents without a terminal, and where it came
// from; see vault.FindAgentToken. It is a variable only so a test can decide what is found without the
// environment or the files of the machine it runs on.
var lookupAgentToken = func() (value, source string, err error) {
	dir, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	return vault.FindAgentToken(dir, home, os.Getenv)
}

// tokenNow is the clock an expiry is checked against when a token is created. A test sets it.
var tokenNow = time.Now

// errTokensNeedEncryption refuses a token command on a vault without a passphrase.
var errTokensNeedEncryption = errors.New("agent tokens need an encrypted vault: without a passphrase nobody " +
	"could manage one; run 'qatlas vault encrypt' first")

func newVaultTokenCommand(opts *Options, reg *capability.Registry) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage agent tokens, which let an agent approve connection changes within a vorbild",
		Long: "An agent token lets an agent approve an open connection change without the passphrase, as long\n" +
			"as the change reaches no further than one of the token's vorbild connections. A token carries a\n" +
			"name, one or more existing connections as vorbilder, and optionally an expiry, nothing else.\n\n" +
			"A change, creating, changing, or deleting a connection that reads a vault credential, is covered\n" +
			"when a vorbild V has the same service (provider and endpoint) and the same credential, and the\n" +
			"change stays inside V:\n" +
			"its permissions among V's; its tools among V's tools list, where V has one (without one, every\n" +
			"tool the permissions allow); its targets among V's, where V has any; and its paths inside V's,\n" +
			"where V is bound to any, so a bound vorbild never covers a connection without paths. V counts as\n" +
			"it is approved, not as it may be configured now. A change partly outside stays open whole, and a\n" +
			"connection that is the vorbild of any token is never changed or deleted with a token. A vorbild\n" +
			"that was renamed, deleted, is not approved, or reads a credential stored anew covers nothing;\n" +
			"'list' and 'show' warn about it.\n\n" +
			"A token never manages credentials or their secrets, the vault's settings, or tokens. Tokens exist\n" +
			"only while the vault is encrypted, sit encrypted inside it as tokens.age, and are managed only\n" +
			"from an interactive terminal with the vault's passphrase, like every other 'vault' command. A\n" +
			"token works in any number of projects: an agent presents it to 'qatlas vault approve' through\n" +
			"QATLAS_AGENT_TOKEN, or else a line QATLAS_AGENT_TOKEN=<token> in the nearest\n" +
			".qatlas/local/agent.env of its working directory or a directory above it, or else in\n" +
			"~/.qatlas/local/agent.env. qatlas only reads these files; a person writes them.\n\n" +
			"A token guards against an agent's mistakes and self-help, such as another customer's connection,\n" +
			"another credential, or more rights, not against an agent that sets out to get around it: an agent\n" +
			"can read every token file it finds.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error { return c.Help() },
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List the agent tokens, without their values",
		Long: "Lists every agent token with its vorbild connections, its expiry, when it was created, and a\n" +
			"warning for an expired token or a vorbild that covers nothing now, because it was renamed or\n" +
			"deleted, is not approved, or reads a credential stored anew since. It never shows a token's value.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error { return runVaultTokenList(c, opts, reg) },
	}

	var models []string
	var expires string
	create := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an agent token and show its value",
		Long: "Creates an agent token named <name> (letters, digits, '-', '_' or '.', at most 64 characters)\n" +
			"with the connections given with --vorbild as its vorbilder, each one that reads a vault\n" +
			"credential, and shows its value: 256 random bits. --expires ends it at the start of a date\n" +
			"(YYYY-MM-DD, local time) or at an RFC 3339 time; without it the token never expires. 'show'\n" +
			"shows the value again later. Put it where the agent finds it, QATLAS_AGENT_TOKEN or a\n" +
			".qatlas/local/agent.env; qatlas never writes it there itself.",
		Args: exactlyOneArg("token name"),
		RunE: func(c *cobra.Command, args []string) error {
			return runVaultTokenCreate(c, opts, reg, args[0], models, expires)
		},
	}
	create.Flags().StringArrayVar(&models, "vorbild", nil,
		"a connection whose reach the token may approve changes within, repeated per connection; required")
	create.Flags().StringVar(&expires, "expires", "",
		"when the token stops working: a date (YYYY-MM-DD, at its start, local time) or an RFC 3339 time")

	show := &cobra.Command{
		Use:   "show <name>",
		Short: "Show one agent token, its value included",
		Long: "Shows the agent token <name> with its value, vorbild connections, expiry, and when it was\n" +
			"created, and warns like 'list'. Its value is shown only here and by 'create', after the passphrase.",
		Args: exactlyOneArg("token name"),
		RunE: func(c *cobra.Command, args []string) error { return runVaultTokenShow(c, opts, reg, args[0]) },
	}

	revoke := &cobra.Command{
		Use:   "revoke <name>",
		Short: "Revoke one agent token",
		Long: "Removes the agent token <name> from the vault. It stops working at once, in a running vault\n" +
			"process too, which reads the tokens from the vault for every approval. Approvals it gave stay.",
		Args: exactlyOneArg("token name"),
		RunE: func(c *cobra.Command, args []string) error { return runVaultTokenRevoke(c, opts, args[0]) },
	}

	cmd.AddCommand(list, create, show, revoke)
	return cmd
}

// tokenVault runs the admin check and returns the vault it unlocked, refusing one without a passphrase.
func tokenVault(opts *Options) (*vault.Vault, error) {
	if err := requireAdmin(opts); err != nil {
		return nil, err
	}
	v, err := vaultOf(opts)
	if err != nil {
		return nil, err
	}
	state, err := v.State()
	if err != nil {
		return nil, classifyUserError(err)
	}
	if state != vault.StateUnlocked {
		return nil, &UsageError{errTokensNeedEncryption}
	}
	return v, nil
}

// tokenConfig loads the configuration a token's vorbilder are checked against.
func tokenConfig(opts *Options, reg *capability.Registry) (*config.Config, error) {
	path, err := config.Path(opts.Config)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(path, reg)
	if err != nil {
		return nil, classifyUserError(err)
	}
	return cfg, nil
}

// tokenWarning says what keeps a token from approving all it could: its expiry, or vorbilder that cover
// nothing now; "" when nothing does.
func tokenWarning(cfg *config.Config, v *vault.Vault, token vault.Token) (string, error) {
	var warning string
	if token.Expired(tokenNow()) {
		warning = "expired"
	}
	lapsed, err := approval.LapsedModels(cfg, v, token)
	if err != nil {
		return "", err
	}
	if len(lapsed) > 0 {
		addWarning(&warning, fmt.Sprintf("%s %s %s nothing now: renamed, deleted, not approved, or reading a "+
			"credential stored anew", plural(len(lapsed), "vorbild", "vorbilder"), strings.Join(lapsed, ", "),
			plural(len(lapsed), "covers", "cover")))
	}
	return warning, nil
}

func formatExpiry(token vault.Token) string {
	if token.Expires == nil {
		return "never"
	}
	return token.Expires.Local().Format(time.RFC3339)
}

func runVaultTokenList(c *cobra.Command, opts *Options, reg *capability.Registry) error {
	v, err := tokenVault(opts)
	if err != nil {
		return err
	}
	cfg, err := tokenConfig(opts, reg)
	if err != nil {
		return err
	}
	tokens, err := v.Tokens()
	if err != nil {
		return classifyUserError(err)
	}
	rows := make([]output.Row, 0, len(tokens))
	for _, token := range tokens {
		warning, err := tokenWarning(cfg, v, token)
		if err != nil {
			return classifyUserError(err)
		}
		rows = append(rows, output.Row{
			"name": token.Name, "vorbilder": strings.Join(token.Models, ", "), "expires": formatExpiry(token),
			"created": token.Created.Local().Format(time.RFC3339), "warning": warning,
		})
	}
	return emit(c, opts, output.Collection{
		Columns: []string{"name", "vorbilder", "expires", "created", "warning"}, Rows: rows,
	})
}

// parseExpiry reads --expires: a date, meaning its start in local time, or an RFC 3339 time. It must lie
// ahead.
func parseExpiry(text string) (*time.Time, error) {
	if text == "" {
		return nil, nil
	}
	at, err := time.ParseInLocation("2006-01-02", text, time.Local)
	if err != nil {
		if at, err = time.Parse(time.RFC3339, text); err != nil {
			return nil, &UsageError{errors.New("--expires takes a date as YYYY-MM-DD or an RFC 3339 time")}
		}
	}
	if !at.After(tokenNow()) {
		return nil, &UsageError{errors.New("--expires lies in the past; a token would never work")}
	}
	return &at, nil
}

func runVaultTokenCreate(c *cobra.Command, opts *Options, reg *capability.Registry, name string, models []string,
	expiresText string) error {
	if err := vault.CheckTokenName(name); err != nil {
		return &UsageError{err}
	}
	if len(models) == 0 {
		return &UsageError{errors.New("name at least one vorbild connection with --vorbild")}
	}
	expires, err := parseExpiry(expiresText)
	if err != nil {
		return err
	}
	v, err := tokenVault(opts)
	if err != nil {
		return err
	}
	cfg, err := tokenConfig(opts, reg)
	if err != nil {
		return err
	}
	for _, model := range models {
		connection, ok := cfg.Connections[model]
		if !ok {
			return &UsageError{fmt.Errorf("vorbild %s is not a configured connection", model)}
		}
		if cfg.Credentials[connection.Credential].Type != config.CredentialTypeVault {
			return &UsageError{fmt.Errorf("vorbild %s does not read a vault credential, so the vault approves "+
				"nothing for it", model)}
		}
	}
	token, err := v.CreateToken(name, models, expires)
	if errors.Is(err, vault.ErrTokenExists) {
		return &UsageError{fmt.Errorf("agent token %s exists already; revoke it first or choose another name", name)}
	}
	if err != nil {
		return classifyUserError(err)
	}
	return emitToken(c, opts, cfg, v, token)
}

// emitToken shows one token, its value included, as 'create' and 'show' do.
func emitToken(c *cobra.Command, opts *Options, cfg *config.Config, v *vault.Vault, token vault.Token) error {
	warning, err := tokenWarning(cfg, v, token)
	if err != nil {
		return classifyUserError(err)
	}
	fields := []output.Field{
		{Name: "name", Value: token.Name},
		{Name: "token", Value: token.Value},
		{Name: "vorbilder", Value: strings.Join(token.Models, ", ")},
		{Name: "expires", Value: formatExpiry(token)},
		{Name: "created", Value: token.Created.Local().Format(time.RFC3339)},
	}
	if warning != "" {
		fields = append(fields, output.Field{Name: "warning", Value: warning})
	}
	return emit(c, opts, output.Object{Fields: fields})
}

func runVaultTokenShow(c *cobra.Command, opts *Options, reg *capability.Registry, name string) error {
	v, err := tokenVault(opts)
	if err != nil {
		return err
	}
	cfg, err := tokenConfig(opts, reg)
	if err != nil {
		return err
	}
	tokens, err := v.Tokens()
	if err != nil {
		return classifyUserError(err)
	}
	for _, token := range tokens {
		if token.Name == name {
			return emitToken(c, opts, cfg, v, token)
		}
	}
	return &UsageError{fmt.Errorf("agent token %s: %w", name, vault.ErrTokenNotFound)}
}

func runVaultTokenRevoke(c *cobra.Command, opts *Options, name string) error {
	v, err := tokenVault(opts)
	if err != nil {
		return err
	}
	if err := v.RevokeToken(name); err != nil {
		if errors.Is(err, vault.ErrTokenNotFound) {
			return &UsageError{fmt.Errorf("agent token %s: %w", name, err)}
		}
		return classifyUserError(err)
	}
	fmt.Fprintf(c.OutOrStdout(), "revoked agent token %s\n", name)
	return nil
}

// runVaultApproveWithToken is 'vault approve' without a terminal: the agent token lookupAgentToken finds is
// handed to the vault process, which approves every open change the token covers, or only the ones names
// lists, and logs each decision. A change it does not cover stays open, and the command then fails with
// admin-required naming each such connection and what of it the token does not cover, never a value.
func runVaultApproveWithToken(c *cobra.Command, opts *Options, reg *capability.Registry, names []string) error {
	token, source, err := lookupAgentToken()
	if err != nil {
		return &UsageError{&application.AdminRequiredError{Reason: "the agent token cannot be read: " + err.Error()}}
	}
	if token == "" {
		reason := "no agent token was found; approving without a terminal needs one in " + vault.AgentTokenEnv +
			", in a .qatlas/local/agent.env of this directory or one above it, or in ~/.qatlas/local/agent.env"
		if source != "" {
			reason = "no agent token was found: " + source + " holds no " + vault.AgentTokenEnv + " line"
		}
		return &UsageError{&application.AdminRequiredError{Reason: reason}}
	}
	opts.Redactor.Add(token)

	v, err := vaultOf(opts)
	if err != nil {
		return err
	}
	state, err := v.State()
	if err != nil {
		return classifyUserError(err)
	}
	if state != vault.StateLocked && state != vault.StateUnlocked {
		return &UsageError{&application.AdminRequiredError{Reason: errTokensNeedEncryption.Error()}}
	}

	cfg, err := tokenConfig(opts, reg)
	if err != nil {
		return err
	}
	scopes, err := approval.VaultScopes(cfg)
	if err != nil {
		return classifyUserError(err)
	}
	for _, name := range names {
		known := false
		for _, scope := range scopes {
			known = known || scope.Connection == name
		}
		if !known {
			return &UsageError{fmt.Errorf("connection %s: %w", name, approval.ErrUnknownConnection)}
		}
	}

	if !vaultProcessSupported {
		return &secret.VaultLockedError{}
	}
	client, err := vaultProcessClient(v)
	if err != nil {
		return classifyUserError(err)
	}
	result, unlogged, err := client.ApproveWithToken(contextOrBackground(c.Context()), token, scopes, names)
	switch {
	case errors.Is(err, vaultproc.ErrNotRunning):
		return &secret.VaultLockedError{}
	case errors.Is(err, vault.ErrTokenUnknown), errors.Is(err, vault.ErrTokenExpired):
		return &UsageError{&application.AdminRequiredError{Reason: fmt.Sprintf("the agent token from %s is "+
			"refused: %s", source, err)}}
	case errors.Is(err, vaultproc.ErrNoTokens), errors.Is(err, vaultproc.ErrVersion):
		return fmt.Errorf("%w; lock the vault and unlock it again with this qatlas", err)
	case err != nil:
		return fmt.Errorf("%w; %s", err, secret.VaultProcessRemedy(err))
	}

	out := c.OutOrStdout()
	if unlogged {
		fmt.Fprintln(c.ErrOrStderr(), "qatlas: warning: the vault process could not record every decision in the "+
			"invocation log")
	}
	if len(result.Lapsed) > 0 {
		fmt.Fprintf(c.ErrOrStderr(), "qatlas: warning: %s %s of agent token %s %s nothing now: renamed, deleted, "+
			"not approved, or reading a credential stored anew\n", plural(len(result.Lapsed), "vorbild", "vorbilder"),
			redactAll(opts.Redactor, result.Lapsed), result.Token, plural(len(result.Lapsed), "covers", "cover"))
	}
	var approved, refused []string
	for _, change := range result.Changes {
		if change.Approved {
			approved = append(approved, fmt.Sprintf("%s (%s)", opts.Redactor.Apply(change.Connection), change.Kind))
			continue
		}
		refused = append(refused, fmt.Sprintf("%s (%s): %s", opts.Redactor.Apply(change.Connection), change.Kind,
			gapsText(change.Gaps)))
	}
	if len(approved) > 0 {
		fmt.Fprintf(out, "approved %d connection %s with agent token %s: %s\n", len(approved),
			plural(len(approved), "change", "changes"), result.Token, strings.Join(approved, ", "))
	}
	for _, name := range result.NotOpen {
		fmt.Fprintf(out, "%s has no open change\n", opts.Redactor.Apply(name))
	}
	if len(result.Changes) == 0 && len(result.NotOpen) == 0 {
		fmt.Fprintln(out, "nothing is open: every connection is approved as it is configured now")
	}
	if len(refused) > 0 {
		return &UsageError{&application.AdminRequiredError{Reason: fmt.Sprintf("agent token %s does not cover "+
			"%d open %s, which %s open: %s", result.Token, len(refused), plural(len(refused), "change", "changes"),
			plural(len(refused), "stays", "stay"), strings.Join(refused, "; "))}}
	}
	return nil
}

// gapsText is what a token does not cover of a change, as a person reads it.
func gapsText(gaps []string) string {
	out := make([]string, len(gaps))
	for i, gap := range gaps {
		switch gap {
		case vault.GapVorbild:
			out[i] = "it is the vorbild of an agent token"
		case vault.GapNoVorbild:
			out[i] = "no vorbild of the token covers anything now"
		default:
			out[i] = gap
		}
	}
	return strings.Join(out, ", ")
}
