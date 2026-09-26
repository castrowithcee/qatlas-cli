// Package config loads and validates the qatlas configuration and resolves the connection a command
// should use. It never reads, stores, or reports secret values: a credential only says where its secrets
// come from, either by naming environment variables or by pointing at the system credential store.
// Resolving a secret from that description is the job of package secret.
package config

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	yaml "go.yaml.in/yaml/v3"
)

// Version is the only configuration schema version this build understands.
const Version = 1

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
	providers     ProviderCatalog       `yaml:"-"`
}

// Service is a technical API endpoint of one provider. BaseURL may be left out for a provider that declares
// a default endpoint; ServiceBaseURL returns the endpoint that applies.
type Service struct {
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
type Credential struct {
	Provider string            `yaml:"provider,omitempty"`
	Type     string            `yaml:"type"`
	Values   map[string]string `yaml:"values,omitempty"`
}

// Connection binds exactly one service to exactly one credential. Target is an optional provider-specific
// scope inside that service.
//
// Permissions and Tools are two independent local allow-lists, and an operation is offered only when both
// admit it. Permissions admits operation effects. Tools, when present, admits individual provider-qualified
// operation IDs of this connection's provider; missing keeps every operation the permissions admit, except
// those that require an allow-list, and an explicit empty list admits none. A tool registered later is never
// added to an existing list.
//
// Description is optional prose the user maintains. A name like "personal" or "crm-internal" is a stable
// selector, not an explanation, so this one line says what the route is for and lets a reader tell two
// routes of one provider apart. Discovery publishes it verbatim and nothing is ever sent to a provider,
// which is why it is configuration, never a secret and never personal data.
type Connection struct {
	Service     string       `yaml:"service"`
	Credential  string       `yaml:"credential"`
	Target      string       `yaml:"target,omitempty"`
	Targets     []string     `yaml:"targets,omitempty"`
	Description string       `yaml:"description,omitempty"`
	Permissions []Permission `yaml:"permissions,omitempty"`
	Tools       []string     `yaml:"tools,omitempty"`
}

// MarshalYAML preserves the semantic difference between a missing permissions or tools field (provider
// compatibility default, or every tool the permissions admit) and an explicit empty list (deny all). A plain
// omitempty slice cannot represent both states when a configuration is saved through the TUI.
func (c Connection) MarshalYAML() (any, error) {
	type wire struct {
		Service     string        `yaml:"service"`
		Credential  string        `yaml:"credential"`
		Target      string        `yaml:"target,omitempty"`
		Targets     []string      `yaml:"targets,omitempty"`
		Description string        `yaml:"description,omitempty"`
		Permissions *[]Permission `yaml:"permissions,omitempty"`
		Tools       *[]string     `yaml:"tools,omitempty"`
	}
	var permissions *[]Permission
	if c.Permissions != nil {
		copy := append(make([]Permission, 0, len(c.Permissions)), c.Permissions...)
		permissions = &copy
	}
	var tools *[]string
	if c.Tools != nil {
		copy := append(make([]string, 0, len(c.Tools)), c.Tools...)
		tools = &copy
	}
	return wire{Service: c.Service, Credential: c.Credential, Target: c.Target, Targets: c.Targets,
		Description: c.Description, Permissions: permissions, Tools: tools}, nil
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
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &NotFoundError{Path: path}
		}
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	defer f.Close()

	cfg, err := Decode(f, providers...)
	if err != nil {
		return nil, &InvalidError{Path: path, Err: err}
	}
	return cfg, nil
}

// Decode reads a configuration from r. Unknown keys and duplicate keys are errors.
func Decode(r io.Reader, providers ...ProviderCatalog) (*Config, error) {
	dec := yaml.NewDecoder(r)
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

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	// A decoded configuration is directly editable, and an absent section round-trips to the same model
	// as an empty one.
	cfg.ensure()
	cfg.normalize()
	return &cfg, nil
}

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

