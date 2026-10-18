package config

import "fmt"

// Renaming records one former provider or tool ID that loading read as its current name.
type Renaming struct {
	// Section is the configuration section holding the entry: services, credentials, connections,
	// defaults.connections or provider_notes.
	Section string
	// Name is the entry in that section: a service, credential or connection name, a domain, or a provider.
	Name string
	// Kind is RenamedProvider or RenamedTool.
	Kind     string
	Old, New string
}

// The two kinds of Renaming.
const (
	RenamedProvider = "provider"
	RenamedTool     = "tool"
)

// Renamings returns every former name that loading mapped to its current one, in file section order. The
// list belongs to the loaded model: a clone, or a model that was edited and saved, no longer carries it.
func (c *Config) Renamings() []Renaming { return append([]Renaming(nil), c.renamings...) }

// ConnectionRenamings returns the renamings that change what the named connection selects: the provider
// of its service and the entries of its tools list.
func (c *Config) ConnectionRenamings(name string) []Renaming {
	conn, ok := c.Connections[name]
	if !ok {
		return nil
	}
	var out []Renaming
	for _, r := range c.renamings {
		switch {
		case r.Section == "services" && r.Name == conn.Service,
			r.Section == "connections" && r.Name == name:
			out = append(out, r)
		}
	}
	return out
}

// applyRenames rewrites former provider and tool IDs to the current ones before validation, so the rest
// of the model only knows current names. It is local: the file is not touched, and saving serializes
// the rewritten model. A tools list keeps an old and a new entry side by side, so the duplicate check
// still reports them. A mapped key that meets an existing one is an error rather than a silent merge.
func (c *Config) applyRenames() error {
	catalog := c.providerCatalog()
	provider := func(id string) (string, bool) {
		if _, known := catalog.ProviderMetadata(id); known {
			return "", false
		}
		return CurrentProviderID(catalog, id)
	}
	record := func(section, name, kind, old, current string) {
		c.renamings = append(c.renamings, Renaming{Section: section, Name: name, Kind: kind, Old: old, New: current})
	}

	for _, name := range sortedKeys(c.Services) {
		s := c.Services[name]
		if current, ok := provider(s.Provider); ok {
			record("services", name, RenamedProvider, s.Provider, current)
			s.Provider = current
			c.Services[name] = s
		}
	}
	for _, name := range sortedKeys(c.Credentials) {
		cred := c.Credentials[name]
		if current, ok := provider(cred.Provider); ok {
			record("credentials", name, RenamedProvider, cred.Provider, current)
			cred.Provider = current
			c.Credentials[name] = cred
		}
	}
	for _, name := range sortedKeys(c.Connections) {
		conn := c.Connections[name]
		metadata, known := catalog.ProviderMetadata(c.Services[conn.Service].Provider)
		if !known || conn.Tools == nil || len(metadata.RenamedTools) == 0 {
			continue
		}
		tools := make([]string, len(conn.Tools))
		for i, tool := range conn.Tools {
			tools[i] = tool
			if current, ok := metadata.RenamedTools[tool]; ok {
				record("connections", name, RenamedTool, tool, current)
				tools[i] = current
			}
		}
		conn.Tools = tools
		c.Connections[name] = conn
	}

	domains := map[string]string{}
	for _, domain := range sortedKeys(c.Defaults.Connections) {
		key := domain
		if current, ok := provider(domain); ok {
			record("defaults.connections", domain, RenamedProvider, domain, current)
			key = current
		} else if current, ok := CurrentToolID(catalog, domain); ok {
			record("defaults.connections", domain, RenamedTool, domain, current)
			key = current
		}
		if _, taken := domains[key]; taken {
			return fmt.Errorf("defaults.connections.%s: the renamed key collides with an existing entry", key)
		}
		domains[key] = c.Defaults.Connections[domain]
	}
	if c.Defaults.Connections != nil {
		c.Defaults.Connections = domains
	}

	notes := map[string]string{}
	for _, name := range sortedKeys(c.ProviderNotes) {
		key := name
		if current, ok := provider(name); ok {
			record("provider_notes", name, RenamedProvider, name, current)
			key = current
		}
		if _, taken := notes[key]; taken {
			return fmt.Errorf("provider_notes.%s: the renamed key collides with an existing entry", key)
		}
		notes[key] = c.ProviderNotes[name]
	}
	if c.ProviderNotes != nil {
		c.ProviderNotes = notes
	}
	return nil
}

// warning words each renaming as one line, naming the entry and that saving writes the current name.
func (r Renaming) warning() string {
	where := r.Section + "." + r.Name
	switch r.Section {
	case "services", "credentials":
		where += ".provider"
	case "connections":
		where += ".tools"
	}
	return fmt.Sprintf("%s: %s %q was renamed to %q; it is read as the new name and saving writes it",
		where, r.Kind, r.Old, r.New)
}
