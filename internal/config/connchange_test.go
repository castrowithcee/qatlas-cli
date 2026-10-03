package config

import (
	"reflect"
	"testing"
)

func TestChangedConnections(t *testing.T) {
	base := func() *Config {
		return &Config{
			Credentials: map[string]Credential{"reader": {Type: CredentialTypeEnv}},
			Connections: map[string]Connection{
				"a": {Service: "wiki", Credential: "reader", Tools: []string{"x.read"}},
				"b": {Service: "wiki", Credential: "reader", Permissions: []Permission{}},
				"c": {Service: "wiki", Credential: "reader"},
			},
		}
	}
	tests := []struct {
		name   string
		change func(*Config)
		want   []ConnectionChange
	}{
		{"nothing", func(*Config) {}, nil},
		{"credential and default only", func(c *Config) {
			c.Credentials["reader"] = Credential{Type: CredentialTypeKeyring}
			c.Defaults.Connections = map[string]string{"book": "a"}
		}, nil},
		{"create", func(c *Config) { c.Connections["d"] = Connection{Service: "wiki", Credential: "reader"} },
			[]ConnectionChange{{"d", ConnectionCreated}}},
		{"delete", func(c *Config) { delete(c.Connections, "c") }, []ConnectionChange{{"c", ConnectionDeleted}}},
		{"change a field", func(c *Config) {
			conn := c.Connections["a"]
			conn.Description = "x"
			c.Connections["a"] = conn
		}, []ConnectionChange{{"a", ConnectionChanged}}},
		{"change the tool list", func(c *Config) {
			conn := c.Connections["a"]
			conn.Tools = []string{"x.read", "x.write"}
			c.Connections["a"] = conn
		}, []ConnectionChange{{"a", ConnectionChanged}}},
		{"missing permissions become explicitly empty", func(c *Config) {
			conn := c.Connections["c"]
			conn.Permissions = []Permission{}
			c.Connections["c"] = conn
		}, []ConnectionChange{{"c", ConnectionChanged}}},
		{"several, sorted", func(c *Config) {
			delete(c.Connections, "b")
			c.Connections["0"] = Connection{Service: "wiki", Credential: "reader"}
			conn := c.Connections["c"]
			conn.Target = "t"
			c.Connections["c"] = conn
		}, []ConnectionChange{{"0", ConnectionCreated}, {"b", ConnectionDeleted}, {"c", ConnectionChanged}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			after := base()
			tt.change(after)
			if got := ChangedConnections(base(), after); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ChangedConnections() = %v, want %v", got, tt.want)
			}
		})
	}
	if got := ChangedConnections(nil, base()); len(got) != 3 {
		t.Errorf("from nothing: %v, want three creations", got)
	}
}

// A clone, which the editors save, is no change.
func TestChangedConnectionsIgnoresAClone(t *testing.T) {
	cfg := &Config{Connections: map[string]Connection{
		"a": {Service: "wiki", Credential: "reader", Permissions: []Permission{}, Tools: []string{}},
		"b": {Service: "wiki", Credential: "reader"},
	}}
	if got := ChangedConnections(cfg, cfg.Clone()); got != nil {
		t.Errorf("ChangedConnections() = %v, want none", got)
	}
}
