// Package config loads and validates the qatlas configuration and resolves the connection a command
// should use. It never reads, stores, or reports secret values: a credential only says where its secrets
// come from, either by naming environment variables or by pointing at the system credential store.
// Resolving a secret from that description is the job of package secret.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	yaml "go.yaml.in/yaml/v3"
)

// Schema versions this build reads. Version 2 adds the stable id of a service, a credential and a
// connection. Version 3 replaces the two overlapping allow-lists of a connection by either effects or tools
// and names the effects field "effects"; a file of an older version may still say "permissions".
const (
	versionNames   = 1
	versionIDs     = 2
	versionEffects = 3
)

// Version is the schema version every configuration is written in. A file of an older schema is still read
// and becomes the current one when it is next saved.
const Version = versionEffects

// Config is the whole configuration file.
//
// The model is provider -> service -> connection -> credential: a service describes a technical API
// endpoint, a credential only the source of a secret, and a connection the selectable access context. That
// separation is what allows several instances of one provider, and several keys for one instance.
//
// ProviderNotes maps a provider ID to one optional line the user maintains on what that provider stands
// for here, such as "wiki" or "CRM". The provider's own description says what kind of system it is, a
// connection's description what one route is for; the note sits between them and lets a reader map a word
// of a task to a provider. Discovery publishes it verbatim and searches it, so like a connection
// description it is configuration, never a secret and never personal data.
type Config struct {
	Version       int                   `yaml:"version"`
	Services      map[string]Service    `yaml:"services,omitempty"`
	Credentials   map[string]Credential `yaml:"credentials,omitempty"`
	Connections   map[string]Connection `yaml:"connections,omitempty"`
	ProviderNotes map[string]string     `yaml:"provider_notes,omitempty"`
	Defaults      Defaults              `yaml:"defaults"`
	Vault         VaultSettings         `yaml:"vault,omitempty"`
	Logs          LogSettings           `yaml:"logs,omitempty"`
	providers     ProviderCatalog       `yaml:"-"`
	renamings     []Renaming            `yaml:"-"`
}

// Service is a technical API endpoint of one provider. BaseURL may be left out for a provider that declares
// a default endpoint; ServiceBaseURL returns the endpoint that applies.
type Service struct {
	// ID is the stable, random, never reused identity of the entry within the services; the map key stays
	// its name. It is no trust anchor: nothing an approval or fingerprint covers depends on it. An entry
	// without one stays valid and gets one when Qatlas next saves.
	ID       string            `yaml:"id,omitempty"`
	Provider string            `yaml:"provider"`
	BaseURL  string            `yaml:"base_url,omitempty"`
	Options  map[string]string `yaml:"options,omitempty"`
}

// Credential names the source of the secrets a provider needs, never a secret itself.
//
// Type "env" maps every provider-defined secret role to the name of an environment variable in Values.
// That is the unchanged path for CI and headless use. Type "keyring" names nothing at all: its secrets
// live in the system credential store, and Values must stay empty, so this file cannot hold a secret even
// by accident.
//
// Provider is optional and names the system these secrets belong to. Secret roles are provider-defined, so
// without it nothing says whether a credential holds a bot token or a wiki token pair, and an editor can
// only offer the roles of every compiled provider at once. A credential written before this field, or by
// hand without it, stays valid and keeps that behaviour.
//
// Forward marks a payload secret: a credential that belongs to no provider and holds freely named fields
// (Fields) whose values a tool may pass on to a third party by reference. It uses the vault or keyring type,
// never env, and is never the credential of a connection. A connection releases it through forward_secrets.
type Credential struct {
	// ID is the stable identity of the credential; see Service.ID.
	ID       string            `yaml:"id,omitempty"`
	Provider string            `yaml:"provider,omitempty"`
	Type     string            `yaml:"type"`
	Values   map[string]string `yaml:"values,omitempty"`
	Forward  bool              `yaml:"forward,omitempty"`
	Fields   []string          `yaml:"fields,omitempty"`
	// Description is one line of prose that says what a forward credential is for. Discovery publishes it
	// verbatim; only a forward credential may have one.
	Description string `yaml:"description,omitempty"`
}

// Connection binds exactly one service to exactly one credential. Target is an optional provider-specific
// scope inside that service.
//
// A connection selects what it offers in one of two modes, never both. By effect (Effects, or the provider's
// defaults when missing) offers every tool of those effects, also one registered later, except the tools
// that require an allow-list; an explicit empty list offers none. By selected tools (Tools) offers exactly
// the listed provider-qualified tool IDs of this connection's provider, and a tool registered later is never
// added; the effects it covers are derived from the tools (see ConnectionEffects) and not stored. An empty
// Tools offers none.
//
// Description is optional prose the user maintains. A name like "personal" or "crm-internal" is a stable
// selector, not an explanation, so this one line says what the route is for and lets a reader tell two
// routes of one provider apart. Discovery publishes it verbatim and nothing is ever sent to a provider,
// which is why it is configuration, never a secret and never personal data.
//
// Paths, when present, binds the connection to projects: it names directories, absolute or starting with
// "~", and the connection is discovered and run only for a project that equals one of them or lies below
// it (see ConnectionApplies). Missing, the connection applies everywhere. Discovery never publishes the
// paths. Managing commands still see every connection.
//
// Files, when present, releases local directories to tools that read (Files.Read) or write (Files.Write)
// local files. Missing, the connection gives no local file access at all. Unlike Paths it is a permission
// and never a selector; see validateFiles.
type Connection struct {
	// ID is the stable identity of the connection; see Service.ID.
	ID          string   `yaml:"id,omitempty"`
	Service     string   `yaml:"service"`
	Credential  string   `yaml:"credential"`
	Target      string   `yaml:"target,omitempty"`
	Targets     []string `yaml:"targets,omitempty"`
	Description string   `yaml:"description,omitempty"`
	Effects     []Effect `yaml:"effects,omitempty"`
	Tools       []string `yaml:"tools,omitempty"`
	// LegacyPermissions only receives the former name of Effects while a file is read; reading folds it into
	// Effects and leaves it empty, and it is never written.
	LegacyPermissions []Effect `yaml:"permissions,omitempty"`
	Paths             []string `yaml:"paths,omitempty"`
	Files             Files    `yaml:"files,omitempty"`
	// ForwardSecrets lists the forward credentials a tool of this connection may use as a reference argument.
	// Missing releases none.
	ForwardSecrets []string `yaml:"forward_secrets,omitempty"`
	// Disabled switches the connection off: it offers no tool while the rest of its selection stays. Missing
	// means active.
	Disabled bool `yaml:"disabled,omitempty"`
	// ActiveUntil ends the connection's activity at an instant, in the format of an agent token's expiry
	// (see ParseExpiryTime). From then on it refuses like a disabled one. Missing means no limit. It is kept
	// as read so a past value stays loadable; MarshalYAML writes it as RFC 3339.
	ActiveUntil string `yaml:"active_until,omitempty"`
	// TargetLabels names targets of this connection for the person: a target value from TargetValues maps to
	// a one-line label. It is display text only. It grants nothing, enters neither Resolved nor the approval
	// scope, and no surface for agents ever shows it, since it is free text a person typed. Missing means no
	// labels.
	TargetLabels map[string]string `yaml:"target_labels,omitempty"`

	// legacyEffects is the former permissions list of a connection that also lists tools, kept from reading
	// the file so a migration can name the connections whose approval it narrows. It is no configuration.
	legacyEffects []Effect
	// bothEffectFields is set when a file names both effects and its former name permissions.
	bothEffectFields bool
	// permissionsInCurrent is set when a file of the current schema still names the former permissions field.
	permissionsInCurrent bool
}