// Validate reports every schema and reference problem at once. Messages name configuration keys and
// environment variable names only, never secret values.
func (c *Config) Validate() error {
	providers := c.providerCatalog()
	var problems []error
	report := func(format string, args ...any) {
		problems = append(problems, fmt.Errorf(format, args...))
	}
	// checkName reports an unusable name under its own key, so several bad names are all visible at once.
	checkName := func(section, kind, name string) {
		err := validateName(name)
		switch {
		case err == nil:
		case name == "":
			report("%s: a %s %v", section, kind, err)
		default:
			report("%s.%s: a %s %v", section, name, kind, err)
		}
	}

	if c.Version != Version {
		report("version: got %d, want %d", c.Version, Version)
	}

	for _, name := range sortedKeys(c.Services) {
		s := c.Services[name]
		checkName("services", "service name", name)
		if s.Provider == "" {
			report("services.%s: provider must not be empty", name)
		} else if _, ok := providers.ProviderMetadata(s.Provider); !ok {
			report("services.%s: unknown provider %q, known providers are %s",
				name, s.Provider, strings.Join(c.Providers(), ", "))
		}
		if err := validateBaseURL(c.ServiceBaseURL(s)); err != nil {
			report("services.%s.base_url: %v", name, err)
		}
	}

	for _, name := range sortedKeys(c.Credentials) {
		cred := c.Credentials[name]
		checkName("credentials", "credential name", name)
		if cred.Provider != "" {
			if _, ok := providers.ProviderMetadata(cred.Provider); !ok {
				report("credentials.%s: unknown provider %q, known providers are %s",
					name, cred.Provider, strings.Join(c.Providers(), ", "))
			} else if roles := c.SecretRolesOf(cred.Provider); len(roles) > 0 {
				// A role the named provider does not define would never be read. Naming the provider is
				// what makes that visible here instead of at the first failing call.
				for _, role := range sortedKeys(cred.Values) {
					if !contains(roles, role) {
						report("credentials.%s.values.%s: %s does not define this secret role, it uses %s",
							name, role, cred.Provider, strings.Join(roles, ", "))
					}
				}
			}
		}
		switch cred.Type {
		case CredentialTypeEnv:
			if len(cred.Values) == 0 {
				report("credentials.%s.values: at least one secret role is required", name)
			}
			for _, role := range sortedKeys(cred.Values) {
				if role == "" {
					report("credentials.%s.values: a secret role must not be empty", name)
				}
				if err := validateEnvName(cred.Values[role]); err != nil {
					report("credentials.%s.values.%s: %v", name, role, err)
				}
			}
		case CredentialTypeKeyring:
			// The message never quotes what was written there: a value under a keyring credential is
			// most likely the secret itself, pasted into the file.
			if len(cred.Values) > 0 {
				report("credentials.%s.values: %s", name, keyringValuesRule)
			}
		case CredentialTypeVault:
			// Same rule, and the same reason: a value here is most likely the secret itself.
			if len(cred.Values) > 0 {
				report("credentials.%s.values: %s", name, vaultValuesRule)
			}
		default:
			report("credentials.%s: type must be one of %s, got %q",
				name, strings.Join(CredentialTypes(), ", "), cred.Type)
		}
	}

	for _, name := range sortedKeys(c.Connections) {
		conn := c.Connections[name]
		checkName("connections", "connection name", name)
		service, ok := c.Services[conn.Service]
		if !ok {
			report("connections.%s.service: unknown service %q", name, conn.Service)
		}
		cred, credOK := c.Credentials[conn.Credential]
		if !credOK {
			report("connections.%s.credential: unknown credential %q", name, conn.Credential)
		}
		// A credential that names another provider than the service cannot serve this route: its secret
		// roles are the other provider's. Saying so here keeps the mismatch out of the first call, where
		// it would look like a rejected token.
		if ok && credOK && cred.Provider != "" && cred.Provider != service.Provider {
			report("connections.%s: service %q belongs to provider %q, credential %q to %q",
				name, conn.Service, service.Provider, conn.Credential, cred.Provider)
		}
		// Only an env credential can be incomplete in the file. A keyring credential names no roles
		// here; which ones it must supply follows from the provider, and whether they are supplied is a
		// question about the credential store, not about this file.
		if ok && credOK && cred.Type == CredentialTypeEnv {
			for _, role := range c.ProviderSecretRoles(service.Provider) {
				if cred.Values[role] == "" {
					report("connections.%s: provider %q requires the secret role %q in credential %q",
						name, service.Provider, role, conn.Credential)
				}
			}
		}
		if err := validateLine(conn.Description, descriptionRule); err != nil {
			report("connections.%s.description: %v", name, err)
		}
		seenPermissions := map[Permission]bool{}
		for _, permission := range conn.Permissions {
			if !validPermission(permission) {
				report("connections.%s.permissions: unknown permission %q, supported permissions are %s",
					name, permission, permissionNames())
				continue
			}
			if seenPermissions[permission] {
				report("connections.%s.permissions: permission %q is listed more than once", name, permission)
			}
			seenPermissions[permission] = true
		}
		if ok {
			metadata, known := providers.ProviderMetadata(service.Provider)
			targets := conn.TargetValues()
			if metadata.Target.Required && len(targets) == 0 {
				report("connections.%s.target: provider %q requires %s", name, service.Provider,
					metadata.Target.Label)
			}
			if strings.TrimSpace(conn.Target) != "" && len(conn.Targets) > 0 {
				report("connections.%s: target and targets cannot both be set", name)
			}
			if len(conn.Targets) > 0 && !metadata.Target.Multiple {
				report("connections.%s.targets: provider %q accepts only one %s", name,
					service.Provider, metadata.Target.Label)
			}
			seenTargets := map[string]bool{}
			formsValid := true
			for _, target := range targets {
				target = strings.TrimSpace(target)
				if target == "" {
					report("connections.%s.targets: targets must not be empty", name)
					formsValid = false
					continue
				}
				if seenTargets[target] {
					report("connections.%s.targets: a target is listed more than once", name)
				}
				if metadata.Target.Validate != nil {
					if err := metadata.Target.Validate(target); err != nil {
						report("connections.%s.target: %v", name, err)
						formsValid = false
					}
				}
				seenTargets[target] = true
			}
			if metadata.Target.ValidateSet != nil && formsValid && len(targets) > 0 {
				if err := metadata.Target.ValidateSet(targets); err != nil {
					report("connections.%s.targets: %v", name, err)
				}
			}
			if metadata.Target.Wildcard != "" && seenTargets[metadata.Target.Wildcard] && len(targets) != 1 {
				report("connections.%s.targets: wildcard %q must be the only target", name,
					metadata.Target.Wildcard)
			}
			if known && conn.Tools != nil {
				c.validateTools(name, service.Provider, metadata, report)
			}
		}
	}

	for _, provider := range sortedKeys(c.ProviderNotes) {
		if _, ok := providers.ProviderMetadata(provider); !ok {
			report("provider_notes.%s: unknown provider, known providers are %s", provider,
				strings.Join(c.Providers(), ", "))
		}
		if err := validateLine(c.ProviderNotes[provider], noteRule); err != nil {
			report("provider_notes.%s: %v", provider, err)
		}
	}

	for _, domain := range sortedKeys(c.Defaults.Connections) {
		conn := c.Defaults.Connections[domain]
		checkName("defaults.connections", "domain", domain)
		if _, ok := c.Connections[conn]; !ok {
			report("defaults.connections.%s: unknown connection %q", domain, conn)
		}
	}

	if store := c.Defaults.SecretStore; store != "" && store != CredentialTypeKeyring && store != CredentialTypeVault {
		report("defaults.secret_store: must be %s or %s, got %q",
			CredentialTypeKeyring, CredentialTypeVault, store)
	}

	return errors.Join(problems...)
}

