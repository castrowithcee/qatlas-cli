package manage

import (
	"cmp"
	"errors"
	"slices"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	yaml "go.yaml.in/yaml/v3"
)

// ExportSelection says which parts of the configuration an export takes. All takes every service,
// credential and connection and excludes the name lists. Otherwise the named entries are taken together
// with what they need (see ExportResult.Dependencies). Defaults and ProviderNotes are taken only when asked
// for; All does not imply them. Agent tokens, approvals, the invocation log, vault settings and every secret
// are never part of an export.
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
}

// ExportConfig selects the parts of the current configuration sel names and, when path is not empty, writes
// them as a YAML file (see TransferFormat) that only this user can read. Nothing is written for an empty
// path, which gives the preview of the same result. An existing file at path is never replaced.
//
// An unknown name is an error that names every one. A default that points at a connection outside the
// selection is left out of the file and named in DefaultsOmitted, so the file always resolves by itself.
func (s *Service) ExportConfig(sel ExportSelection, path string) (*ExportResult, error) {
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
	data, err := yaml.Marshal(&f)
	if err != nil {
		return nil, err
	}
	if err := config.WriteNewPrivateFile(path, data); err != nil {
		return nil, err
	}
	res.Path = path
	return res, nil
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