// foldLegacyEffects moves the former permissions field of every connection into the current model. Alone it
// is the effects list. Next to tools it is dropped, since the bound that the former validation enforced
// (every listed tool's effect is among the permissions) made the tools the whole offer; the list stays in
// legacyEffects for the migration report. Next to effects it is a problem, reported by Validate. A file of
// the current schema never folds: it must say effects, and the field is a problem instead.
func (c *Config) foldLegacyEffects() {
	for name, conn := range c.Connections {
		legacy := conn.LegacyPermissions
		if legacy == nil {
			continue
		}
		conn.LegacyPermissions = nil
		switch {
		case c.Version >= versionEffects:
			conn.permissionsInCurrent = true
		case conn.Effects != nil:
			conn.bothEffectFields = true
		case conn.Tools != nil:
			conn.legacyEffects = legacy
		default:
			conn.Effects = legacy
		}
		c.Connections[name] = conn
	}
}

// MarshalYAML preserves the semantic difference between a missing effects or tools field (the provider's
// default effects) and an explicit empty list (offer nothing). A plain omitempty slice cannot represent both
// states when a configuration is saved through the TUI.
func (c Connection) MarshalYAML() (any, error) {
	type wire struct {
		ID             string            `yaml:"id,omitempty"`
		Service        string            `yaml:"service"`
		Credential     string            `yaml:"credential"`
		Target         string            `yaml:"target,omitempty"`
		Targets        []string          `yaml:"targets,omitempty"`
		Description    string            `yaml:"description,omitempty"`
		Effects        *[]Effect         `yaml:"effects,omitempty"`
		Tools          *[]string         `yaml:"tools,omitempty"`
		Paths          []string          `yaml:"paths,omitempty"`
		Files          Files             `yaml:"files,omitempty"`
		ForwardSecrets []string          `yaml:"forward_secrets,omitempty"`
		Disabled       bool              `yaml:"disabled,omitempty"`
		ActiveUntil    string            `yaml:"active_until,omitempty"`
		TargetLabels   map[string]string `yaml:"target_labels,omitempty"`
	}
	activeUntil := c.ActiveUntilCanonical()
	var effects *[]Effect
	if c.Effects != nil {
		copy := append(make([]Effect, 0, len(c.Effects)), c.Effects...)
		effects = &copy
	}
	var tools *[]string
	if c.Tools != nil {
		copy := append(make([]string, 0, len(c.Tools)), c.Tools...)
		tools = &copy
	}
	return wire{ID: c.ID, Service: c.Service, Credential: c.Credential, Target: c.Target, Targets: c.Targets,
		Description: c.Description, Effects: effects, Tools: tools, Paths: c.Paths, Files: c.Files,
		ForwardSecrets: c.ForwardSecrets, Disabled: c.Disabled, ActiveUntil: activeUntil,
		TargetLabels: c.TargetLabels}, nil
}

// Defaults holds the connection chosen for a domain when no connection is given explicitly, and the
// credential type a new credential starts from.
type Defaults struct {
	Connections map[string]string `yaml:"connections,omitempty"`
	// SecretStore is the credential type a new credential is created with unless the person picks another
	// one: CredentialTypeKeyring or CredentialTypeVault. Left empty, it means CredentialTypeKeyring, so an
	// existing file without this field keeps the behaviour it already had.
	SecretStore string `yaml:"secret_store,omitempty"`
}

// SecretStore returns the effective default credential type, CredentialTypeKeyring when Defaults.SecretStore
// is left empty.
func (c *Config) SecretStore() string {
	if c.Defaults.SecretStore == "" {
		return CredentialTypeKeyring
	}
	return c.Defaults.SecretStore
}

// DefaultVaultIdleTimeout is how long the vault process holds the unlocked vault without a read before it
// locks itself, unless vault.idle_timeout says otherwise.
const DefaultVaultIdleTimeout = 12 * time.Hour

// DefaultVaultAdminTimeout is how long an admin session of the TUI editor (internal/tui, see requireAdmin)
// stays open, without a key press in that window, before it asks for the vault's passphrase again, unless
// vault.admin_timeout says otherwise.
const DefaultVaultAdminTimeout = 10 * time.Minute

// VaultSettings holds the settings of the vault that are not secret.
type VaultSettings struct {
	// IdleTimeout is how long the vault process holds the unlocked vault without a read before it locks
	// itself, as a Go duration such as "12h" or "30m". Left empty, it means DefaultVaultIdleTimeout. It must
	// be positive: a vault process that never locks itself is not offered, and 'qatlas vault lock' locks it
	// at once.
	IdleTimeout string `yaml:"idle_timeout,omitempty"`
	// AdminTimeout is how long the TUI editor's admin session (internal/tui, see requireAdmin) stays open,
	// without a key press in that window, before it asks for the vault's passphrase again, as a Go duration
	// such as "10m". Left empty, it means DefaultVaultAdminTimeout. "0" is allowed and means asking for the
	// passphrase on every managing action instead of keeping a session at all; it must not be negative.
	AdminTimeout string `yaml:"admin_timeout,omitempty"`
}

// VaultIdleTimeout returns the effective idle timeout of the vault process. A configuration that passed
// Validate always has one; an unparseable value falls back to DefaultVaultIdleTimeout.
func (c *Config) VaultIdleTimeout() time.Duration {
	if d, err := parseIdleTimeout(c.Vault.IdleTimeout); err == nil && d > 0 {
		return d
	}
	return DefaultVaultIdleTimeout
}

func parseIdleTimeout(text string) (time.Duration, error) {
	if text == "" {
		return DefaultVaultIdleTimeout, nil
	}
	d, err := time.ParseDuration(text)
	if err != nil {
		return 0, errors.New(`must be a duration such as "12h" or "30m"`)
	}
	if d <= 0 {
		return 0, errors.New("must be positive; run 'qatlas vault lock' to lock the vault at once")
	}
	return d, nil
}

// VaultAdminTimeout returns the effective admin session timeout the TUI editor's requireAdmin reads (see
// internal/tui/admin.go). A configuration that passed Validate always has one; an unparseable value falls
// back to DefaultVaultAdminTimeout. Unlike VaultIdleTimeout, "0" is a real, valid value, not a fallback
// trigger: it tells requireAdmin to never keep a session at all, asking for the passphrase on every managing
// action instead.
func (c *Config) VaultAdminTimeout() time.Duration {
	if d, err := parseAdminTimeout(c.Vault.AdminTimeout); err == nil {
		return d
	}
	return DefaultVaultAdminTimeout
}

func parseAdminTimeout(text string) (time.Duration, error) {
	if text == "" {
		return DefaultVaultAdminTimeout, nil
	}
	d, err := time.ParseDuration(text)
	if err != nil {
		return 0, errors.New(`must be a duration such as "10m" or "0"`)
	}
	if d < 0 {
		return 0, errors.New("must not be negative; 0 means asking for the passphrase on every change")
	}
	return d, nil
}

// DefaultLogRetentionDays is how many days of invocation log entries are kept when logs.retention_days is
// left out.
const DefaultLogRetentionDays = 90

// LogSettings holds the settings of the invocation log that are not secret. The log itself is not
// optional: every invoke over the CLI and MCP writes an entry whatever this section says; only how long
// entries are kept is configurable.
type LogSettings struct {
	// RetentionDays is how many days of invocation log entries are kept, a positive integer. Left out or
	// zero, it means DefaultLogRetentionDays.
	RetentionDays int `yaml:"retention_days,omitempty"`
}

