package config

import (
	"reflect"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

// withForward is minimal plus the extra credentials and the extra lines of the connection "wiki".
func withForward(credentials, connection string) string {
	base := strings.Replace(minimal, "connections:\n", credentials+"connections:\n", 1)
	return strings.Replace(base, "    credential: reader\n", "    credential: reader\n"+connection, 1)
}

func TestForwardCredentialValidation(t *testing.T) {
	const good = "  shared:\n    type: vault\n    forward: true\n    fields: [user, pass]\n    description: shared login\n"
	tests := []struct {
		name, in, want string
	}{
		{"keyring source", withForward(strings.Replace(good, "vault", "keyring", 1), "    forward_secrets: [shared]\n"), ""},
		{"vault source", withForward(good, "    forward_secrets: [shared]\n"), ""},
		{"no forward_secrets at all", withForward(good, ""), ""},
		{"env source", withForward("  shared:\n    type: env\n    forward: true\n    fields: [a]\n    values: {a: X}\n", ""),
			"credentials.shared.type"},
		{"no fields", withForward("  shared:\n    type: vault\n    forward: true\n", ""),
			"credentials.shared.fields: a forward credential needs at least one field"},
		{"a provider", withForward(strings.Replace(good, "    forward: true\n", "    forward: true\n    provider: bookstack\n", 1), ""),
			"credentials.shared.provider"},
		{"a bad field name", withForward("  shared:\n    type: vault\n    forward: true\n    fields: ['a b']\n", ""),
			"credentials.shared.fields[0]"},
		{"a duplicate field", withForward("  shared:\n    type: vault\n    forward: true\n    fields: [a, a]\n", ""),
			"listed more than once"},
		{"fields without forward", withForward("  plain:\n    type: vault\n    fields: [a]\n", ""),
			"only a forward credential has fields"},
		{"a description without forward", withForward("  plain:\n    type: vault\n    description: x\n", ""),
			"only a forward credential has a description"},
		{"a value in a forward credential", withForward(good+"    values: {user: hunter2hunter2}\n", ""),
			"credentials.shared.values"},
		{"as the credential of a connection",
			strings.Replace(withForward(good, ""), "    credential: reader\n", "    credential: shared\n", 1),
			"cannot serve a connection"},
		{"an unknown forward secret", withForward(good, "    forward_secrets: [nope]\n"),
			"connections.wiki.forward_secrets[0]: unknown credential"},
		{"a non-forward forward secret", withForward(good, "    forward_secrets: [reader]\n"),
			`credential "reader" is not a forward credential`},
		{"a duplicate forward secret", withForward(good, "    forward_secrets: [shared, shared]\n"),
			"listed more than once"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(tt.in), testProviders)
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("Decode() = %v, want valid", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("Decode() = %v, want it to contain %q", err, tt.want)
			}
			if err != nil && strings.Contains(err.Error(), "hunter2") {
				t.Errorf("the error quotes a configured value: %v", err)
			}
		})
	}
}

func TestForwardSettingsRoundTripAndResolve(t *testing.T) {
	in := withForward("  shared:\n    type: keyring\n    forward: true\n    fields: [user, pass]\n    description: shared login\n",
		"    forward_secrets: [shared]\n")
	cfg, err := Decode(strings.NewReader(in), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Decode(strings.NewReader(string(data)), testProviders)
	if err != nil {
		t.Fatalf("the saved form does not load: %v\n%s", err, data)
	}
	if !reflect.DeepEqual(cfg.Connections, again.Connections) || !reflect.DeepEqual(cfg.Credentials, again.Credentials) {
		t.Errorf("the settings did not survive a round trip")
	}
	if !reflect.DeepEqual(cfg.Clone().Connections["wiki"].ForwardSecrets, []string{"shared"}) ||
		!reflect.DeepEqual(cfg.Clone().Credentials["shared"].Fields, []string{"user", "pass"}) {
		t.Errorf("Clone() lost the forward settings")
	}
	resolved, err := cfg.Resolve("wiki", "knowledge")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resolved.ForwardSecrets, []string{"shared"}) || len(resolved.Forward) != 1 ||
		resolved.Forward["shared"].Description != "shared login" {
		t.Errorf("Resolve() = %+v", resolved)
	}
}

func TestExistingConfigsHaveNoForwardSettings(t *testing.T) {
	cfg, err := Decode(strings.NewReader(minimal), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := yaml.Marshal(cfg)
	if strings.Contains(string(data), "forward") || strings.Contains(string(data), "fields") {
		t.Errorf("a configuration without forward settings gained some:\n%s", data)
	}
	resolved, _ := cfg.Resolve("wiki", "knowledge")
	if resolved.ForwardSecrets != nil || resolved.Forward != nil {
		t.Errorf("Resolve() = %+v, want no forward settings", resolved)
	}
}

func TestCheckForwardValue(t *testing.T) {
	cred := Credential{Type: CredentialTypeVault, Forward: true, Fields: []string{"user"}}
	for _, tt := range []struct {
		field, value string
		ok           bool
	}{
		{"user", "abcd", true}, {"user", "abc", false}, {"user", "", false}, {"other", "abcdef", false},
	} {
		err := cred.CheckForwardValue(tt.field, tt.value)
		if (err == nil) != tt.ok {
			t.Errorf("CheckForwardValue(%q, %q) = %v, want ok=%t", tt.field, tt.value, err, tt.ok)
		}
		if err != nil && tt.value != "" && strings.Contains(err.Error(), tt.value) && tt.value != tt.field {
			t.Errorf("the error carries the value: %v", err)
		}
	}
	if err := (Credential{Type: CredentialTypeKeyring}).CheckForwardValue("any", "x"); err != nil {
		t.Errorf("a plain credential refused a short value: %v", err)
	}
}
