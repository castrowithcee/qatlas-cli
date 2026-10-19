package config

import (
	"errors"
	"strings"
	"testing"
)

// problemsBase is a valid configuration over testProviders that every case below spoils in one place.
func problemsBase(t *testing.T) *Config {
	t.Helper()
	cfg := New(filesProviders{testProviders})
	cfg.Services = map[string]Service{
		"gh":   {Provider: "github", BaseURL: "https://api.github.com"},
		"wiki": {Provider: "bookstack", BaseURL: "https://wiki.example.invalid"},
		"docs": {Provider: "filer", BaseURL: "https://docs.example.invalid"},
		"seat": {Provider: "seatable.base"},
		"nc":   {Provider: "nextcloud", BaseURL: "https://nc.example.invalid"},
	}
	cfg.Credentials = map[string]Credential{
		"ghkey":   {Type: CredentialTypeEnv, Values: map[string]string{"token": "GH_TOKEN"}},
		"wikikey": {Type: CredentialTypeEnv, Values: map[string]string{"token-id": "W_ID", "token-secret": "W_SECRET"}},
		"dockey":  {Type: CredentialTypeEnv, Values: map[string]string{"token": "DOC_TOKEN"}},
		"seatkey": {Type: CredentialTypeEnv, Values: map[string]string{"api-token": "SEAT_TOKEN"}},
		"nckey": {Type: CredentialTypeEnv,
			Values: map[string]string{"user-id": "NC_USER", "app-password": "NC_PASS"}},
		"fwd": {Type: CredentialTypeVault, Forward: true, Fields: []string{"user"}},
	}
	cfg.Connections = map[string]Connection{
		"gh":   {Service: "gh", Credential: "ghkey", Target: "repos/a/b"},
		"wiki": {Service: "wiki", Credential: "wikikey"},
		"docs": {Service: "docs", Credential: "dockey"},
		"seat": {Service: "seat", Credential: "seatkey", Targets: []string{"t1"}},
		"nc":   {Service: "nc", Credential: "nckey", Target: "files"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("base configuration is invalid: %v", err)
	}
	return cfg
}

func TestProblemsAreStructured(t *testing.T) {
	seenCodes := map[string]bool{}
	long := strings.Repeat("a", 201)
	conn := func(c *Config, name string, edit func(*Connection)) {
		v := c.Connections[name]
		edit(&v)
		c.Connections[name] = v
	}
	cred := func(c *Config, name string, edit func(*Credential)) {
		v := c.Credentials[name]
		edit(&v)
		c.Credentials[name] = v
	}
	for _, tt := range []struct {
		name  string
		edit  func(*Config)
		loc   string
		code  string
		text  string
		field string
		index int // -1 for none
	}{
		{"version", func(c *Config) { c.Version = 9 }, "version", CodeUnsupportedVersion, "version: got 9, want 1, 2 or 3", "", -1},
		{"service name", func(c *Config) { c.Services["bad name"] = c.Services["gh"] }, "services.bad name",
			CodeInvalidName, "services.bad name: a service name must consist", "", -1},
		{"defaults name", func(c *Config) { c.Defaults.Connections = map[string]string{"bad domain": "gh"} },
			"defaults.connections.bad domain", CodeInvalidName, "defaults.connections.bad domain: a domain must", "connections.bad domain", -1},
		{"provider missing", func(c *Config) { c.Services["gh"] = Service{BaseURL: "https://api.github.com"} },
			"services.gh.provider", CodeProviderMissing, "services.gh: provider must not be empty", "provider", -1},
		{"service unknown provider", func(c *Config) { c.Services["gh"] = Service{Provider: "nope", BaseURL: "https://x.invalid"} },
			"services.gh.provider", CodeUnknownProvider, "services.gh: unknown provider \"nope\", known providers are", "provider", -1},
		{"credential unknown provider", func(c *Config) { cred(c, "ghkey", func(v *Credential) { v.Provider = "nope" }) },
			"credentials.ghkey.provider", CodeUnknownProvider, "credentials.ghkey: unknown provider \"nope\"", "provider", -1},
		{"note unknown provider", func(c *Config) { c.ProviderNotes = map[string]string{"nope": "n"} },
			"provider_notes.nope", CodeUnknownProvider, "provider_notes.nope: unknown provider, known providers are", "", -1},
		{"base url generic", func(c *Config) { c.Services["gh"] = Service{Provider: "github", BaseURL: "ftp://x.invalid"} },
			"services.gh.base_url", CodeInvalidBaseURL, "services.gh.base_url: ", "base_url", -1},
		{"base url provider", func(c *Config) { c.Services["gh"] = Service{Provider: "github", BaseURL: "https://x.invalid/a"} },
			"services.gh.base_url", CodeInvalidBaseURL, "services.gh.base_url: a GitHub base URL", "base_url", -1},
		{"unknown role", func(c *Config) {
			cred(c, "ghkey", func(v *Credential) {
				v.Provider = "github"
				v.Values = map[string]string{"token": "A_B", "extra": "A_C"}
			})
		}, "credentials.ghkey.values.extra", CodeUnknownSecretRole, "credentials.ghkey.values.extra: github does not define", "values.extra", -1},
		{"roles missing", func(c *Config) { c.Credentials["k"] = Credential{Type: CredentialTypeEnv} },
			"credentials.k.values", CodeSecretRolesMissing, "credentials.k.values: at least one secret role", "values", -1},
		{"empty role", func(c *Config) {
			c.Credentials["k"] = Credential{Type: CredentialTypeEnv, Values: map[string]string{"": "A_B"}}
		}, "credentials.k.values", CodeEmptySecretRole, "credentials.k.values: a secret role must not be empty", "values", -1},
		{"env name", func(c *Config) {
			cred(c, "ghkey", func(v *Credential) { v.Values = map[string]string{"token": "bad name"} })
		}, "credentials.ghkey.values.token", CodeInvalidEnvName, "credentials.ghkey.values.token: ", "values.token", -1},
		{"keyring values", func(c *Config) {
			c.Credentials["k"] = Credential{Type: CredentialTypeKeyring, Values: map[string]string{"a": "b"}}
		}, "credentials.k.values", CodeSecretValuesNotAllowed, "credentials.k.values: must be absent for a keyring", "values", -1},
		{"vault values", func(c *Config) {
			c.Credentials["k"] = Credential{Type: CredentialTypeVault, Values: map[string]string{"a": "b"}}
		}, "credentials.k.values", CodeSecretValuesNotAllowed, "credentials.k.values: must be absent for a vault", "values", -1},
		{"credential type", func(c *Config) { c.Credentials["k"] = Credential{Type: "x"} },
			"credentials.k.type", CodeInvalidCredentialType, "credentials.k: type must be one of", "type", -1},
		{"fields without forward", func(c *Config) { cred(c, "ghkey", func(v *Credential) { v.Fields = []string{"a"} }) },
			"credentials.ghkey.fields", CodeForwardFieldsNotAllowed, "credentials.ghkey.fields: only a forward", "fields", -1},
		{"description without forward", func(c *Config) { cred(c, "ghkey", func(v *Credential) { v.Description = "d" }) },
			"credentials.ghkey.description", CodeForwardDescriptionNotAllowed, "credentials.ghkey.description: only a forward", "description", -1},
		{"forward description", func(c *Config) { cred(c, "fwd", func(v *Credential) { v.Description = long }) },
			"credentials.fwd.description", CodeInvalidDescription, "credentials.fwd.description: is 201 characters", "description", -1},
		{"forward provider", func(c *Config) { cred(c, "fwd", func(v *Credential) { v.Provider = "github" }) },
			"credentials.fwd.provider", CodeForwardProviderNotAllowed, "credentials.fwd.provider: a forward", "provider", -1},
		{"forward type", func(c *Config) { cred(c, "fwd", func(v *Credential) { v.Type = CredentialTypeEnv }) },
			"credentials.fwd.type", CodeInvalidForwardType, "credentials.fwd.type: a forward credential must be", "type", -1},
		{"forward fields missing", func(c *Config) { cred(c, "fwd", func(v *Credential) { v.Fields = nil }) },
			"credentials.fwd.fields", CodeForwardFieldsMissing, "credentials.fwd.fields: a forward credential needs", "fields", -1},
		{"field name", func(c *Config) { cred(c, "fwd", func(v *Credential) { v.Fields = []string{"bad name"} }) },
			"credentials.fwd.fields[0]", CodeInvalidFieldName, "credentials.fwd.fields[0]: a field name must", "fields", 0},
		{"duplicate field", func(c *Config) { cred(c, "fwd", func(v *Credential) { v.Fields = []string{"a", "a"} }) },
			"credentials.fwd.fields[1]", CodeDuplicateField, "credentials.fwd.fields[1]: a field is listed more", "fields", 1},
		{"unknown service", func(c *Config) { conn(c, "gh", func(v *Connection) { v.Service = "nope" }) },
			"connections.gh.service", CodeUnknownService, "connections.gh.service: unknown service \"nope\"", "service", -1},
		{"unknown credential", func(c *Config) { conn(c, "gh", func(v *Connection) { v.Credential = "nope" }) },
			"connections.gh.credential", CodeUnknownCredential, "connections.gh.credential: unknown credential \"nope\"", "credential", -1},
		{"forward as login", func(c *Config) { conn(c, "gh", func(v *Connection) { v.Credential = "fwd" }) },
			"connections.gh.credential", CodeForwardCredentialAsLogin, "connections.gh.credential: credential \"fwd\" is a forward", "credential", -1},
		{"unknown forward secret", func(c *Config) { conn(c, "gh", func(v *Connection) { v.ForwardSecrets = []string{"nope"} }) },
			"connections.gh.forward_secrets[0]", CodeUnknownForwardSecret, "connections.gh.forward_secrets[0]: unknown credential", "forward_secrets", 0},
		{"not forward", func(c *Config) { conn(c, "gh", func(v *Connection) { v.ForwardSecrets = []string{"ghkey"} }) },
			"connections.gh.forward_secrets", CodeNotForwardCredential, "connections.gh.forward_secrets: credential \"ghkey\" is not", "forward_secrets", -1},
		{"duplicate forward secret", func(c *Config) { conn(c, "gh", func(v *Connection) { v.ForwardSecrets = []string{"fwd", "fwd"} }) },
			"connections.gh.forward_secrets", CodeDuplicateForwardSecret, "connections.gh.forward_secrets: credential \"fwd\" is listed", "forward_secrets", -1},
		{"provider mismatch", func(c *Config) { cred(c, "ghkey", func(v *Credential) { v.Provider = "bookstack" }) },
			"connections.gh", CodeProviderMismatch, "connections.gh: service \"gh\" belongs to provider \"github\"", "", -1},
		{"role missing", func(c *Config) {
			cred(c, "ghkey", func(v *Credential) { v.Values = map[string]string{"token": ""} })
		}, "connections.gh", CodeSecretRoleMissing, "connections.gh: provider \"github\" requires the secret role \"token\"", "", -1},
		{"connection description", func(c *Config) { conn(c, "gh", func(v *Connection) { v.Description = long }) },
			"connections.gh.description", CodeInvalidDescription, "connections.gh.description: is 201 characters", "description", -1},
		{"paths empty", func(c *Config) { conn(c, "gh", func(v *Connection) { v.Paths = []string{} }) },
			"connections.gh.paths", CodePathsEmpty, "connections.gh.paths: must name at least one", "paths", -1},
		{"path", func(c *Config) { conn(c, "gh", func(v *Connection) { v.Paths = []string{"rel"} }) },
			"connections.gh.paths[0]", CodeInvalidPath, "connections.gh.paths[0]: must name a directory", "paths", 0},
		{"duplicate path", func(c *Config) {
			conn(c, "gh", func(v *Connection) { v.Paths = []string{drive + "/srv/a", drive + "/srv/a"} })
		}, "connections.gh.paths[1]", CodeDuplicatePath, "connections.gh.paths[1]: a directory is listed", "paths", 1},
		{"unknown permission", func(c *Config) { conn(c, "gh", func(v *Connection) { v.Effects = []Effect{"fly"} }) },
			"connections.gh.effects", CodeUnknownEffect, "connections.gh.effects: unknown permission \"fly\"", "effects", -1},
		{"duplicate permission", func(c *Config) {
			conn(c, "gh", func(v *Connection) { v.Effects = []Effect{EffectRead, EffectRead} })
		}, "connections.gh.effects", CodeDuplicateEffect, "connections.gh.effects: permission \"read\" is listed", "effects", -1},
		{"target required", func(c *Config) { conn(c, "gh", func(v *Connection) { v.Target = "" }) },
			"connections.gh.target", CodeTargetRequired, "connections.gh.target: provider \"github\" requires", "target", -1},
		{"target and targets", func(c *Config) { conn(c, "gh", func(v *Connection) { v.Targets = []string{"repos/a/b"} }) },
			"connections.gh", CodeTargetAndTargets, "connections.gh: target and targets cannot both be set", "", -1},
		{"single only", func(c *Config) { conn(c, "nc", func(v *Connection) { v.Target = ""; v.Targets = []string{"files"} }) },
			"connections.nc.targets", CodeTargetsSingleOnly, "connections.nc.targets: provider \"nextcloud\" accepts only one", "targets", -1},
		{"empty target in list", func(c *Config) { conn(c, "seat", func(v *Connection) { v.Targets = []string{"t1", " "} }) },
			"connections.seat.targets[1]", CodeEmptyTarget, "connections.seat.targets[1]: targets must not be empty", "targets", 1},
		{"duplicate target", func(c *Config) { conn(c, "seat", func(v *Connection) { v.Targets = []string{"t1", "t1"} }) },
			"connections.seat.targets[1]", CodeDuplicateTarget, "connections.seat.targets[1]: a target is listed", "targets", 1},
		{"invalid target", func(c *Config) { conn(c, "gh", func(v *Connection) { v.Target = "x" }) },
			"connections.gh.target", CodeInvalidTarget, "connections.gh.target: a GitHub target must", "target", -1},
		{"invalid target in list", func(c *Config) {
			conn(c, "gh", func(v *Connection) { v.Target = ""; v.Targets = []string{"repos/a", "x"} })
		},
			"connections.gh.targets[1]", CodeInvalidTarget, "connections.gh.targets[1]: a GitHub target must", "targets", 1},
		{"invalid target set", func(c *Config) {
			conn(c, "gh", func(v *Connection) { v.Target = ""; v.Targets = []string{"repos/a", "orgs/b"} })
		}, "connections.gh.targets", CodeInvalidTargetSet, "connections.gh.targets: several GitHub targets", "targets", -1},
		{"wildcard", func(c *Config) { conn(c, "seat", func(v *Connection) { v.Targets = []string{"*", "t1"} }) },
			"connections.seat.targets", CodeWildcardNotAlone, "connections.seat.targets: wildcard \"*\" must be", "targets", -1},
		{"duplicate tool", func(c *Config) {
			conn(c, "gh", func(v *Connection) { v.Tools = []string{"github.issues.get", "github.issues.get"} })
		}, "connections.gh.tools", CodeDuplicateTool, "connections.gh.tools: tool \"github.issues.get\" is listed", "tools", -1},
		{"tool effect", func(c *Config) {
			conn(c, "gh", func(v *Connection) {
				v.legacyEffects = []Effect{EffectRead}
				v.Tools = []string{"github.issues.create"}
			})
		}, "connections.gh.tools", CodeToolEffectNotPermitted,
			"connections.gh.tools: tool \"github.issues.create\" has effect create, which the former permissions of the connection do not allow", "tools", -1},
		{"effects and tools", func(c *Config) {
			conn(c, "gh", func(v *Connection) {
				v.Effects = []Effect{EffectRead}
				v.Tools = []string{"github.issues.get"}
			})
		}, "connections.gh.effects", CodeEffectsAndTools, "connections.gh.effects: effects and tools are both given", "effects", -1},
		{"permissions and effects", func(c *Config) { conn(c, "gh", func(v *Connection) { v.bothEffectFields = true }) },
			"connections.gh.effects", CodePermissionsAndEffects, "connections.gh.effects: both effects and its former name", "effects", -1},
		{"permissions in a current file", func(c *Config) { conn(c, "gh", func(v *Connection) { v.permissionsInCurrent = true }) },
			"connections.gh.effects", CodePermissionsRenamed, "connections.gh.effects: permissions is the former name", "effects", -1},
		{"tool of other provider", func(c *Config) { conn(c, "gh", func(v *Connection) { v.Tools = []string{"bookstack.pages.get"} }) },
			"connections.gh.tools", CodeToolOfOtherProvider, "connections.gh.tools: tool \"bookstack.pages.get\" belongs to provider \"bookstack\"", "tools", -1},
		{"unknown tool entry", func(c *Config) {
			conn(c, "gh", func(v *Connection) { v.Tools = []string{"github.issues.get", "nope"} })
		},
			"connections.gh.tools[2]", CodeUnknownToolEntry, "connections.gh.tools: entry 2 is not a registered tool", "tools", 2},
		{"files empty", func(c *Config) { conn(c, "docs", func(v *Connection) { v.Files.Read = []string{} }) },
			"connections.docs.files.read", CodeFilesEmpty, "connections.docs.files.read: must name at least one", "files.read", -1},
		{"files direction", func(c *Config) { conn(c, "wiki", func(v *Connection) { v.Files.Write = []string{drive + "/srv/a"} }) },
			"connections.wiki.files.write", CodeFilesDirectionUnsupported, "connections.wiki.files.write: the provider of this connection has no tool", "files.write", -1},
		{"files entry", func(c *Config) { conn(c, "docs", func(v *Connection) { v.Files.Read = []string{"rel"} }) },
			"connections.docs.files.read[0]", CodeInvalidFilesEntry, "connections.docs.files.read[0]: must name a directory", "files.read", 0},
		{"duplicate files entry", func(c *Config) {
			conn(c, "docs", func(v *Connection) { v.Files.Write = []string{drive + "/srv/a", drive + "/srv/a"} })
		}, "connections.docs.files.write[1]", CodeDuplicateFilesEntry, "connections.docs.files.write[1]: a directory is listed", "files.write", 1},
		{"note", func(c *Config) { c.ProviderNotes = map[string]string{"github": long} },
			"provider_notes.github", CodeInvalidNote, "provider_notes.github: is 201 characters", "", -1},
		{"default connection", func(c *Config) { c.Defaults.Connections = map[string]string{"git": "nope"} },
			"defaults.connections.git", CodeUnknownConnection, "defaults.connections.git: unknown connection \"nope\"", "connections.git", -1},
		{"secret store", func(c *Config) { c.Defaults.SecretStore = "x" },
			"defaults.secret_store", CodeInvalidSecretStore, "defaults.secret_store: must be keyring or vault", "secret_store", -1},
		{"idle timeout", func(c *Config) { c.Vault.IdleTimeout = "x" },
			"vault.idle_timeout", CodeInvalidTimeout, "vault.idle_timeout: must be a duration", "idle_timeout", -1},
		{"admin timeout", func(c *Config) { c.Vault.AdminTimeout = "x" },
			"vault.admin_timeout", CodeInvalidTimeout, "vault.admin_timeout: ", "admin_timeout", -1},
		{"retention", func(c *Config) { c.Logs.RetentionDays = -1 },
			"logs.retention_days", CodeInvalidRetention, "logs.retention_days: must be a positive integer, got -1", "retention_days", -1},
		{"malformed id", func(c *Config) { c.Services["gh"] = Service{ID: "zz", Provider: "github"} },
			"services.gh.id", CodeInvalidID, "services.gh.id: must be 32 lower-case hex digits", "id", -1},
		{"duplicate id", func(c *Config) {
			id := strings.Repeat("a", 32)
			s := c.Services["gh"]
			s.ID = id
			c.Services["gh"] = s
			w := c.Services["wiki"]
			w.ID = id
			c.Services["wiki"] = w
		}, "services.wiki.id", CodeDuplicateID, "services.wiki.id: duplicate of services.gh.id", "id", -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := problemsBase(t)
			tt.edit(cfg)
			err := cfg.Validate()
			var got *Problem
			problems := Problems(err)
			for i := range problems {
				if problems[i].Code == tt.code {
					got = &problems[i]
					break
				}
			}
			if got == nil {
				t.Fatalf("no problem with code %s in %v", tt.code, err)
			}
			if !strings.HasPrefix(got.Error(), tt.text) {
				t.Errorf("Error() = %q, want prefix %q", got.Error(), tt.text)
			}
			if !strings.Contains(err.Error(), got.Error()) {
				t.Errorf("joined text %q lacks %q", err, got)
			}
			if got.Location() != tt.loc && !strings.HasPrefix(got.Error(), tt.loc) {
				t.Errorf("Location() = %q, want %q", got.Location(), tt.loc)
			}
			if got.Field != tt.field {
				t.Errorf("Field = %q, want %q", got.Field, tt.field)
			}
			if got.HasIndex != (tt.index >= 0) || (got.HasIndex && got.Index != tt.index) {
				t.Errorf("Index = %d (set %v), want %d", got.Index, got.HasIndex, tt.index)
			}
			if got.Section != strings.SplitN(tt.loc, ".", 2)[0] {
				t.Errorf("Section = %q, want the first key of %q", got.Section, tt.loc)
			}
			if got.Hint() == "" {
				t.Errorf("code %s has no hint", got.Code)
			}
			seenCodes[tt.code] = true
		})
	}

	// No code without a hint, no hint for a code the table never provokes.
	for code, hint := range hints {
		if strings.TrimSpace(hint) == "" {
			t.Errorf("code %s has an empty hint", code)
		}
		if strings.Contains(strings.ToLower(hint), "github") || strings.Contains(strings.ToLower(hint), "bookstack") {
			t.Errorf("hint of %s names a provider", code)
		}
		if !seenCodes[code] {
			t.Errorf("code %s is never reported by the table", code)
		}
	}
	for code := range seenCodes {
		if _, ok := hints[code]; !ok {
			t.Errorf("code %s has no hint", code)
		}
	}
}

