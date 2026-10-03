package config

import (
	"bytes"
	"sort"

	yaml "go.yaml.in/yaml/v3"
)

// ConnectionChange kinds.
const (
	ConnectionCreated = "create"
	ConnectionChanged = "update"
	ConnectionDeleted = "delete"
)

// ConnectionChange names one connection whose configuration entry differs between two configurations and
// how: ConnectionCreated, ConnectionChanged, or ConnectionDeleted.
type ConnectionChange struct {
	Name string
	Kind string
}

// ChangedConnections lists the connections whose entry in after differs from the one in before, sorted by
// name. Only the connection entry itself counts: a change of a service, a credential, or a default that
// leaves every connection entry as it was lists nothing. A nil configuration holds no connection.
func ChangedConnections(before, after *Config) []ConnectionChange {
	var oldConns, newConns map[string]Connection
	if before != nil {
		oldConns = before.Connections
	}
	if after != nil {
		newConns = after.Connections
	}
	var changes []ConnectionChange
	for name, conn := range newConns {
		old, existed := oldConns[name]
		switch {
		case !existed:
			changes = append(changes, ConnectionChange{Name: name, Kind: ConnectionCreated})
		case !sameConnection(old, conn):
			changes = append(changes, ConnectionChange{Name: name, Kind: ConnectionChanged})
		}
	}
	for name := range oldConns {
		if _, ok := newConns[name]; !ok {
			changes = append(changes, ConnectionChange{Name: name, Kind: ConnectionDeleted})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Name < changes[j].Name })
	return changes
}

// sameConnection compares two entries the way the file does: by their encoding, which keeps a missing list
// and an explicitly empty one apart (see Connection.MarshalYAML) and ignores a nil list against an empty
// one wherever the file does.
func sameConnection(a, b Connection) bool {
	ea, errA := yaml.Marshal(a)
	eb, errB := yaml.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return bytes.Equal(ea, eb)
}