// LogRetentionDays returns the effective retention window of the invocation log. A configuration that
// passed Validate always has a positive one; an unset value falls back to DefaultLogRetentionDays.
func (c *Config) LogRetentionDays() int {
	if c.Logs.RetentionDays <= 0 {
		return DefaultLogRetentionDays
	}
	return c.Logs.RetentionDays
}

// The supported credential types.
const (
	// CredentialTypeEnv resolves from the environment variables the credential names.
	CredentialTypeEnv = "env"
	// CredentialTypeKeyring resolves from the system credential store, and from the plaintext fallback
	// beside this file when that fallback was switched on. Both are overridden by a derived environment
	// variable, so the same credential still works in a container.
	CredentialTypeKeyring = "keyring"
	// CredentialTypeVault resolves from the vault directory beside this file, unencrypted or encrypted to
	// a passphrase. Like CredentialTypeKeyring it is overridden by a derived environment variable, and it
	// consults no other stage: a vault credential either names its variable or keeps its secrets in the
	// vault, never in the system keyring.
	CredentialTypeVault = "vault"
)

// CredentialTypes lists the supported credential types, in the order the documentation shows them.
func CredentialTypes() []string {
	return []string{CredentialTypeEnv, CredentialTypeKeyring, CredentialTypeVault}
}

// NotFoundError reports that no configuration file exists at the resolved path.
type NotFoundError struct{ Path string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("no configuration file at %s", e.Path)
}

// InvalidError reports that a configuration file exists but does not satisfy the schema.
type InvalidError struct {
	Path string
	Err  error
}

func (e *InvalidError) Error() string { return fmt.Sprintf("%s: %v", e.Path, e.Err) }

func (e *InvalidError) Unwrap() error { return e.Err }

// Path returns the configuration file to use. An explicit path wins, then the file named by
// QATLAS_CONFIG, then config.yaml inside the directory named by QATLAS_CLI_HOME, then
// ~/.qatlas/cli/config.yaml.
func Path(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if p := os.Getenv("QATLAS_CONFIG"); p != "" {
		return p, nil
	}
	if dir := os.Getenv("QATLAS_CLI_HOME"); dir != "" {
		return filepath.Join(dir, "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine the user home directory: %w", err)
	}
	return filepath.Join(home, ".qatlas", "cli", "config.yaml"), nil
}

// Load reads and validates the configuration at path.
func Load(path string, providers ...ProviderCatalog) (*Config, error) {
	return load(path, Observer{}, providers...)
}

func load(path string, obs Observer, providers ...ProviderCatalog) (*Config, error) {
	data, err := readFile(path, obs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &NotFoundError{Path: path}
		}
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}

	cfg, err := Decode(bytes.NewReader(data), providers...)
	if err != nil {
		return nil, &InvalidError{Path: path, Err: err}
	}
	return cfg, nil
}

// Decode reads a configuration from r. Unknown keys and duplicate keys are errors.
func Decode(r io.Reader, providers ...ProviderCatalog) (*Config, error) {
	cfg, err := decodeRaw(r, providers...)
	if err != nil {
		return nil, err
	}
	cfg.foldLegacyEffects()
	if err := cfg.applyRenames(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	// A decoded configuration is directly editable, and an absent section round-trips to the same model
	// as an empty one.
	cfg.ensure()
	cfg.normalize()
	return cfg, nil
}

// decodeRaw parses exactly one document into the model without mapping former names or validating it.
func decodeRaw(r io.Reader, providers ...ProviderCatalog) (*Config, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	// The version is checked before the strict decoding, so a file of a newer schema reports its version
	// instead of an unknown key. A file that does not parse at all is left to the strict decoding below.
	var head struct {
		Version int `yaml:"version"`
	}
	if yaml.Unmarshal(data, &head) == nil && head.Version != 0 && !knownVersion(head.Version) {
		return nil, problem(SectionVersion, "", "", CodeUnsupportedVersion, "got %d, want %s", head.Version, versionWant).
			with("want", versionWant)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var cfg Config
	cfg.providers = providerCatalog(providers)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the configuration file is empty")
		}
		return nil, redactDecodeError(err)
	}
	// A second document would silently shadow the first one.
	if err := dec.Decode(new(Config)); !errors.Is(err, io.EOF) {
		return nil, errors.New("the configuration file must contain exactly one document")
	}
	return &cfg, nil
}

const versionWant = "1, 2 or 3"

func knownVersion(v int) bool { return v == versionNames || v == versionIDs || v == versionEffects }

// yamlLine matches the position the YAML library puts in front of a message it can locate.
var yamlLine = regexp.MustCompile(`^line (\d+): `)

// yamlProblems names the kinds of problem the library reports, keyed by a fragment of its message that
// carries no text from the file. The first match wins.
var yamlProblems = []struct{ match, problem string }{
	{" not found in type ", "unknown key"},
	{" already defined at line ", "duplicate key"},
	{"cannot unmarshal ", "a value does not have the type its key requires"},
	{"cannot decode ", "a value has an explicit type tag it does not satisfy"},
	{"unknown anchor ", "a reference to an anchor that is not defined"},
	{"value contains itself", "an anchor that refers to itself"},
	{"invalid map key", "an unusable key"},
}

// redactDecodeError replaces a decoder error with one that names the place and the kind of problem but
// never the text that caused it. The library quotes the offending key or value, and a user can type a
// secret in either place, so the whole message has to be rebuilt rather than filtered.
//
// The kinds are matched on message fragments; an unrecognized message keeps its position and loses its
// explanation. That is the deliberate trade against quoting the file. Extend the table when the library
// gains a message worth naming.
func redactDecodeError(err error) error {
	messages := []string{strings.TrimPrefix(err.Error(), "yaml: ")}
	var typeErr *yaml.TypeError
	if errors.As(err, &typeErr) {
		messages = typeErr.Errors
	}

	problems := make([]string, 0, len(messages))
	seen := make(map[string]bool, len(messages))
	for _, message := range messages {
		where := ""
		if at := yamlLine.FindStringSubmatch(message); at != nil {
			where = at[0]
			message = message[len(at[0]):]
		}
		problem := where + "the file does not parse as YAML"
		for _, known := range yamlProblems {
			if strings.Contains(message, known.match) {
				problem = where + known.problem
				break
			}
		}
		if !seen[problem] {
			seen[problem] = true
			problems = append(problems, problem)
		}
	}
	return errors.New(strings.Join(problems, "; "))
}

