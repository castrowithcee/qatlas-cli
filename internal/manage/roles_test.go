package manage

import (
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

type roleCatalog struct{}

var roleProviders = []config.ProviderMetadata{
	{ID: "wiki", SecretRoles: []config.SecretRole{{Name: "token-id"}, {Name: "token-secret"}}},
	{ID: "mail", SecretRoles: []config.SecretRole{{Name: "api-key"}}},
}

func (roleCatalog) ProviderMetadata(id string) (config.ProviderMetadata, bool) {
	for _, p := range roleProviders {
		if p.ID == id {
			return p, true
		}
	}
	return config.ProviderMetadata{}, false
}

func (roleCatalog) ProviderMetadataAll() []config.ProviderMetadata { return roleProviders }

func roleConfig() *config.Config {
	cfg := config.New(roleCatalog{})
	cfg.Services = map[string]config.Service{
		"wiki-svc": {Provider: "wiki"},
		"mail-svc": {Provider: "mail"},
	}
	cfg.Connections = map[string]config.Connection{
		"c1": {Service: "wiki-svc", Credential: "derived"},
		"c2": {Service: "wiki-svc", Credential: "ambiguous"},
		"c3": {Service: "mail-svc", Credential: "ambiguous"},
		"c4": {Service: "mail-svc", Credential: "contradicting"},
	}
	return cfg
}

var (
	wikiRoles = []string{"token-id", "token-secret"}
	allRoles  = []string{"api-key", "token-id", "token-secret"}
)

func TestOfferedRoles(t *testing.T) {
	tests := []struct {
		name string
		cred string
		with config.Credential
		want []string
	}{
		{"the field", "any", config.Credential{Provider: "wiki"}, wikiRoles},
		{"derived from connections", "derived", config.Credential{}, wikiRoles},
		{"no field, no connection", "unused", config.Credential{}, allRoles},
		{"connections disagree", "ambiguous", config.Credential{}, allRoles},
		{"field wins over connections", "contradicting", config.Credential{Provider: "wiki"}, wikiRoles},
		{"unsaved credential derives nothing", "", config.Credential{}, allRoles},
		{"unknown provider", "x", config.Credential{Provider: "nope"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OfferedRoles(roleConfig(), tt.cred, tt.with); !slices.Equal(got, tt.want) {
				t.Errorf("OfferedRoles() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAcceptedRoles(t *testing.T) {
	tests := []struct {
		name string
		cred config.Credential
		want []string
	}{
		{"the field", config.Credential{Provider: "wiki"}, wikiRoles},
		{"no field", config.Credential{}, allRoles},
		{"unknown provider", config.Credential{Provider: "nope"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AcceptedRoles(roleConfig(), tt.cred); !slices.Equal(got, tt.want) {
				t.Errorf("AcceptedRoles() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Connections that lead to another provider than the field never change what is accepted.
func TestAcceptedRolesIgnoresConnections(t *testing.T) {
	cfg := roleConfig()
	cred := config.Credential{Provider: "wiki"}
	if got := AcceptedRoles(cfg, cred); !slices.Equal(got, wikiRoles) {
		t.Errorf("field wiki, connections mail: AcceptedRoles() = %v, want %v", got, wikiRoles)
	}
	if slices.Contains(AcceptedRoles(cfg, cred), "api-key") {
		t.Error("a role of the provider the connections lead to was accepted")
	}
	// Without the field, a derived provider does not narrow acceptance either.
	if got := AcceptedRoles(cfg, config.Credential{}); !slices.Equal(got, allRoles) {
		t.Errorf("no field: AcceptedRoles() = %v, want %v", got, allRoles)
	}
}

func TestRoleStateKeyringVocabulary(t *testing.T) {
	stage := func(state secret.StoreState) []string {
		return []string{string(secret.SourceStore) + " (" + string(state) + ")"}
	}
	tests := []struct {
		name    string
		source  secret.Source
		checked []string
		want    string
	}{
		{"stored", secret.SourceStore, nil, StateStored},
		{"environment", secret.SourceEnv, nil, StateOverride},
		{"plaintext", secret.SourcePlaintext, nil, StatePlaintext},
		{"empty", secret.SourceMissing, stage(secret.StoreEmpty), StateEmpty},
		{"locked", secret.SourceMissing, stage(secret.StoreLocked), StateKeyringLocked},
		{"unavailable", secret.SourceMissing, stage(secret.StoreUnavailable), StateUnreachable},
		{"timed out", secret.SourceMissing, stage(secret.StoreTimedOut), StateUnreachable},
		{"off", secret.SourceMissing, stage(secret.StoreOff), StateOff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := RoleStateInput{Type: config.CredentialTypeKeyring, Source: tt.source, Checked: tt.checked}
			if got := RoleState(in); got != tt.want {
				t.Errorf("RoleState() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRoleStateWithoutVault(t *testing.T) {
	for _, processUnlocked := range []bool{false, true} {
		in := RoleStateInput{Type: config.CredentialTypeVault, Credential: "c", Role: "r", ProcessUnlocked: processUnlocked}
		if got := RoleState(in); got != StateNoVault {
			t.Errorf("RoleState() = %q, want %q", got, StateNoVault)
		}
	}
}

func TestRoleStateVaultVocabulary(t *testing.T) {
	dir := t.TempDir()
	state := func(v *vault.Vault, processUnlocked bool) string {
		return RoleState(RoleStateInput{
			Type: config.CredentialTypeVault, Credential: "c", Role: "r", Vault: v, ProcessUnlocked: processUnlocked,
		})
	}

	if got := state(vault.New(dir), false); got != StateEmpty {
		t.Errorf("absent vault: %q, want %q", got, StateEmpty)
	}

	v := vault.New(dir)
	if err := v.Set("c", "other", "seed-value", nil); err != nil {
		t.Fatal(err)
	}
	if got := state(v, false); got != StateEmpty {
		t.Errorf("unencrypted vault without the role: %q, want %q", got, StateEmpty)
	}
	if err := v.Set("c", "r", "stored-value", nil); err != nil {
		t.Fatal(err)
	}
	if got := state(v, false); got != StateStoredVault {
		t.Errorf("unencrypted vault with the role: %q, want %q", got, StateStoredVault)
	}

	if err := v.Encrypt("passphrase"); err != nil {
		t.Fatal(err)
	}
	locked := vault.New(dir)
	if got := state(locked, false); got != StateVaultLocked {
		t.Errorf("locked vault: %q, want %q", got, StateVaultLocked)
	}
	if got := state(locked, true); got != StateVaultElsewhere {
		t.Errorf("locked vault held open elsewhere: %q, want %q", got, StateVaultElsewhere)
	}
	for _, got := range []string{state(locked, false), state(locked, true)} {
		if strings.Contains(got, "stored-value") {
			t.Errorf("state %q carries a secret value", got)
		}
	}
}
