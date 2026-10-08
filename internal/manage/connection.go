package manage

import (
	"sort"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// ProviderServices are the services of one provider, in ascending name order.
func ProviderServices(cfg *config.Config, provider string) []string {
	var names []string
	for name, svc := range cfg.Services {
		if svc.Provider == provider {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// ProviderCredentials are the credentials a connection of one provider may use, in ascending name order:
// those that belong to it for display (see CredentialProvider), and those whose provider is still open. A
// credential that belongs to another provider is never offered, and neither is a payload credential, which
// serves no connection. A credential just created has no connection yet, so nothing places it anywhere;
// keeping it selectable is what lets the connection form settle it.
func ProviderCredentials(cfg *config.Config, provider string) []string {
	var names []string
	for name, cred := range cfg.Credentials {
		if cred.Forward {
			continue
		}
		switch CredentialProvider(cfg, name, cred) {
		case provider, "":
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// SplitTargets is how a target list is stored: one entry as target, several as targets, and none as
// neither, so a file that names one target reads as it always did.
func SplitTargets(values []string) (target string, targets []string) {
	switch len(values) {
	case 0:
		return "", nil
	case 1:
		return values[0], nil
	}
	return "", append([]string(nil), values...)
}

// DefaultPermissions are what a connection without a permissions list allows: the provider's declared
// defaults, or read alone. Changes, execution and administration are never among the fallback.
func DefaultPermissions(metadata config.ProviderMetadata) []config.Permission {
	if len(metadata.DefaultPermissions) > 0 {
		return metadata.DefaultPermissions
	}
	return []config.Permission{config.PermissionRead}
}

// ConnectionStart is where the setup of a new connection of one provider begins. The values are only a
// visible preselection; every one of them stays free to change before saving.
type ConnectionStart struct {
	// Name is the provider's own name while no connection has it yet, else empty.
	Name string
	// BaseURL is the provider's default base URL for a new service.
	BaseURL string
	// Storage is the configuration's default place for a new credential.
	Storage string
	// ProfileID names the provider's recommended profile; empty when it declares none.
	ProfileID string
	// Permissions and Tools are what that profile ticks. Tools is an explicit selection: a tool the
	// profile does not list is never enabled. Both are empty without a profile.
	Permissions []config.Permission
	Tools       []string
}

// ConnectionDefaults are the starting values of a new connection of one provider.
func ConnectionDefaults(cfg *config.Config, provider string) ConnectionStart {
	metadata, _ := cfg.ProviderMetadata(provider)
	start := ConnectionStart{BaseURL: metadata.DefaultBaseURL, Storage: cfg.SecretStore()}
	if _, taken := cfg.Connections[provider]; !taken {
		start.Name = provider
	}
	if profile, ok := metadata.RecommendedProfile(); ok {
		start.ProfileID = profile.ID
		start.Permissions = metadata.ProfilePermissions(profile)
		start.Tools = append([]string(nil), profile.Tools...)
	}
	return start
}
