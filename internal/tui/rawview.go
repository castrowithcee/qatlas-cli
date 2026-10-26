package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
	yaml "go.yaml.in/yaml/v3"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// secretMask stands where a value that may be a secret would be.
const secretMask = config.SecretMask

// envName is what a variable name looks like; anything else in a credential's values is masked.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// maskOption is the value of a service option, masked where its key names a secret.
func maskOption(key, value string) string {
	if config.SecretKeyName(key) {
		return secretMask
	}
	return config.MaskURL(value)
}

// maskValues copies the values of a credential with every one masked that is not the name of an
// environment variable. An env credential names variables, which are no secrets; any other type holds none.
func maskValues(typ string, values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for role, v := range values {
		if typ == config.CredentialTypeEnv && envName.MatchString(v) {
			out[role] = v
		} else {
			out[role] = secretMask
		}
	}
	return out
}

// rawLines is the configuration entry name as YAML, one line each, with no secret value in it and no control
// character from any text of the configuration.
func (m *Model) rawLines(name string) []string {
	var doc any
	switch m.section {
	case sectionServices:
		s := m.cfg.Services[name]
		s.BaseURL = config.MaskURL(s.BaseURL)
		options := make(map[string]string, len(s.Options))
		for k, v := range s.Options {
			options[k] = maskOption(k, v)
		}
		if len(options) > 0 {
			s.Options = options
		}
		doc = map[string]config.Service{name: s}
	case sectionCredentials:
		c := m.cfg.Credentials[name]
		c.Values = maskValues(c.Type, c.Values)
		doc = map[string]config.Credential{name: c}
	case sectionConnections:
		doc = map[string]config.Connection{name: m.cfg.Connections[name]}
	default:
		return nil
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		lines = append(lines, m.logText(ansi.Strip(line)))
	}
	return lines
}
