package manage

import (
	"errors"
	"fmt"
	"slices"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// ErrChooseProvider is the error of a setup whose provider is missing or unknown.
var ErrChooseProvider = errors.New("choose a provider first")

// ErrChooseStorage is the error of a new credential that names no place for its secrets.
var ErrChooseStorage = errors.New("choose where the new credential's secrets are kept")

// The kinds of entry a setup names in its errors.
const (
	KindService    = "service"
	KindCredential = "credential"
	KindConnection = "connection"
)

// EmptyNameError reports an entry of a setup without a name.
type EmptyNameError struct{ Kind string }

func (e *EmptyNameError) Error() string { return fmt.Sprintf("a %s name must not be empty", e.Kind) }

// NameTakenError reports a setup that adds an entry under a name the configuration already uses. Kind is
// KindService, KindCredential or KindConnection.
type NameTakenError struct{ Kind, Name string }

func (e *NameTakenError) Error() string {
	if e.Kind == KindConnection {
		return fmt.Sprintf("a connection named %q already exists; choose another name", e.Name)
	}
	return fmt.Sprintf("a %s named %q already exists; choose it instead of adding it again", e.Kind, e.Name)
}

// UnknownEntryError reports a reused service or credential that does not exist or does not serve the
// provider of the setup. Kind is KindService or KindCredential.
type UnknownEntryError struct{ Kind, Name, Provider string }

func (e *UnknownEntryError) Error() string {
	return fmt.Sprintf("unknown %s %q for provider %q", e.Kind, e.Name, e.Provider)
}

// RoleMissingError reports a secret role of a new keyring or vault credential without its secret. It never
// carries a value.
type RoleMissingError struct{ Role string }

func (e *RoleMissingError) Error() string { return fmt.Sprintf("%s is empty; type its secret", e.Role) }

// ConnectionStage says how much of a connection setup BuildConnectionCandidate applies. A guided surface
// checks every step against the core before it opens the next one.
type ConnectionStage int

const (
	// StageService applies the service: a new one is added, a reused one is checked.
	StageService ConnectionStage = iota
	// StageCredential also applies the credential.
	StageCredential
	// StageScope also adds the connection with its name, targets, description and the entries a surface
	// carries over, still without permissions and tools.
	StageScope
	// StageRights also applies permissions and tools. The candidate is complete.
	StageRights
)

// ConnectionDraft is what a surface collected for a new connection. Names are taken as given: the surface
// trims what its input needs trimmed.
type ConnectionDraft struct {
	Provider string

	// NewService adds the service Service with BaseURL; otherwise Service names a configured service of
	// the provider.
	NewService bool
	Service    string
	BaseURL    string

	// NewCredential adds the credential Credential with the secrets kept at Storage (a config credential
	// type); otherwise Credential names a configured credential that may serve the provider. EnvNames are
	// the environment variables of an env credential by role: an entry is written as given, and the
	// configuration's validation refuses a role without a valid one.
	NewCredential bool
	Credential    string
	Storage       string
	EnvNames      map[string]string

	// The connection. Permissions is the comma-separated form of config.ParsePermissions: empty keeps the
	// provider's defaults, "none" allows nothing. With ToolsSelected, Tools is the explicit selection, and
	// no tool outside it is enabled, an empty one included; without it the connection offers every tool.
	Name          string
	Targets       []string
	Description   string
	Permissions   string
	ToolsSelected bool
	Tools         []string
	Paths         []string
	Files         config.Files
	Forward       []string
}

// ConnectionCandidate is a validated copy of the configuration with the new connection, and what saving it
// takes besides the file.
type ConnectionCandidate struct {
	Config *config.Config
	// Service, Credential and Name are the entries of the setup; Name stays empty before StageScope, and
	// Credential before StageCredential.
	Service, Credential, Name string
	NewService, NewCredential bool
	// Storage is the credential type of a new credential, "" for a reused one.
	Storage string
	// Roles are the secret roles of a new credential, in ascending order.
	Roles []string
}

// StoresSecrets reports whether saving writes secrets: a new keyring or vault credential keeps its secrets
// in a store, an env credential only names variables.
func (c ConnectionCandidate) StoresSecrets() bool {
	return c.NewCredential && c.Storage != config.CredentialTypeEnv
}

// MissingSecret is the first role of a new keyring or vault credential without a value, in role order, or
// nil.
func (c ConnectionCandidate) MissingSecret(secrets map[string]string) error {
	if !c.StoresSecrets() {
		return nil
	}
	for _, role := range c.Roles {
		if secrets[role] == "" {
			return &RoleMissingError{Role: role}
		}
	}
	return nil
}

// credentialServes reports whether a configured credential may serve a connection of provider: it is no
// payload credential and does not belong to another provider (see CredentialProvider).
func credentialServes(cfg *config.Config, name string, cred config.Credential, provider string) bool {
	if cred.Forward {
		return false
	}
	belongs := CredentialProvider(cfg, name, cred)
	return belongs == provider || belongs == ""
}

// BuildConnectionCandidate applies the draft, up to stage, to a copy of cfg and lets the core validate the
// result. Nothing decides on its own whether a target, a permission or a tool is allowed: the
// configuration's own methods do. A connection never gets a credential that belongs to another provider
// than its service. The errors are the types of this file, or the configuration's own.
func BuildConnectionCandidate(cfg *config.Config, d ConnectionDraft, stage ConnectionStage) (ConnectionCandidate, error) {
	cand, err := buildCandidate(cfg, d, stage)
	if err != nil {
		return ConnectionCandidate{}, err
	}
	return cand, nil
}

func buildCandidate(cfg *config.Config, d ConnectionDraft, stage ConnectionStage) (ConnectionCandidate, error) {
	cand := ConnectionCandidate{Config: cfg.Clone(), Service: d.Service, NewService: d.NewService}
	if !slices.Contains(cand.Config.Providers(), d.Provider) {
		return ConnectionCandidate{}, ErrChooseProvider
	}
	roles := cand.Config.SecretRolesOf(d.Provider)

	if d.NewService {
		if d.Service == "" {
			return cand, &EmptyNameError{KindService}
		}
		if _, taken := cand.Config.Services[d.Service]; taken {
			return cand, &NameTakenError{KindService, d.Service}
		}
		if err := cand.Config.SetService(d.Service, config.Service{Provider: d.Provider, BaseURL: d.BaseURL}); err != nil {
			return cand, err
		}
	} else if svc, ok := cand.Config.Services[d.Service]; !ok || svc.Provider != d.Provider {
		return cand, &UnknownEntryError{KindService, d.Service, d.Provider}
	}

	if stage >= StageCredential {
		cand.Credential, cand.NewCredential = d.Credential, d.NewCredential
		if d.NewCredential {
			if d.Credential == "" {
				return cand, &EmptyNameError{KindCredential}
			}
			if _, taken := cand.Config.Credentials[d.Credential]; taken {
				return cand, &NameTakenError{KindCredential, d.Credential}
			}
			if !slices.Contains(config.CredentialTypes(), d.Storage) {
				return cand, ErrChooseStorage
			}
			cand.Storage, cand.Roles = d.Storage, roles
			cred := config.Credential{Provider: d.Provider, Type: d.Storage}
			// Only an env credential names anything in the file; the secrets of a keyring or vault
			// credential go to a store and never into this configuration.
			if d.Storage == config.CredentialTypeEnv {
				cred.Values = map[string]string{}
				for _, role := range roles {
					if value, ok := d.EnvNames[role]; ok {
						cred.Values[role] = value
					}
				}
			}
			if err := cand.Config.SetCredential(d.Credential, cred); err != nil {
				return cand, err
			}
		} else if cred, ok := cand.Config.Credentials[d.Credential]; !ok || !credentialServes(cfg, d.Credential, cred, d.Provider) {
			return cand, &UnknownEntryError{KindCredential, d.Credential, d.Provider}
		}
	}

	if stage >= StageScope {
		cand.Name = d.Name
		if d.Name == "" {
			return cand, &EmptyNameError{KindConnection}
		}
		if _, taken := cand.Config.Connections[d.Name]; taken {
			return cand, &NameTakenError{KindConnection, d.Name}
		}
		target, targets := SplitTargets(d.Targets)
		conn := config.Connection{
			Service: d.Service, Credential: d.Credential, Target: target, Targets: targets,
			Description: d.Description, Paths: slices.Clone(d.Paths), ForwardSecrets: slices.Clone(d.Forward),
			Files: config.Files{Read: slices.Clone(d.Files.Read), Write: slices.Clone(d.Files.Write)},
		}
		if stage >= StageRights {
			permissions, err := config.ParsePermissions(d.Permissions)
			if err != nil {
				return cand, err
			}
			conn.Permissions = permissions
			if d.ToolsSelected {
				conn.Tools = slices.Clone(d.Tools)
				if conn.Tools == nil {
					conn.Tools = []string{}
				}
			}
		}
		if err := cand.Config.SetConnection(d.Name, conn); err != nil {
			return cand, err
		}
	}

	if err := cand.Config.Validate(); err != nil {
		return cand, err
	}
	return cand, nil
}

// SaveResult says what a saved connection setup left to report. Both parts are redacted and never carry a
// secret value.
type SaveResult struct {
	// Warning is set when the secrets of a new vault credential could not be handed on to a vault process
	// that holds the vault unlocked; the save itself stands.
	Warning string
	// Logged is the warning of the connection log, "" when it was written.
	Logged string
}

// SaveConnectionSetup saves a complete candidate against the revision base it was built from. before is the
// configuration the candidate was built from; the log compares with it. A candidate that stores secrets
// writes them first, from secrets by role, and the configuration with them in one commit (see
// CommitSecrets): a conflict or a failure leaves neither behind. offer is asked for a passphrase only when
// a secret would be the vault's very first. The approval of the new connection stays with the surface.
//
// A connection whose credential belongs to another provider than its service is refused here as well, so a
// candidate that was not built by BuildConnectionCandidate cannot save one.
func (s *Service) SaveConnectionSetup(before *config.Config, c ConnectionCandidate, base config.Revision,
	secrets map[string]string, offer vault.PassphraseFunc) (SaveResult, error) {
	if err := checkSetupProviders(before, c); err != nil {
		return SaveResult{}, err
	}
	var res SaveResult
	if c.StoresSecrets() {
		if err := c.MissingSecret(secrets); err != nil {
			return res, err
		}
		toVault := c.Storage == config.CredentialTypeVault
		warning, err := s.CommitSecrets(c.Config, base, c.Credential, toVault, c.Roles, secrets, offer)
		if err != nil {
			return res, err
		}
		res.Warning, res.Logged = warning, s.RecordConnections(before, c.Config)
		return res, nil
	}
	logged, err := s.SaveConfig(before, c.Config, base)
	res.Logged = logged
	return res, err
}

// checkSetupProviders refuses a new connection whose credential is of another provider than its service.
func checkSetupProviders(before *config.Config, c ConnectionCandidate) error {
	if c.Name == "" {
		return nil
	}
	conn, ok := c.Config.Connections[c.Name]
	if !ok {
		return nil
	}
	provider := c.Config.Services[conn.Service].Provider
	cred, ok := c.Config.Credentials[conn.Credential]
	if !ok {
		return nil
	}
	if c.NewCredential {
		if cred.Provider != provider {
			return &UnknownEntryError{KindCredential, conn.Credential, provider}
		}
		return nil
	}
	if !credentialServes(before, conn.Credential, cred, provider) {
		return &UnknownEntryError{KindCredential, conn.Credential, provider}
	}
	return nil
}