// Validate reports every schema and reference problem at once, each as a Problem inside the joined error.
// Messages name configuration keys and environment variable names only, never secret values.
func (c *Config) Validate() error {
	providers := c.providerCatalog()
	var problems []error
	report := func(p Problem) {
		problems = append(problems, p)
	}
	// checkName reports an unusable name under its own key, so several bad names are all visible at once.
	// For a nested key such as defaults.connections the entry sits below field.
	checkName := func(section, field, kind, name string) {
		err := validateName(name)
		if err == nil {
			return
		}
		p := problem(section, name, "", CodeInvalidName, "a %s %v", kind, err)
		if field != "" {
			p.Name, p.Field = "", field
			if name != "" {
				p.Field = field + "." + name
			}
		}
		report(p)
	}

	if !knownVersion(c.Version) {
		report(problem(SectionVersion, "", "", CodeUnsupportedVersion, "got %d, want %s", c.Version, versionWant).
			with("want", versionWant))
	}

	for _, name := range sortedKeys(c.Services) {
		s := c.Services[name]
		checkName(SectionServices, "", "service name", name)
		metadata, metaOK := providers.ProviderMetadata(s.Provider)
		if s.Provider == "" {
			report(problem(SectionServices, name, "", CodeProviderMissing, "provider must not be empty").
				refined("provider"))
		} else if !metaOK {
			report(problem(SectionServices, name, "", CodeUnknownProvider,
				"unknown provider %q, known providers are %s", s.Provider, strings.Join(c.Providers(), ", ")).
				refined("provider").with("known", strings.Join(c.Providers(), ", ")))
		}
		if err := validateBaseURL(c.ServiceBaseURL(s)); err != nil {
			report(problem(SectionServices, name, "base_url", CodeInvalidBaseURL, "%v", err))
		} else if metaOK && metadata.ValidateBaseURL != nil {
			// The generic check above already ensured a well-formed http(s) URL; this second, provider-owned
			// check catches a base URL that its own client would still refuse, such as a GitHub host without
			// /api/v3, before the first call instead of at it.
			if err := metadata.ValidateBaseURL(c.ServiceBaseURL(s)); err != nil {
				report(problem(SectionServices, name, "base_url", CodeInvalidBaseURL, "%v", err).because(err))
			}
		}
	}

	for _, name := range sortedKeys(c.Credentials) {
		cred := c.Credentials[name]
		checkName(SectionCredentials, "", "credential name", name)
		validateForward(name, cred, report)
		if cred.Provider != "" {
			if _, ok := providers.ProviderMetadata(cred.Provider); !ok {
				report(problem(SectionCredentials, name, "", CodeUnknownProvider,
					"unknown provider %q, known providers are %s", cred.Provider, strings.Join(c.Providers(), ", ")).
					refined("provider").with("known", strings.Join(c.Providers(), ", ")))
			} else if roles := c.SecretRolesOf(cred.Provider); len(roles) > 0 {
				// A role the named provider does not define would never be read. Naming the provider is
				// what makes that visible here instead of at the first failing call.
				for _, role := range sortedKeys(cred.Values) {
					if !contains(roles, role) {
						report(problem(SectionCredentials, name, "values."+role, CodeUnknownSecretRole,
							"%s does not define this secret role, it uses %s", cred.Provider, strings.Join(roles, ", ")).
							with("provider", cred.Provider).with("roles", strings.Join(roles, ", ")))
					}
				}
			}
		}
		switch cred.Type {
		case CredentialTypeEnv:
			if len(cred.Values) == 0 {
				report(problem(SectionCredentials, name, "values", CodeSecretRolesMissing,
					"at least one secret role is required"))
			}
			for _, role := range sortedKeys(cred.Values) {
				if role == "" {
					report(problem(SectionCredentials, name, "values", CodeEmptySecretRole,
						"a secret role must not be empty"))
				}
				if err := validateEnvName(cred.Values[role]); err != nil {
					report(problem(SectionCredentials, name, "values."+role, CodeInvalidEnvName, "%v", err))
				}
			}
		case CredentialTypeKeyring:
			// The message never quotes what was written there: a value under a keyring credential is
			// most likely the secret itself, pasted into the file.
			if len(cred.Values) > 0 {
				report(problem(SectionCredentials, name, "values", CodeSecretValuesNotAllowed, "%s", keyringValuesRule).
					with("type", CredentialTypeKeyring))
			}
		case CredentialTypeVault:
			// Same rule, and the same reason: a value here is most likely the secret itself.
			if len(cred.Values) > 0 {
				report(problem(SectionCredentials, name, "values", CodeSecretValuesNotAllowed, "%s", vaultValuesRule).
					with("type", CredentialTypeVault))
			}
		default:
			report(problem(SectionCredentials, name, "", CodeInvalidCredentialType,
				"type must be one of %s, got %q", strings.Join(CredentialTypes(), ", "), cred.Type).
				refined("type"))
		}
	}

	for _, name := range sortedKeys(c.Connections) {
		conn := c.Connections[name]
		checkName(SectionConnections, "", "connection name", name)
		service, ok := c.Services[conn.Service]
		if !ok {
			report(problem(SectionConnections, name, "service", CodeUnknownService, "unknown service %q", conn.Service))
		}
		cred, credOK := c.Credentials[conn.Credential]
		if !credOK {
			report(problem(SectionConnections, name, "credential", CodeUnknownCredential,
				"unknown credential %q", conn.Credential))
		} else if cred.Forward {
			report(problem(SectionConnections, name, "credential", CodeForwardCredentialAsLogin,
				"credential %q is a forward credential and cannot serve a connection", conn.Credential).
				with("credential", conn.Credential))
		}
		seenForward := map[string]bool{}
		for i, ref := range conn.ForwardSecrets {
			target, exists := c.Credentials[ref]
			switch {
			case !exists:
				// An unknown entry is named by its position: free text here may be a pasted secret.
				report(problem(SectionConnections, name, "forward_secrets", CodeUnknownForwardSecret,
					"unknown credential").at(i))
			case !target.Forward:
				report(problem(SectionConnections, name, "forward_secrets", CodeNotForwardCredential,
					"credential %q is not a forward credential", ref).with("credential", ref))
			}
			if seenForward[ref] {
				dup := problem(SectionConnections, name, "forward_secrets", CodeDuplicateForwardSecret,
					"credential %q is listed more than once", ref)
				if exists {
					dup = dup.with("credential", ref)
				}
				report(dup)
			}
			seenForward[ref] = true
		}
		// A credential that names another provider than the service cannot serve this route: its secret
		// roles are the other provider's. Saying so here keeps the mismatch out of the first call, where
		// it would look like a rejected token.
		if ok && credOK && cred.Provider != "" && cred.Provider != service.Provider {
			report(problem(SectionConnections, name, "", CodeProviderMismatch,
				"service %q belongs to provider %q, credential %q to %q",
				conn.Service, service.Provider, conn.Credential, cred.Provider).
				with("service", conn.Service).with("credential", conn.Credential))
		}
		// Only an env credential can be incomplete in the file. A keyring credential names no roles
		// here; which ones it must supply follows from the provider, and whether they are supplied is a
		// question about the credential store, not about this file.
		if ok && credOK && cred.Type == CredentialTypeEnv {
			for _, role := range c.RequiredSecretRoles(service.Provider) {
				if cred.Values[role] == "" {
					report(problem(SectionConnections, name, "", CodeSecretRoleMissing,
						"provider %q requires the secret role %q in credential %q", service.Provider, role, conn.Credential).
						with("provider", service.Provider).with("role", role).with("credential", conn.Credential))
				}
			}
		}
		if err := validateLine(conn.Description, descriptionRule); err != nil {
			report(problem(SectionConnections, name, "description", CodeInvalidDescription, "%v", err))
		}
		if _, err := ParseExpiryTime(conn.ActiveUntil); conn.ActiveUntil != "" && err != nil {
			report(problem(SectionConnections, name, "active_until", CodeInvalidActiveUntil, "%v", err))
		}
		validateTargetLabels(name, conn, report)
		validatePaths(name, conn.Paths, report)
		if conn.permissionsInCurrent {
			report(problem(SectionConnections, name, "effects", CodePermissionsRenamed,
				"permissions is the former name of effects"))
		} else if conn.bothEffectFields {
			report(problem(SectionConnections, name, "effects", CodePermissionsAndEffects,
				"both effects and its former name permissions are given"))
		}
		if conn.Effects != nil && conn.Tools != nil {
			report(problem(SectionConnections, name, "effects", CodeEffectsAndTools,
				"effects and tools are both given, a connection offers by effect or by selected tools"))
		}
		seenEffects := map[Effect]bool{}
		for _, permission := range conn.Effects {
			if !validEffect(permission) {
				report(problem(SectionConnections, name, "effects", CodeUnknownEffect,
					"unknown permission %q, supported permissions are %s", permission, permissionNames()).
					with("supported", permissionNames()))
				continue
			}
			if seenEffects[permission] {
				report(problem(SectionConnections, name, "effects", CodeDuplicateEffect,
					"permission %q is listed more than once", permission).with("permission", string(permission)))
			}
			seenEffects[permission] = true
		}
		if ok {
			metadata, known := providers.ProviderMetadata(service.Provider)
			targets := conn.TargetValues()
			if metadata.Target.Required && len(targets) == 0 {
				report(problem(SectionConnections, name, "target", CodeTargetRequired,
					"provider %q requires %s", service.Provider, metadata.Target.Label).
					with("provider", service.Provider))
			}
			if strings.TrimSpace(conn.Target) != "" && len(conn.Targets) > 0 {
				report(problem(SectionConnections, name, "", CodeTargetAndTargets,
					"target and targets cannot both be set"))
			}
			if len(conn.Targets) > 0 && !metadata.Target.Multiple {
				report(problem(SectionConnections, name, "targets", CodeTargetsSingleOnly,
					"provider %q accepts only one %s", service.Provider, metadata.Target.Label).
					with("provider", service.Provider))
			}
			seenTargets := map[string]bool{}
			formsValid := true
			// A connection using the targets list gets one path per entry, so several bad entries are all
			// visible at once and each points at the entry that caused it; a single target keeps the plain
			// target path, since there is only ever one entry to point at.
			usesTargetList := len(conn.Targets) > 0
			for i, target := range targets {
				target = strings.TrimSpace(target)
				// at places the problem on the entry of a targets list, or on the plain target key.
				at := func(p Problem) Problem {
					if usesTargetList {
						p.Field = "targets"
						return p.at(i)
					}
					return p
				}
				if target == "" {
					report(at(problem(SectionConnections, name, "target", CodeEmptyTarget, "targets must not be empty")))
					formsValid = false
					continue
				}
				if seenTargets[target] {
					report(at(problem(SectionConnections, name, "target", CodeDuplicateTarget,
						"a target is listed more than once")))
				}
				validate := metadata.Target.Validate
				if !usesTargetList && metadata.Target.ValidateSingle != nil {
					validate = metadata.Target.ValidateSingle
				}
				if validate != nil {
					if err := validate(target); err != nil {
						report(at(problem(SectionConnections, name, "target", CodeInvalidTarget, "%v", err).because(err)))
						formsValid = false
					}
				}
				seenTargets[target] = true
			}
			if metadata.Target.ValidateSet != nil && formsValid && len(targets) > 0 {
				if err := metadata.Target.ValidateSet(targets); err != nil {
					report(problem(SectionConnections, name, "targets", CodeInvalidTargetSet, "%v", err).because(err))
				}
			}
			if metadata.Target.Wildcard != "" && seenTargets[metadata.Target.Wildcard] && len(targets) != 1 {
				report(problem(SectionConnections, name, "targets", CodeWildcardNotAlone,
					"wildcard %q must be the only target", metadata.Target.Wildcard))
			}
			if known && conn.Tools != nil {
				c.validateTools(name, service.Provider, metadata, report)
			}
		}
		validateFiles(name, conn.Files, c.localFilesOf(conn, providers), report)
	}

	for _, provider := range sortedKeys(c.ProviderNotes) {
		if _, ok := providers.ProviderMetadata(provider); !ok {
			report(problem(SectionProviderNotes, provider, "", CodeUnknownProvider,
				"unknown provider, known providers are %s", strings.Join(c.Providers(), ", ")).
				with("known", strings.Join(c.Providers(), ", ")))
		}
		if err := validateLine(c.ProviderNotes[provider], noteRule); err != nil {
			report(problem(SectionProviderNotes, provider, "", CodeInvalidNote, "%v", err))
		}
	}

	for _, domain := range sortedKeys(c.Defaults.Connections) {
		conn := c.Defaults.Connections[domain]
		checkName(SectionDefaults, "connections", "domain", domain)
		if _, ok := c.Connections[conn]; !ok {
			report(problem(SectionDefaults, "", "connections."+domain, CodeUnknownConnection,
				"unknown connection %q", conn))
		}
	}

	if store := c.Defaults.SecretStore; store != "" && store != CredentialTypeKeyring && store != CredentialTypeVault {
		report(problem(SectionDefaults, "", "secret_store", CodeInvalidSecretStore,
			"must be %s or %s, got %q", CredentialTypeKeyring, CredentialTypeVault, store))
	}

	if _, err := parseIdleTimeout(c.Vault.IdleTimeout); err != nil {
		report(problem(SectionVault, "", "idle_timeout", CodeInvalidTimeout, "%v", err))
	}
	if _, err := parseAdminTimeout(c.Vault.AdminTimeout); err != nil {
		report(problem(SectionVault, "", "admin_timeout", CodeInvalidTimeout, "%v", err))
	}
	if c.Logs.RetentionDays < 0 {
		report(problem(SectionLogs, "", "retention_days", CodeInvalidRetention,
			"must be a positive integer, got %d", c.Logs.RetentionDays))
	}

	c.validateIDs(report)

	return errors.Join(problems...)
}

