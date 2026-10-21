package manage

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	yaml "go.yaml.in/yaml/v3"
)

// ExportSelection says which parts of the configuration an export takes. All takes every service,
// credential and connection and excludes the name lists. Otherwise the named entries are taken together
// with what they need (see ExportResult.Dependencies). Defaults and ProviderNotes are taken only when asked
// for; All does not imply them. Agent tokens, approvals, the invocation log and vault settings are never
// part of an export, and secrets only of ExportConfigWithSecrets.
type ExportSelection struct {
	All                                bool
	Services, Credentials, Connections []string
	Defaults, ProviderNotes            bool
}

// ExportDependency is an entry taken only because a selected one needs it.
type ExportDependency struct {
	// Kind is "service" or "credential"; For lists the connections that need the entry.
	Kind, Name string
	For        []string
}

// ExportResult is what an export contains.
type ExportResult struct {
	// Path is the file written, "" for a preview.
	Path                               string
	Services, Credentials, Connections []string
	Dependencies                       []ExportDependency
	// Defaults are the domains of defaults.connections taken. DefaultsOmitted are those left out because
	// their connection is not part of the export.
	Defaults, DefaultsOmitted []string
	ProviderNotes             []string
	// SecretsNotIncluded are the exported credentials whose secrets live in the keyring or the vault and
	// are not part of the file; env credentials name variables only.
	SecretsNotIncluded []string
	// SecretsIncluded names, per credential, the roles whose secrets the file holds (never a value); only
	// ExportConfigWithSecrets fills it. SecretsMissing names what was asked for but is not stored: a
	// credential for which no role holds a value, or "credential.field" for a missing field of a forward
	// credential. Neither stops the export.
	SecretsIncluded []ExportedSecret
	SecretsMissing  []string
	// Encrypted tells whether the file is an age file. Warnings are for the person to read after an export
	// with secrets, such as the one for a file in plain text.
	Encrypted bool
	Warnings  []string
}

// ExportedSecret names the roles of one credential whose secrets an export holds.
type ExportedSecret struct {
	Credential string
	Roles      []string
}

// SecretExport says how ExportConfigWithSecrets protects and reads the secrets. It is a type of the
// service API only: no command line or agent surface builds it.
type SecretExport struct {
	// ExportPassphrase returns the passphrase the file is encrypted with, which a surface has asked for
	// twice. It is not the vault passphrase. It must be set unless Unencrypted is.
	ExportPassphrase vault.PassphraseFunc
	// Unencrypted writes the secrets as plain text. It must be asked for expressly and excludes
	// ExportPassphrase.
	Unencrypted bool
	// VaultPassphrase unlocks an encrypted vault whose secrets are read; asked at most once.
	VaultPassphrase vault.PassphraseFunc
}

// ExportConfig selects the parts of the current configuration sel names and, when path is not empty, writes
// them as a YAML file (see TransferFormat) that only this user can read. Nothing is written for an empty
// path, which gives the preview of the same result. An existing file at path is never replaced.
//
// An unknown name is an error that names every one. A default that points at a connection outside the
// selection is left out of the file and named in DefaultsOmitted, so the file always resolves by itself.
func (s *Service) ExportConfig(sel ExportSelection, path string) (*ExportResult, error) {
	return s.export(sel, path, nil)
}

// ExportConfigWithSecrets is ExportConfig that also puts the secrets of the exported keyring and vault
// credentials into the file, which then has TransferFormatVersionSecrets. It needs a path: there is no
// preview, since reading the secrets may ask for the vault passphrase. The vault is opened here with
// opts.VaultPassphrase, never through the vault process. The file is encrypted with the passphrase of
// opts.ExportPassphrase, or written in plain text if opts.Unencrypted. An empty passphrase and
// neither-or-both of the two modes are refused before any secret is read.
//
// A secret that is not stored is named in ExportResult.SecretsMissing and left out. A keyring or vault that
// cannot be read fails the export instead: that is not an absent secret. No partial file remains.
func (s *Service) ExportConfigWithSecrets(sel ExportSelection, path string, opts SecretExport) (*ExportResult, error) {
	if path == "" {
		return nil, errors.New("an export with secrets needs a file path")
	}
	if opts.Unencrypted == (opts.ExportPassphrase != nil) {
		return nil, errors.New("an export with secrets is either encrypted with an export passphrase or expressly unencrypted")
	}
	return s.export(sel, path, &opts)
}

