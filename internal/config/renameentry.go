package config

import (
	"fmt"
	"slices"
	"sort"
)

// TakenError reports a new name that another entry of the same kind already has.
type TakenError struct {
	Kind string
	Name string
}

func (e *TakenError) Error() string {
	return fmt.Sprintf("%s %q exists already", e.Kind, e.Name)
}

// RenameService gives the service old the name name, and every connection that uses it the new name, in
// one step. The entry keeps its id. It returns the references it rewrote, as configuration paths, sorted.
func (c *Config) RenameService(old, name string) ([]string, error) {
	if err := checkRename("service", c.Services, old, name); err != nil {
		return nil, err
	}
	c.Services[name] = c.Services[old]
	delete(c.Services, old)
	var refs []string
	for conn, entry := range c.Connections {
		if entry.Service == old {
			entry.Service = name
			c.Connections[conn] = entry
			refs = append(refs, "connections."+conn+".service")
		}
	}
	sort.Strings(refs)
	return refs, nil
}

// RenameConnection gives the connection old the name name, and every default that names it the new name, in
// one step. The entry keeps its id. It returns the references it rewrote, as configuration paths, sorted.
func (c *Config) RenameConnection(old, name string) ([]string, error) {
	if err := checkRename("connection", c.Connections, old, name); err != nil {
		return nil, err
	}
	c.Connections[name] = c.Connections[old]
	delete(c.Connections, old)
	var refs []string
	for domain, conn := range c.Defaults.Connections {
		if conn == old {
			c.Defaults.Connections[domain] = name
			refs = append(refs, "defaults.connections."+domain)
		}
	}
	sort.Strings(refs)
	return refs, nil
}

// RenameCredential gives the credential old the name name, and every connection that reads it or releases it
// as a forward credential the new name, in one step. The entry keeps its id. It returns the references it
// rewrote, as configuration paths, sorted.
func (c *Config) RenameCredential(old, name string) ([]string, error) {
	if err := checkRename("credential", c.Credentials, old, name); err != nil {
		return nil, err
	}
	c.Credentials[name] = c.Credentials[old]
	delete(c.Credentials, old)
	var refs []string
	for conn, entry := range c.Connections {
		changed := false
		if entry.Credential == old {
			entry.Credential = name
			refs = append(refs, "connections."+conn+".credential")
			changed = true
		}
		if slices.Contains(entry.ForwardSecrets, old) {
			// A new list, so a configuration that shares the old one with this one keeps it.
			forward := make([]string, len(entry.ForwardSecrets))
			for i, ref := range entry.ForwardSecrets {
				if ref == old {
					ref = name
				}
				forward[i] = ref
			}
			entry.ForwardSecrets = forward
			refs = append(refs, "connections."+conn+".forward_secrets")
			changed = true
		}
		if changed {
			c.Connections[conn] = entry
		}
	}
	sort.Strings(refs)
	return refs, nil
}

// checkRename reports why old cannot be renamed to name among entries: old is missing, name is no valid name,
// is old itself, or is taken.
func checkRename[V any](kind string, entries map[string]V, old, name string) error {
	if _, ok := entries[old]; !ok {
		return &NotThereError{Kind: kind, Name: old}
	}
	if err := validateName(name); err != nil {
		return fmt.Errorf("the new %s name %v", kind, err)
	}
	if name == old {
		return fmt.Errorf("%s %q already has this name", kind, old)
	}
	if _, taken := entries[name]; taken {
		return &TakenError{Kind: kind, Name: name}
	}
	return nil
}