// MinForwardValueLength is the shortest value a field of a forward credential may hold. A shorter value would
// make the redactor replace common text wherever it occurs.
const MinForwardValueLength = 4

// CheckForwardValue reports whether value may be stored in field of the forward credential. Every other
// credential accepts any value here. The message names the field and the rule, never the value.
func (c Credential) CheckForwardValue(field, value string) error {
	if !c.Forward {
		return nil
	}
	if !contains(c.Fields, field) {
		return fmt.Errorf("%q is not a field of this forward credential", field)
	}
	if len(value) < MinForwardValueLength {
		return fmt.Errorf("the value of field %q is shorter than %d characters", field, MinForwardValueLength)
	}
	return nil
}

// validateForward checks the rules of a payload secret: it needs fields, has no provider, and its source is the
// vault or the keyring. A credential without the flag must not carry fields.
func validateForward(name string, cred Credential, report func(Problem)) {
	if !cred.Forward {
		if len(cred.Fields) > 0 {
			report(problem(SectionCredentials, name, "fields", CodeForwardFieldsNotAllowed,
				"only a forward credential has fields"))
		}
		if cred.Description != "" {
			report(problem(SectionCredentials, name, "description", CodeForwardDescriptionNotAllowed,
				"only a forward credential has a description"))
		}
		return
	}
	if err := validateLine(cred.Description, descriptionRule); err != nil {
		report(problem(SectionCredentials, name, "description", CodeInvalidDescription, "%v", err))
	}
	if cred.Provider != "" {
		report(problem(SectionCredentials, name, "provider", CodeForwardProviderNotAllowed,
			"a forward credential belongs to no provider"))
	}
	if cred.Type != CredentialTypeVault && cred.Type != CredentialTypeKeyring {
		report(problem(SectionCredentials, name, "type", CodeInvalidForwardType,
			"a forward credential must be %s or %s", CredentialTypeKeyring, CredentialTypeVault))
	}
	if len(cred.Fields) == 0 {
		report(problem(SectionCredentials, name, "fields", CodeForwardFieldsMissing,
			"a forward credential needs at least one field"))
	}
	seen := map[string]bool{}
	for i, field := range cred.Fields {
		if err := validateName(field); err != nil {
			report(problem(SectionCredentials, name, "fields", CodeInvalidFieldName, "a field name %v", err).at(i))
		}
		if seen[field] {
			report(problem(SectionCredentials, name, "fields", CodeDuplicateField,
				"a field is listed more than once").at(i))
		}
		seen[field] = true
	}
}