// validateTools checks the tools allow-list of one connection against the tools its provider registered.
// Every entry must be a registered tool of that provider, listed once, whose effect the connection's
// permissions admit: an entry the permissions exclude could never be offered, so it is refused rather than
// kept as a line that reads like a grant. Only registered IDs are quoted; an unknown entry is named by its
// position, because free text in a configuration file is where a pasted secret ends up.
func (c *Config) validateTools(name, provider string, metadata ProviderMetadata, report func(string, ...any)) {
	effects := map[string]Permission{}
	for _, tool := range metadata.Tools {
		effects[tool.ID] = tool.Effect
	}
	owners := map[string]string{}
	for _, other := range c.providerCatalog().ProviderMetadataAll() {
		for _, tool := range other.Tools {
			owners[tool.ID] = other.ID
		}
	}
	permitted := map[Permission]bool{}
	for _, permission := range c.ConnectionPermissions(name) {
		permitted[permission] = true
	}
	seen := map[string]bool{}
	for i, tool := range c.Connections[name].Tools {
		effect, registered := effects[tool]
		switch {
		case registered && seen[tool]:
			report("connections.%s.tools: tool %q is listed more than once", name, tool)
		case registered && !permitted[effect]:
			report("connections.%s.tools: tool %q has effect %s, which the connection's permissions do not "+
				"allow", name, tool, effect)
		case registered:
		case owners[tool] != "":
			report("connections.%s.tools: tool %q belongs to provider %q, not to %q", name, tool,
				owners[tool], provider)
		default:
			report("connections.%s.tools: entry %d is not a registered tool of provider %q", name, i+1,
				provider)
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

func providerCatalog(providers []ProviderCatalog) ProviderCatalog {
	if len(providers) > 0 && providers[0] != nil {
		return providers[0]
	}
	return emptyProviderCatalog{}
}

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

// ProviderSecretRoles returns the roles one provider requires, in declaration order.
func (c *Config) ProviderSecretRoles(provider string) []string {
	metadata, _ := c.ProviderMetadata(provider)
	roles := make([]string, len(metadata.SecretRoles))
	for i := range metadata.SecretRoles {
		roles[i] = metadata.SecretRoles[i].Name
	}
	return roles
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

func validPermission(permission Permission) bool {
	for _, candidate := range Permissions() {
		if permission == candidate {
			return true
		}
	}
	return false
}

func permissionNames() string {
	names := make([]string, len(Permissions()))
	for i, permission := range Permissions() {
		names[i] = string(permission)
	}
	return strings.Join(names, ", ")
}

// ConnectionPermissions returns the explicit local permissions of a connection. A missing field keeps
// only the provider's pre-permission operation classes, so adding a new mutation never grants it silently.
// An explicitly empty list disables every operation on the connection.
func (c *Config) ConnectionPermissions(name string) []Permission {
	conn, ok := c.Connections[name]
	if !ok {
		return nil
	}
	if conn.Permissions != nil {
		return append(make([]Permission, 0, len(conn.Permissions)), conn.Permissions...)
	}
	service := c.Services[conn.Service]
	metadata, _ := c.ProviderMetadata(service.Provider)
	if len(metadata.DefaultPermissions) > 0 {
		return append([]Permission(nil), metadata.DefaultPermissions...)
	}
	return []Permission{PermissionRead}
}

// Refusal names why a connection does not offer a tool. The values are stable: discovery publishes them
// and an unsupported-capability diagnostic carries them, so a caller branches on the value, not the text.
type Refusal string

// The refusals, in the order ConnectionRefusal checks them.
const (
	// RefusalEffect: the connection's permissions do not allow the tool's effect.
	RefusalEffect Refusal = "effect-not-permitted"
	// RefusalToolAllowList: the tool requires an allow-list, and the connection has no tools list.
	RefusalToolAllowList Refusal = "requires-tool-allow-list"
	// RefusalToolsList: the connection has a tools list, and it does not name the tool.
	RefusalToolsList Refusal = "not-in-tools-list"
	// RefusalNoConnection: no configured connection of the tool's provider exists.
	RefusalNoConnection Refusal = "no-connection"
	// RefusalOtherProvider: the connection belongs to another provider than the tool.
	RefusalOtherProvider Refusal = "other-provider"
)

// ConnectionRefusal answers whether the local configuration exposes one operation on a connection, and if
// not, why. The empty refusal means the connection offers the tool: its effect is among the connection's
// permissions and, when the connection lists tools, its ID is among them. A tool that requires an
// allow-list is exposed only by a connection whose tools list names it. The effect is checked first,
// because it is the coarser of the two lists. An unknown connection offers nothing.
func (c *Config) ConnectionRefusal(name string, tool ToolMetadata) Refusal {
	conn, ok := c.Connections[name]
	if !ok {
		return RefusalNoConnection
	}
	permitted := false
	for _, permission := range c.ConnectionPermissions(name) {
		if permission == tool.Effect {
			permitted = true
			break
		}
	}
	switch {
	case !permitted:
		return RefusalEffect
	case conn.Tools == nil && tool.RequiresToolAllowList:
		return RefusalToolAllowList
	case conn.Tools != nil && !contains(conn.Tools, tool.ID):
		return RefusalToolsList
	}
	return ""
}

// ConnectionAllows reports whether a connection offers one operation, by the rule of ConnectionRefusal.
// Every discovery and invoke path asks this one question, so none of them can offer what another refuses.
func (c *Config) ConnectionAllows(name string, tool ToolMetadata) bool {
	return c.ConnectionRefusal(name, tool) == ""
}

// IdleWarning says why a connection offers no tool at all, or returns "" when it offers at least one. Such
// a connection is valid and stays configured, but no agent can use it, and discovery counts it among the
// configured connections of its provider only. The text names the connection and the reason, never a
// secret, and is the same wherever it is shown.
func (c *Config) IdleWarning(name string) string {
	conn, ok := c.Connections[name]
	if !ok {
		return ""
	}
	// A provider this build registers no tool for leaves nothing to judge the connection by.
	metadata, _ := c.ProviderMetadata(c.Services[conn.Service].Provider)
	if len(metadata.Tools) == 0 {
		return ""
	}
	for _, tool := range metadata.Tools {
		if c.ConnectionAllows(name, tool) {
			return ""
		}
	}
	reason := "its permissions and tools list allow none of its provider's tools"
	switch {
	case conn.Permissions != nil && len(conn.Permissions) == 0:
		reason = "its permissions are empty ('permissions: []')"
	case conn.Tools != nil && len(conn.Tools) == 0:
		reason = "its tools list is empty ('tools: []')"
	}
	return fmt.Sprintf("connection %q offers no tool: %s, so no agent can use it", name, reason)
}

// SecretRoleDescription returns the provider-authored help for a role.
func (c *Config) SecretRoleDescription(role string) string {
	for _, metadata := range c.providerCatalog().ProviderMetadataAll() {
		for _, candidate := range metadata.SecretRoles {
			if candidate.Name == role {
				return candidate.Description
			}
		}
	}
	return ""
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
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("must use scheme http or https, got %q", u.Scheme)
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