func TestProblemToolEffectHint(t *testing.T) {
	if got := (Problem{Code: CodeToolEffectNotPermitted}).Hint(); got != "Remove these tools, or list their effect among the former permissions." {
		t.Errorf("Hint() = %q", got)
	}
}

// TestProblemParametersCarryNoFreeValue spoils one entry per kind of free value and checks that none of them
// reaches an exported field, while the rendered text still shows it.
func TestProblemParametersCarryNoFreeValue(t *testing.T) {
	const canary = "canary-9f3a"
	cfg := problemsBase(t)
	cfg.Services["gh"] = Service{Provider: canary + "-provider", BaseURL: "ftp://" + canary + ".invalid"}
	cfg.Services["wiki"] = Service{Provider: "github", BaseURL: "https://" + canary + ".invalid/x"}
	cfg.Credentials["k1"] = Credential{Type: canary + "-type"}
	cfg.Credentials["k2"] = Credential{Type: CredentialTypeKeyring, Values: map[string]string{"a": canary + "-secret"}}
	cfg.Credentials["k3"] = Credential{Type: CredentialTypeEnv, Provider: canary + "-cprovider",
		Values: map[string]string{"a": canary + " secret"}}
	cfg.Credentials["fwd"] = Credential{Type: CredentialTypeVault, Forward: true, Description: canary + strings.Repeat("a", 200),
		Fields: []string{"user"}}
	cfg.Connections["docs"] = Connection{Service: canary + "-service", Credential: canary + "-credential",
		ForwardSecrets: []string{canary + "-forward", canary + "-forward"}, Description: canary + strings.Repeat("a", 200),
		Paths: []string{canary + "/relative"}, Effects: []Effect{Effect(canary + "-perm")},
		Files: Files{Read: []string{canary + "/relative"}}}
	cfg.Connections["gh"] = Connection{Service: "wiki", Credential: "wikikey", Target: canary + "-target",
		Tools: []string{canary + "-tool"}}
	cfg.Connections["seat"] = Connection{Service: "seat", Credential: "seatkey",
		Targets: []string{canary + "-a", canary + "-a"}}
	cfg.Defaults.Connections = map[string]string{"git": canary + "-conn"}
	cfg.Defaults.SecretStore = canary + "-store"
	cfg.Vault.IdleTimeout = canary
	cfg.Logs.RetentionDays = -7

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), canary) {
		t.Fatalf("Validate() = %v, want a text that quotes the canaries", err)
	}
	problems := Problems(err)
	if len(problems) == 0 {
		t.Fatal("no problems")
	}
	for _, p := range problems {
		fields := []string{p.Section, p.Name, p.Field, p.Code, p.Hint()}
		for k, v := range p.Params {
			fields = append(fields, k, v)
		}
		for _, f := range fields {
			if strings.Contains(f, canary) {
				t.Errorf("problem %s exposes a free value in %q", p.Code, f)
			}
		}
	}
}

func TestProblemsThroughInvalidError(t *testing.T) {
	cfg := problemsBase(t)
	cfg.Logs.RetentionDays = -1
	cfg.Vault.IdleTimeout = "x"
	err := &InvalidError{Path: "/x/config.yaml", Err: cfg.Validate()}
	got := Problems(err)
	if len(got) != 2 || got[0].Code != CodeInvalidTimeout || got[1].Code != CodeInvalidRetention {
		t.Fatalf("Problems() = %+v", got)
	}
	if want := "/x/config.yaml: " + got[0].Error() + "\n" + got[1].Error(); err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
	if Problems(errors.New("plain")) != nil || Problems(nil) != nil {
		t.Error("Problems of a plain error must be nil")
	}
}

func TestProblemKeepsProviderErrorAsCause(t *testing.T) {
	cfg := problemsBase(t)
	c := cfg.Connections["gh"]
	c.Target = "x"
	cfg.Connections["gh"] = c
	var cause error
	for _, p := range Problems(cfg.Validate()) {
		if p.Code == CodeInvalidTarget {
			cause = errors.Unwrap(p)
		}
	}
	if cause == nil || cause.Error() != "a GitHub target must name a project or a repository" {
		t.Errorf("cause = %v", cause)
	}
}