// validateTools checks the tools allow-list of one connection against the tools its provider registered.
// Every entry must be a registered tool of that provider, listed once. A file that still carries the former
// permissions next to the tools keeps its former bound: a tool whose effect they exclude was never offered,
// so it is refused rather than offered now. An ID the provider lists as removed is skipped, and ToolWarnings
// names it. Only registered IDs are quoted; an unknown entry is named by its position, because free text in
// a configuration file is where a pasted secret ends up.
func (c *Config) validateTools(name, provider string, metadata ProviderMetadata, report func(Problem)) {
	effects := map[string]Effect{}
	for _, tool := range metadata.Tools {
		effects[tool.ID] = tool.Effect
	}
	owners := map[string]string{}
	for _, other := range c.providerCatalog().ProviderMetadataAll() {
		for _, tool := range other.Tools {
			owners[tool.ID] = other.ID
		}
	}
	legacy := c.Connections[name].legacyEffects
	seen := map[string]bool{}
	for i, tool := range c.Connections[name].Tools {
		effect, registered := effects[tool]
		switch {
		case contains(metadata.RemovedTools, tool):
			// Ignored, and named by ToolWarnings.
		case registered && seen[tool]:
			report(problem(SectionConnections, name, "tools", CodeDuplicateTool,
				"tool %q is listed more than once", tool).with("tool", tool))
		case registered && legacy != nil && !slices.Contains(legacy, effect):
			report(problem(SectionConnections, name, "tools", CodeToolEffectNotPermitted,
				"tool %q has effect %s, which the former permissions of the connection do not allow", tool, effect).
				with("tool", tool).with("effect", string(effect)))
		case registered:
		case owners[tool] != "":
			report(problem(SectionConnections, name, "tools", CodeToolOfOtherProvider,
				"tool %q belongs to provider %q, not to %q", tool, owners[tool], provider).
				with("tool", tool).with("owner", owners[tool]).with("provider", provider))
		default:
			report(problem(SectionConnections, name, "tools", CodeUnknownToolEntry,
				"entry %d is not a registered tool of provider %q", i+1, provider).
				refined("tools").at(i+1).with("provider", provider))
		}
		seen[tool] = true
	}
}

// TargetValues returns the configured target boundary in declaration order. Existing single-target
// connections keep using Target; providers that explicitly support an allow-list use Targets instead.
func (c Connection) TargetValues() []string {
	if len(c.Targets) > 0 {
		return append([]string(nil), c.Targets...)
	}
	if strings.TrimSpace(c.Target) == "" {
		return nil
	}
	return []string{c.Target}
}

// PruneTargetLabels returns the labels of the connection whose target is still one of its targets, as a copy
// that shares nothing with the connection, or nil when none is left. It is the one place that decides a label
// is orphaned; every write by Qatlas goes through it (see Config.SetConnection), while loading never repairs a
// hand-written orphan but reports it (see validateTargetLabels).
func (c Connection) PruneTargetLabels() map[string]string {
	var kept map[string]string
	for _, target := range c.TargetValues() {
		if label, ok := c.TargetLabels[target]; ok {
			if kept == nil {
				kept = map[string]string{}
			}
			kept[target] = label
		}
	}
	return kept
}

// validateTargetLabels checks the labels of one connection: each names a target of the connection and is one
// non-empty line. A problem never quotes a target or a label, both are free text.
func validateTargetLabels(name string, conn Connection, report func(Problem)) {
	if len(conn.TargetLabels) == 0 {
		return
	}
	targets := map[string]bool{}
	for _, target := range conn.TargetValues() {
		targets[target] = true
	}
	keys := make([]string, 0, len(conn.TargetLabels))
	for key := range conn.TargetLabels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		label := conn.TargetLabels[key]
		switch {
		case !targets[key]:
			report(problem(SectionConnections, name, "target_labels", CodeUnknownTargetLabel,
				"a label names a target the connection does not have"))
		case normalizeDescription(label) == "" && !strings.ContainsAny(label, "\n\r"):
			report(problem(SectionConnections, name, "target_labels", CodeEmptyTargetLabel,
				"a target label must not be empty"))
		default:
			if err := validateLine(label, targetLabelRule); err != nil {
				report(problem(SectionConnections, name, "target_labels", CodeInvalidTargetLabel, "%v", err))
			}
		}
	}
}

func providerCatalog(providers []ProviderCatalog) ProviderCatalog {
	if len(providers) > 0 && providers[0] != nil {
		return providers[0]
	}
	return emptyProviderCatalog{}
}

// ProviderCatalog returns the provider catalog the configuration was loaded with, an empty one when there
// is none.
func (c *Config) ProviderCatalog() ProviderCatalog { return c.providerCatalog() }

func (c *Config) providerCatalog() ProviderCatalog {
	if c.providers != nil {
		return c.providers
	}
	return emptyProviderCatalog{}
}

// ServiceBaseURL returns the endpoint a service reaches: its base_url, or the default endpoint of its
// provider when the file leaves base_url out. A provider declares a default only for an endpoint every
// installation can use, such as its public API; a provider without one, like a self-hosted system, still
// needs base_url. The default is applied where the endpoint is read, never written into the file.
func (c *Config) ServiceBaseURL(s Service) string {
	if s.BaseURL != "" {
		return s.BaseURL
	}
	metadata, _ := c.providerCatalog().ProviderMetadata(s.Provider)
	return metadata.DefaultBaseURL
}

// Providers returns the registered provider IDs in deterministic order.
func (c *Config) Providers() []string {
	metadata := c.providerCatalog().ProviderMetadataAll()
	providers := make([]string, len(metadata))
	for i := range metadata {
		providers[i] = metadata[i].ID
	}
	return providers
}

// ProviderMetadata returns one registered provider's configuration contract.
func (c *Config) ProviderMetadata(provider string) (ProviderMetadata, bool) {
	return c.providerCatalog().ProviderMetadata(provider)
}

// ProviderSecretRoles returns the roles one provider defines, optional ones included, in declaration order.
func (c *Config) ProviderSecretRoles(provider string) []string {
	metadata, _ := c.ProviderMetadata(provider)
	roles := make([]string, len(metadata.SecretRoles))
	for i := range metadata.SecretRoles {
		roles[i] = metadata.SecretRoles[i].Name
	}
	return roles
}

// RequiredSecretRoles returns the roles one provider requires, in declaration order: every role it defines
// except the optional ones.
func (c *Config) RequiredSecretRoles(provider string) []string {
	metadata, _ := c.ProviderMetadata(provider)
	roles := make([]string, 0, len(metadata.SecretRoles))
	for _, role := range metadata.SecretRoles {
		if !role.Optional {
			roles = append(roles, role.Name)
		}
	}
	return roles
}

// SecretRoleOptional reports whether provider declares role optional. A role the provider does not define,
// and every role of an unknown provider, is not optional.
func (c *Config) SecretRoleOptional(provider, role string) bool {
	metadata, _ := c.ProviderMetadata(provider)
	for _, candidate := range metadata.SecretRoles {
		if candidate.Name == role {
			return candidate.Optional
		}
	}
	return false
}

// SecretRoles returns all registered roles in deterministic order.
func (c *Config) SecretRoles() []string {
	roles := map[string]bool{}
	for _, metadata := range c.providerCatalog().ProviderMetadataAll() {
		for _, role := range metadata.SecretRoles {
			roles[role.Name] = true
		}
	}
	return sortedKeys(roles)
}

