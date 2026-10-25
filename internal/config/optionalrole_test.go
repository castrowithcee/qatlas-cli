package config

import (
	"slices"
	"testing"
)

// optionalProviders has one provider, "site", with a required role "token" and an optional role "password".
var optionalProviders ProviderCatalog = testProviderCatalog{
	"site": {
		ID: "site", Name: "Site",
		SecretRoles: []SecretRole{
			{Name: "token", Description: "site token"},
			{Name: "password", Description: "site password", Optional: true},
		},
		Target: TargetMetadata{Label: "target"},
		Tools:  []ToolMetadata{{ID: "site.items.get", Effect: EffectRead}},
	},
}

func optionalConfig(cred Credential) *Config {
	cfg := New(optionalProviders)
	cfg.Services = map[string]Service{"site": {Provider: "site", BaseURL: "https://site.example.invalid"}}
	cfg.Credentials = map[string]Credential{"key": cred}
	cfg.Connections = map[string]Connection{"site": {Service: "site", Credential: "key"}}
	return cfg
}

func codesOf(err error) []string {
	var codes []string
	for _, p := range Problems(err) {
		codes = append(codes, p.Code)
	}
	return codes
}

func TestOptionalSecretRoleValidation(t *testing.T) {
	tests := []struct {
		name string
		cred Credential
		want string
	}{
		{"env without the optional role", Credential{Provider: "site", Type: CredentialTypeEnv,
			Values: map[string]string{"token": "SITE_TOKEN"}}, ""},
		{"env with the optional role", Credential{Provider: "site", Type: CredentialTypeEnv,
			Values: map[string]string{"token": "SITE_TOKEN", "password": "SITE_PASS"}}, ""},
		{"env without a provider and without the optional role", Credential{Type: CredentialTypeEnv,
			Values: map[string]string{"token": "SITE_TOKEN"}}, ""},
		{"keyring", Credential{Provider: "site", Type: CredentialTypeKeyring}, ""},
		{"vault", Credential{Provider: "site", Type: CredentialTypeVault}, ""},
		{"env without the required role", Credential{Provider: "site", Type: CredentialTypeEnv,
			Values: map[string]string{"password": "SITE_PASS"}}, CodeSecretRoleMissing},
		{"env with an empty optional variable name", Credential{Provider: "site", Type: CredentialTypeEnv,
			Values: map[string]string{"token": "SITE_TOKEN", "password": ""}}, CodeInvalidEnvName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := optionalConfig(tt.cred).Validate()
			codes := codesOf(err)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want valid", err)
				}
				return
			}
			if !slices.Contains(codes, tt.want) {
				t.Fatalf("codes = %v, want %s", codes, tt.want)
			}
			if slices.Contains(codes, CodeUnknownSecretRole) {
				t.Errorf("an optional role must be a known role, codes = %v", codes)
			}
		})
	}
}

func TestOptionalSecretRoleLists(t *testing.T) {
	cfg := New(optionalProviders)
	if got := cfg.ProviderSecretRoles("site"); !slices.Equal(got, []string{"token", "password"}) {
		t.Errorf("ProviderSecretRoles = %v", got)
	}
	if got := cfg.RequiredSecretRoles("site"); !slices.Equal(got, []string{"token"}) {
		t.Errorf("RequiredSecretRoles = %v", got)
	}
	if got := cfg.SecretRolesOf("site"); !slices.Equal(got, []string{"password", "token"}) {
		t.Errorf("SecretRolesOf = %v", got)
	}
	for _, tt := range []struct {
		provider, role string
		want           bool
	}{{"site", "password", true}, {"site", "token", false}, {"site", "nope", false}, {"nope", "password", false}} {
		if got := cfg.SecretRoleOptional(tt.provider, tt.role); got != tt.want {
			t.Errorf("SecretRoleOptional(%q, %q) = %v, want %v", tt.provider, tt.role, got, tt.want)
		}
	}
	// Every role of a provider without optional roles stays required.
	if got := New(testProviders).RequiredSecretRoles("bookstack"); !slices.Equal(got, []string{"token-id", "token-secret"}) {
		t.Errorf("RequiredSecretRoles(bookstack) = %v", got)
	}
}
