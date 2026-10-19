package config

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
)

// idPattern is the form of a stable id: 16 random bytes in lower-case hex, like a vault entry id.
var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Only an unusable OS random source fails here; nothing safe is left to do then.
		panic("config: cannot read random bytes: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// validateIDs reports a malformed id and ids that appear twice within one type. A duplicate names both
// keys and is never repaired: choosing which entry keeps the id would be a guess.
func (c *Config) validateIDs(report func(string, ...any)) {
	check := func(section string, ids map[string]string) {
		first := map[string]string{}
		for _, name := range sortedKeys(ids) {
			id := ids[name]
			if id == "" {
				continue
			}
			if !idPattern.MatchString(id) {
				report("%s.%s.id: must be 32 lower-case hex digits", section, name)
				continue
			}
			if other, dup := first[id]; dup {
				report("%s.%s.id: duplicate of %s.%s.id", section, name, section, other)
				continue
			}
			first[id] = name
		}
	}
	services := map[string]string{}
	for name, s := range c.Services {
		services[name] = s.ID
	}
	credentials := map[string]string{}
	for name, cr := range c.Credentials {
		credentials[name] = cr.ID
	}
	connections := map[string]string{}
	for name, cn := range c.Connections {
		connections[name] = cn.ID
	}
	check("services", services)
	check("credentials", credentials)
	check("connections", connections)
}

// assignIDs gives every entry without an id a fresh one. Existing ids are never touched.
func (c *Config) assignIDs() {
	for name, s := range c.Services {
		if s.ID == "" {
			s.ID = newID()
			c.Services[name] = s
		}
	}
	for name, cr := range c.Credentials {
		if cr.ID == "" {
			cr.ID = newID()
			c.Credentials[name] = cr
		}
	}
	for name, cn := range c.Connections {
		if cn.ID == "" {
			cn.ID = newID()
			c.Connections[name] = cn
		}
	}
}

// withoutIDs returns a copy for the schema-1 file, which has no id key.
func (c *Config) withoutIDs() *Config {
	out := c.Clone()
	for name, s := range out.Services {
		s.ID = ""
		out.Services[name] = s
	}
	for name, cr := range out.Credentials {
		cr.ID = ""
		out.Credentials[name] = cr
	}
	for name, cn := range out.Connections {
		cn.ID = ""
		out.Connections[name] = cn
	}
	return out
}