// SecretRolesOf returns the secret roles of one provider, sorted. An unknown provider has none.
func (c *Config) SecretRolesOf(provider string) []string {
	metadata, ok := c.providerCatalog().ProviderMetadata(provider)
	if !ok {
		return nil
	}
	roles := make([]string, 0, len(metadata.SecretRoles))
	for _, role := range metadata.SecretRoles {
		roles = append(roles, role.Name)
	}
	sort.Strings(roles)
	return roles
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func validEffect(permission Effect) bool {
	for _, candidate := range Effects() {
		if permission == candidate {
			return true
		}
	}
	return false
}

func permissionNames() string {
	names := make([]string, len(Effects()))
	for i, permission := range Effects() {
		names[i] = string(permission)
	}
	return strings.Join(names, ", ")
}

// ConnectionEffects returns the effective effects of a connection: what it offers, as effects. Selecting by
// effect, it is the effects field, or the provider's default effects when the field is missing so adding a
// new mutation never grants it silently; an explicitly empty list offers nothing. Selecting tools, the
// effects are derived from the registered tools it lists, in the order of Effects, and none is stored.
// Approvals and agent tokens cover exactly these.
func (c *Config) ConnectionEffects(name string) []Effect {
	conn, ok := c.Connections[name]
	if !ok {
		return nil
	}
	if conn.Tools != nil {
		metadata, _ := c.ProviderMetadata(c.Services[conn.Service].Provider)
		return EffectsOfTools(metadata, conn.Tools)
	}
	if conn.Effects != nil {
		return append(make([]Effect, 0, len(conn.Effects)), conn.Effects...)
	}
	service := c.Services[conn.Service]
	metadata, _ := c.ProviderMetadata(service.Provider)
	if len(metadata.DefaultEffects) > 0 {
		return append([]Effect(nil), metadata.DefaultEffects...)
	}
	return []Effect{EffectRead}
}

// EffectsOfTools returns the effects of the tools of metadata that tools names, in the order of Effects and
// never nil. A name the provider registers no tool for adds none.
func EffectsOfTools(metadata ProviderMetadata, tools []string) []Effect {
	derived := map[Effect]bool{}
	for _, tool := range metadata.Tools {
		if slices.Contains(tools, tool.ID) {
			derived[tool.Effect] = true
		}
	}
	effects := []Effect{}
	for _, effect := range Effects() {
		if derived[effect] {
			effects = append(effects, effect)
		}
	}
	return effects
}

// NarrowedEffects names, sorted, the connections of a configuration read from an older schema that list tools
// and whose former effects (the permissions, or the provider's defaults without them) were broader than the
// effects the tools derive now. A change of the effective effects changes the approval fingerprint of such a
// connection. It answers only for a configuration read before the migration that writes the current schema.
func (c *Config) NarrowedEffects() []string {
	var names []string
	for _, name := range sortedKeys(c.Connections) {
		conn := c.Connections[name]
		if conn.Tools == nil {
			continue
		}
		former := conn.legacyEffects
		if former == nil {
			metadata, _ := c.ProviderMetadata(c.Services[conn.Service].Provider)
			former = metadata.DefaultEffects
			if len(former) == 0 {
				former = []Effect{EffectRead}
			}
		}
		derived := c.ConnectionEffects(name)
		for _, effect := range former {
			if !slices.Contains(derived, effect) {
				names = append(names, name)
				break
			}
		}
	}
	return names
}

// Refusal names why a connection does not offer a tool. The values are stable: discovery publishes them
// and an unsupported-capability diagnostic carries them, so a caller branches on the value, not the text.
type Refusal string

// The refusals, in the order ConnectionRefusal checks them.
const (
	// RefusalEffect: the connection offers by effect, and its effects do not include the tool's effect.
	RefusalEffect Refusal = "effect-not-permitted"
	// RefusalToolAllowList: the tool requires an allow-list, and the connection offers by effect.
	RefusalToolAllowList Refusal = "requires-tool-allow-list"
	// RefusalToolsList: the connection offers selected tools, and its list does not name the tool.
	RefusalToolsList Refusal = "not-in-tools-list"
	// RefusalNoLocalFiles: the tool reads or writes local files, and the connection releases no directory
	// for that direction.
	RefusalNoLocalFiles Refusal = "no-local-files"
	// RefusalNoConnection: no configured connection of the tool's provider exists.
	RefusalNoConnection Refusal = "no-connection"
	// RefusalOtherProvider: the connection belongs to another provider than the tool.
	RefusalOtherProvider Refusal = "other-provider"
	// RefusalOutsideTargets: the connection's targets do not reach the tool, which the provider judges.
	RefusalOutsideTargets Refusal = "outside-targets"
	// RefusalDisabled: the connection is switched off or past its active_until, and would offer the tool
	// otherwise.
	RefusalDisabled Refusal = "connection-disabled"
)

// ConnectionRefusal answers whether the local configuration exposes one operation on a connection, and if
// not, why. now is the time the caller reads the clock at; a connection past its active_until refuses like a
// disabled one, and the package itself never reads the system time. The empty refusal means the connection
// offers the tool: by effect, the tool's effect is among the connection's effects and it does not require
// an allow-list; by selected tools, its ID is among them. An unknown connection offers nothing.
func (c *Config) ConnectionRefusal(name string, tool ToolMetadata, now time.Time) Refusal {
	conn, ok := c.Connections[name]
	if !ok {
		return RefusalNoConnection
	}
	if refusal := c.selectionRefusal(conn, name, tool); refusal != "" || !conn.Off(now) {
		return refusal
	}
	return RefusalDisabled
}

// selectionRefusal is the refusal by the connection's selection alone, ignoring whether it is disabled, so a
// disabled connection reports connection-disabled only for a tool it would otherwise offer.
func (c *Config) selectionRefusal(conn Connection, name string, tool ToolMetadata) Refusal {
	if refusal := c.listRefusal(conn, name, tool); refusal != "" {
		return refusal
	}
	return c.reachRefusal(conn, tool)
}

// reachRefusal is the last stage of the selection: a tool the selection would offer is still refused when
// the connection's targets do not reach it. The selection itself stays as configured, so the tool is offered
// again once the targets reach it.
func (c *Config) reachRefusal(conn Connection, tool ToolMetadata) Refusal {
	metadata, _ := c.ProviderMetadata(c.Services[conn.Service].Provider)
	if reached, _ := metadata.Target.Reach(tool.ID, conn.TargetValues()); !reached {
		return RefusalOutsideTargets
	}
	return ""
}

// listRefusal is the refusal by effects, tools list, allow-list and local files, before the targets.
func (c *Config) listRefusal(conn Connection, name string, tool ToolMetadata) Refusal {
	if conn.Tools != nil {
		if !contains(conn.Tools, tool.ID) {
			return RefusalToolsList
		}
	} else if !slices.Contains(c.ConnectionEffects(name), tool.Effect) {
		return RefusalEffect
	} else if tool.RequiresToolAllowList {
		return RefusalToolAllowList
	}
	switch {
	case tool.LocalFiles == LocalFilesRead && len(conn.Files.Read) == 0,
		tool.LocalFiles == LocalFilesWrite && len(conn.Files.Write) == 0:
		return RefusalNoLocalFiles
	}
	return ""
}

// ConnectionAllows reports whether a connection offers one operation, by the rule of ConnectionRefusal.
// Every discovery and invoke path asks this one question, so none of them can offer what another refuses.
func (c *Config) ConnectionAllows(name string, tool ToolMetadata, now time.Time) bool {
	return c.ConnectionRefusal(name, tool, now) == ""
}

// IdleWarning says why a connection offers no tool at all, or returns "" when it offers at least one. Such
// a connection is valid and stays configured, but no agent can use it, and discovery counts it among the
// configured connections of its provider only. The text names the connection and the reason, never a
// secret, and is the same wherever it is shown.
func (c *Config) IdleWarning(name string) string {
	conn, ok := c.Connections[name]
	if !ok || conn.Disabled {
		// A disabled connection is switched off on purpose, which is no problem to warn about. A connection
		// past its active_until is judged by its selection alone, as it is not idle for being expired.
		return ""
	}
	// A provider this build registers no tool for leaves nothing to judge the connection by.
	metadata, _ := c.ProviderMetadata(c.Services[conn.Service].Provider)
	if len(metadata.Tools) == 0 {
		return ""
	}
	for _, tool := range metadata.Tools {
		if c.selectionRefusal(conn, name, tool) == "" {
			return ""
		}
	}
	reason := "its effects or tools list allow none of its provider's tools"
	for _, tool := range metadata.Tools {
		if c.listRefusal(conn, name, tool) == "" {
			reason = "its targets reach none of the tools its selection allows"
			break
		}
	}
	switch {
	case conn.Effects != nil && len(conn.Effects) == 0:
		reason = "its effects are empty ('effects: []')"
	case conn.Tools != nil && len(conn.Tools) == 0:
		reason = "its tools list is empty ('tools: []')"
	}
	return fmt.Sprintf("connection %q offers no tool: %s, so no agent can use it", name, reason)
}

// SecretRoleDescriptionOf returns the help one provider authors for one of its roles. Role names are not
// unique across providers ("token" exists in several), so a role is only explained by the provider it
// belongs to. Without a provider the help is given only when every provider that defines the role words it
// the same; an ambiguous role gets none rather than another provider's text.
func (c *Config) SecretRoleDescriptionOf(provider, role string) string {
	if provider != "" {
		metadata, ok := c.providerCatalog().ProviderMetadata(provider)
		if !ok {
			return ""
		}
		for _, candidate := range metadata.SecretRoles {
			if candidate.Name == role {
				return candidate.Description
			}
		}
		return ""
	}
	found := ""
	for _, metadata := range c.providerCatalog().ProviderMetadataAll() {
		for _, candidate := range metadata.SecretRoles {
			if candidate.Name != role {
				continue
			}
			if found != "" && found != candidate.Description {
				return ""
			}
			found = candidate.Description
		}
	}
	return found
}

func validateBaseURL(raw string) error {
	if raw == "" {
		return errors.New("must not be empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("is not a valid URL")
	}
	// http stays allowed so a local test server can be configured; providers enforce transport security.
	// The scheme is not quoted: in "user:secret@host" the parser reads the user as the scheme.
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("must use scheme http or https")
	}
	if u.Host == "" {
		return errors.New("must contain a host")
	}
	return nil
}

// envNameRule states what a credential value must look like.
//
// The message never quotes the offending input. A user who pastes a secret into a credential field instead
// of the variable name would otherwise see that secret echoed in the editor and in `config validate`, and
// the redactor cannot catch it: it only knows values that were resolved from an environment variable.
const envNameRule = "must be the name of an environment variable, written with letters, digits and " +
	"underscores and not starting with a digit, never the secret value itself"

// keyringValuesRule states why a keyring credential carries no values. Like envNameRule it never quotes
// the input: whatever stands under a keyring credential is most likely the secret itself.
const keyringValuesRule = "must be absent for a keyring credential, whose secrets live in the credential " +
	"store; set them with 'qatlas credential set'"

// vaultValuesRule is keyringValuesRule's counterpart for a vault credential.
const vaultValuesRule = "must be absent for a vault credential, whose secrets live in the vault; " +
	"set them with 'qatlas credential set'"

// maxDescriptionLength bounds a connection description and a provider note. Each labels a route or a
// provider for the person choosing one, neither is documentation, and discovery repeats them, so they stay
// short enough to read at a glance.
const maxDescriptionLength = 200

// descriptionRule and noteRule state what a connection description and a provider note must look like.
// Like the other rules they never quote the input: both are free text, and free text is exactly where a
// secret gets pasted by accident.
const (
	descriptionRule = "a connection description must be a single line of at most 200 characters; " +
		"discovery publishes it, so it must never carry a secret or personal data"
	noteRule = "a provider note must be a single line of at most 200 characters; " +
		"discovery publishes it, so it must never carry a secret or personal data"
)

// targetLabelRule states what a target label must look like. It never quotes the input either.
const targetLabelRule = "a target label must be a single line of at most 200 characters; " +
	"it is for the person only and is never shown to an agent"

// validateLine refuses a description or note that is not one short line, naming rule. An empty text is a
// missing one and needs no rule.
func validateLine(text, rule string) error {
	if strings.ContainsAny(text, "\n\r") {
		return errors.New("must not contain a line break: " + rule)
	}
	if length := utf8.RuneCountInString(normalizeDescription(text)); length > maxDescriptionLength {
		return fmt.Errorf("is %d characters long: %s", length, rule)
	}
	return nil
}

// normalizeDescription drops the blanks at the edges and collapses the runs between words, so the same
// sentence typed with different spacing is stored, measured and published as one text. A line break is
// deliberately not collapsed: validateLine refuses it, because a description is one line.
func normalizeDescription(text string) string {
	return strings.Join(strings.FieldsFunc(text, func(r rune) bool { return r == ' ' || r == '\t' }), " ")
}

// nameRule states the character set of every service, credential, connection and domain name. These names
// are configuration keys and `--connection` arguments, so they stay free of quoting and shell surprises.
const nameRule = "must consist of letters, digits, '-', '_' or '.' and must start and end with a letter or a digit"

func validateEnvName(name string) error {
	if name == "" {
		return errors.New("must name an environment variable")
	}
	for i, r := range name {
		digit := r >= '0' && r <= '9'
		letter := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
		if letter || r == '_' || (digit && i > 0) {
			continue
		}
		return errors.New(envNameRule)
	}
	return nil
}

func validateName(name string) error {
	if name == "" {
		return errors.New("must not be empty")
	}
	for _, r := range name {
		if !isNameRune(r) {
			return errors.New(nameRule)
		}
	}
	// Every allowed rune is one ASCII byte, so the first and last byte are the first and last rune.
	if !isAlphanumeric(rune(name[0])) || !isAlphanumeric(rune(name[len(name)-1])) {
		return errors.New(nameRule)
	}
	return nil
}

func isNameRune(r rune) bool {
	return isAlphanumeric(r) || r == '-' || r == '_' || r == '.'
}

func isAlphanumeric(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ToolWarnings names every former provider or tool ID that loading read as its current name, then every
// tools entry that a provider has removed, one line per entry, removed ones sorted by connection. Such an
// entry stays valid, so an older file keeps working.
func (c *Config) ToolWarnings() []string {
	var warnings []string
	for _, renaming := range c.renamings {
		warnings = append(warnings, renaming.warning())
	}
	for _, name := range sortedKeys(c.Connections) {
		conn := c.Connections[name]
		metadata, _ := c.ProviderMetadata(c.Services[conn.Service].Provider)
		for _, tool := range conn.Tools {
			if contains(metadata.RemovedTools, tool) {
				warnings = append(warnings, fmt.Sprintf("connections.%s.tools: tool %q was removed from qatlas "+
					"and is ignored", name, tool))
			}
		}
	}
	return warnings
}