func (s *Service) export(sel ExportSelection, path string, withSecrets *SecretExport) (*ExportResult, error) {
	cfg, _, err := s.Load()
	if err != nil {
		return nil, err
	}
	part, res, err := selectExport(cfg, sel)
	if err != nil {
		return nil, err
	}
	// What an import would reject must not be written.
	if err := part.Validate(); err != nil {
		return nil, s.refuse(&config.InvalidError{Path: s.store.Path(), Err: err})
	}
	if path == "" {
		return res, nil
	}
	f := transferFile{Format: TransferFormat, FormatVersion: TransferFormatVersion, Services: part.Services,
		Credentials: part.Credentials, Connections: part.Connections, ProviderNotes: part.ProviderNotes}
	if len(part.Defaults.Connections) > 0 || part.Defaults.SecretStore != "" {
		d := part.Defaults
		f.Defaults = &d
	}
	var passphrase string
	if withSecrets != nil && !withSecrets.Unencrypted {
		if passphrase, err = withSecrets.ExportPassphrase(exportPassphrasePrompt); err != nil {
			return nil, err
		}
		if passphrase == "" {
			return nil, vault.ErrEmptyExportPassphrase
		}
	}
	if withSecrets != nil {
		if err := s.addSecrets(part, &f, res, withSecrets.VaultPassphrase); err != nil {
			return nil, err
		}
	}
	data, err := yaml.Marshal(&f)
	if err != nil {
		return nil, err
	}
	if withSecrets != nil {
		if passphrase != "" {
			if data, err = vault.EncryptExport(data, passphrase); err != nil {
				return nil, err
			}
			res.Encrypted = true
		} else if len(f.Secrets) > 0 {
			res.Warnings = append(res.Warnings, "the file holds secrets in plain text: delete it after the import")
		}
	}
	if err := config.WriteNewPrivateFile(path, data); err != nil {
		return nil, err
	}
	res.Path = path
	return res, nil
}

// addSecrets reads the secrets of the keyring and vault credentials in part into f.Secrets and names them in
// res. The vault is opened by the vault passphrase of ask, in this process.
func (s *Service) addSecrets(part *config.Config, f *transferFile, res *ExportResult, ask vault.PassphraseFunc) error {
	secrets := map[string]map[string]string{}
	var notIncluded []string
	for _, name := range res.SecretsNotIncluded {
		cred := part.Credentials[name]
		roles := AcceptedRoles(part, cred)
		if cred.Forward {
			roles = cred.Fields
		}
		found := map[string]string{}
		for _, role := range roles {
			value, ok, err := s.readSecret(name, role, cred.Type, ask)
			if err != nil {
				return fmt.Errorf("reading the secret of %s.%s: %w", name, role, err)
			}
			switch {
			case ok:
				found[role] = value
			case cred.Forward:
				res.SecretsMissing = append(res.SecretsMissing, name+"."+role)
			}
		}
		if len(found) == 0 {
			if !cred.Forward {
				res.SecretsMissing = append(res.SecretsMissing, name)
			}
			notIncluded = append(notIncluded, name)
			continue
		}
		secrets[name] = found
		res.SecretsIncluded = append(res.SecretsIncluded, ExportedSecret{Credential: name, Roles: sortedNames(found)})
	}
	res.SecretsNotIncluded = notIncluded
	if len(secrets) == 0 {
		return nil
	}
	// What an import would reject must not be written.
	if err := checkTransferSecrets(part, secrets); err != nil {
		return s.refuse(err)
	}
	f.FormatVersion = TransferFormatVersionSecrets
	f.Secrets = secrets
	return nil
}

// readSecret reads one role from the vault or the keyring, whichever the credential's type names. A role
// that is not stored is not found; a store that cannot answer is an error without the value.
func (s *Service) readSecret(credential, role, credType string, ask vault.PassphraseFunc) (string, bool, error) {
	if credType == config.CredentialTypeVault {
		v := s.secrets.Vault()
		if v == nil {
			return "", false, errors.New("no vault is configured for this run")
		}
		value, found, _, err := v.Get(credential, role, ask)
		return value, found && value != "", err
	}
	value, state := s.secrets.StoreValue(context.Background(), credential, role)
	switch state {
	case secret.StoreHolds:
		return value, value != "", nil
	case secret.StoreEmpty:
		return "", false, nil
	case secret.StoreOff:
		return "", false, secret.ErrDisabled
	case secret.StoreTimedOut:
		return "", false, secret.ErrTimedOut
	case secret.StoreLocked:
		return "", false, secret.ErrLocked
	}
	return "", false, secret.ErrUnavailable
}

// selectExport builds the part of cfg that sel names.
func selectExport(cfg *config.Config, sel ExportSelection) (*config.Config, *ExportResult, error) {
	if sel.All && (len(sel.Services)+len(sel.Credentials)+len(sel.Connections)) > 0 {
		return nil, nil, errors.New("all and named entries exclude each other")
	}
	if !sel.All && len(sel.Services)+len(sel.Credentials)+len(sel.Connections) == 0 && !sel.Defaults && !sel.ProviderNotes {
		return nil, nil, errors.New("nothing is selected")
	}
	var unknown []error
	check := func(kind string, names []string, has func(string) bool) {
		for _, name := range names {
			if !has(name) {
				unknown = append(unknown, &config.NotThereError{Kind: kind, Name: name})
			}
		}
	}
	check("connection", sel.Connections, func(n string) bool { _, ok := cfg.Connections[n]; return ok })
	check("service", sel.Services, func(n string) bool { _, ok := cfg.Services[n]; return ok })
	check("credential", sel.Credentials, func(n string) bool { _, ok := cfg.Credentials[n]; return ok })
	if len(unknown) > 0 {
		return nil, nil, errors.Join(unknown...)
	}

	services := map[string]bool{}
	credentials := map[string]bool{}
	connections := map[string]bool{}
	if sel.All {
		for name := range cfg.Services {
			services[name] = true
		}
		for name := range cfg.Credentials {
			credentials[name] = true
		}
		for name := range cfg.Connections {
			connections[name] = true
		}
	}
	for _, name := range sel.Services {
		services[name] = true
	}
	for _, name := range sel.Credentials {
		credentials[name] = true
	}
	// A connection pulls in its service, its credential and the forward credentials it releases. What was
	// not selected itself is reported as a dependency.
	type depKey struct{ kind, name string }
	explicit := map[depKey]bool{}
	for name := range services {
		explicit[depKey{"service", name}] = true
	}
	for name := range credentials {
		explicit[depKey{"credential", name}] = true
	}
	for _, name := range sel.Connections {
		connections[name] = true
	}
	deps := map[depKey][]string{}
	for _, name := range sortedNames(connections) {
		conn := cfg.Connections[name]
		need := func(kind, dep string, taken map[string]bool) {
			taken[dep] = true
			if key := (depKey{kind, dep}); !explicit[key] {
				deps[key] = append(deps[key], name)
			}
		}
		need("service", conn.Service, services)
		need("credential", conn.Credential, credentials)
		for _, ref := range conn.ForwardSecrets {
			need("credential", ref, credentials)
		}
	}

	part := config.New(cfg.ProviderCatalog())
	res := &ExportResult{}
	for _, name := range sortedNames(services) {
		part.Services[name] = cfg.Services[name]
		res.Services = append(res.Services, name)
	}
	for _, name := range sortedNames(credentials) {
		cred := cfg.Credentials[name]
		// Only an env credential holds anything in the file, and that is a variable name.
		if cred.Type != config.CredentialTypeEnv {
			cred.Values = nil
			res.SecretsNotIncluded = append(res.SecretsNotIncluded, name)
		}
		part.Credentials[name] = cred
		res.Credentials = append(res.Credentials, name)
	}
	for _, name := range sortedNames(connections) {
		part.Connections[name] = cfg.Connections[name]
		res.Connections = append(res.Connections, name)
	}
	for key, by := range deps {
		res.Dependencies = append(res.Dependencies, ExportDependency{Kind: key.kind, Name: key.name, For: by})
	}
	slices.SortFunc(res.Dependencies, func(a, b ExportDependency) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name))
	})

	if sel.Defaults {
		for _, domain := range sortedNames(cfg.Defaults.Connections) {
			target := cfg.Defaults.Connections[domain]
			if !connections[target] {
				res.DefaultsOmitted = append(res.DefaultsOmitted, domain)
				continue
			}
			part.Defaults.Connections[domain] = target
			res.Defaults = append(res.Defaults, domain)
		}
		part.Defaults.SecretStore = cfg.Defaults.SecretStore
	}
	if sel.ProviderNotes {
		for _, provider := range sortedNames(cfg.ProviderNotes) {
			if part.ProviderNotes == nil {
				part.ProviderNotes = map[string]string{}
			}
			part.ProviderNotes[provider] = cfg.ProviderNotes[provider]
			res.ProviderNotes = append(res.ProviderNotes, provider)
		}
	}
	return part, res, nil
}
