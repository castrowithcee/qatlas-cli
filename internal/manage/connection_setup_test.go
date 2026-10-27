package manage

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

type setupCatalog struct{}

var setupProviders = []config.ProviderMetadata{
	{
		ID: "docs", DefaultBaseURL: "https://docs.example",
		SecretRoles: []config.SecretRole{{Name: "key-id"}, {Name: "key-secret"}},
		Tools: []config.ToolMetadata{
			{ID: "docs.read", Effect: config.EffectRead},
			{ID: "docs.write", Effect: config.EffectUpdate},
			{ID: "docs.fetch", Effect: config.EffectRead, LocalFiles: config.LocalFilesRead},
		},
		LocalFiles: config.LocalFilesSupport{Read: true},
	},
	{ID: "mail", SecretRoles: []config.SecretRole{{Name: "api-key"}}},
}

func (setupCatalog) ProviderMetadata(id string) (config.ProviderMetadata, bool) {
	for _, p := range setupProviders {
		if p.ID == id {
			return p, true
		}
	}
	return config.ProviderMetadata{}, false
}

func (setupCatalog) ProviderMetadataAll() []config.ProviderMetadata { return setupProviders }

func setupConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.New(setupCatalog{})
	for name, svc := range map[string]config.Service{
		"docs-a": {Provider: "docs", BaseURL: "https://a.example"}, "mail-a": {Provider: "mail", BaseURL: "https://mail.example"},
	} {
		if err := cfg.SetService(name, svc); err != nil {
			t.Fatal(err)
		}
	}
	for name, cred := range map[string]config.Credential{
		"docs-key": {Provider: "docs", Type: config.CredentialTypeKeyring},
		"mail-key": {Provider: "mail", Type: config.CredentialTypeKeyring},
		"payload":  {Type: config.CredentialTypeKeyring, Forward: true, Fields: []string{"user"}},
		"derived":  {Type: config.CredentialTypeKeyring},
	} {
		if err := cfg.SetCredential(name, cred); err != nil {
			t.Fatal(err)
		}
	}
	if err := cfg.SetConnection("mail", config.Connection{Service: "mail-a", Credential: "derived"}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// reuse is a draft that reuses a docs service and credential under a free name.
func reuse() ConnectionDraft {
	return ConnectionDraft{Provider: "docs", Service: "docs-a", Credential: "docs-key", Name: "mine"}
}

func TestBuildConnectionCandidateRights(t *testing.T) {
	tests := []struct {
		name        string
		edit        func(*ConnectionDraft)
		wantPerm    []config.Effect
		wantTools   []string
		toolsNil    bool
		permsNil    bool
		wantFilesRd []string
	}{
		{name: "empty rights keep the defaults and offer every tool", edit: func(*ConnectionDraft) {},
			permsNil: true, toolsNil: true},
		{name: "none allows nothing", edit: func(d *ConnectionDraft) { d.Effects = "none" },
			wantPerm: []config.Effect{}, toolsNil: true},
		{name: "listed permissions", edit: func(d *ConnectionDraft) { d.Effects = "read, update" },
			wantPerm: []config.Effect{config.EffectRead, config.EffectUpdate}, toolsNil: true},
		{name: "selected tools are stored alone", edit: func(d *ConnectionDraft) {
			d.Effects, d.ToolsSelected, d.Tools = "read", true, []string{"docs.read"}
		}, permsNil: true, wantTools: []string{"docs.read"}},
		{name: "a selected tool of another effect than the rights text", edit: func(d *ConnectionDraft) {
			d.Effects, d.ToolsSelected, d.Tools = "read", true, []string{"docs.write"}
		}, permsNil: true, wantTools: []string{"docs.write"}},
		{name: "an empty selection enables no tool", edit: func(d *ConnectionDraft) {
			d.ToolsSelected = true
		}, permsNil: true, wantTools: []string{}},
		{name: "tools without a selection are ignored", edit: func(d *ConnectionDraft) {
			d.Tools = []string{"docs.write"}
		}, permsNil: true, toolsNil: true},
		{name: "files", edit: func(d *ConnectionDraft) {
			d.Files = config.Files{Read: []string{"~/docs"}}
		}, permsNil: true, toolsNil: true, wantFilesRd: []string{"~/docs"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := reuse()
			tt.edit(&d)
			cand, err := BuildConnectionCandidate(setupConfig(t), d, StageRights)
			if err != nil {
				t.Fatalf("BuildConnectionCandidate() = %v", err)
			}
			conn := cand.Config.Connections["mine"]
			if (conn.Effects == nil) != tt.permsNil || !slices.Equal(conn.Effects, tt.wantPerm) {
				t.Errorf("permissions = %#v, want %#v (nil %v)", conn.Effects, tt.wantPerm, tt.permsNil)
			}
			if (conn.Tools == nil) != tt.toolsNil || !slices.Equal(conn.Tools, tt.wantTools) {
				t.Errorf("tools = %#v, want %#v (nil %v)", conn.Tools, tt.wantTools, tt.toolsNil)
			}
			if !slices.Equal(conn.Files.Read, tt.wantFilesRd) {
				t.Errorf("files.read = %v, want %v", conn.Files.Read, tt.wantFilesRd)
			}
			if cand.Name != "mine" || cand.NewService || cand.NewCredential || cand.StoresSecrets() {
				t.Errorf("unexpected candidate %+v", cand)
			}
		})
	}
}

func TestBuildConnectionCandidateForward(t *testing.T) {
	d := reuse()
	d.Forward = []string{"payload"}
	cand, err := BuildConnectionCandidate(setupConfig(t), d, StageRights)
	if err != nil {
		t.Fatalf("BuildConnectionCandidate() = %v", err)
	}
	if got := cand.Config.Connections["mine"].ForwardSecrets; !slices.Equal(got, []string{"payload"}) {
		t.Errorf("forward_secrets = %v", got)
	}
	d.Forward = []string{"docs-key"}
	if _, err := BuildConnectionCandidate(setupConfig(t), d, StageRights); err == nil {
		t.Error("a credential that is no payload credential was released")
	}
}

func TestBuildConnectionCandidateStages(t *testing.T) {
	cfg := setupConfig(t)
	d := reuse()
	d.Effects, d.ToolsSelected, d.Tools = "read", true, []string{"docs.read"}
	cand, err := BuildConnectionCandidate(cfg, d, StageScope)
	if err != nil {
		t.Fatal(err)
	}
	if conn := cand.Config.Connections["mine"]; conn.Effects != nil || conn.Tools != nil {
		t.Errorf("rights applied before their stage: %+v", conn)
	}
	cand, err = BuildConnectionCandidate(cfg, ConnectionDraft{Provider: "docs", Service: "docs-a", Name: "taken"}, StageService)
	if err != nil || len(cand.Config.Connections) != 1 || cand.Name != "" || cand.Credential != "" {
		t.Errorf("service stage = %+v, %v", cand, err)
	}
	if _, ok := cfg.Connections["mine"]; ok {
		t.Error("the input configuration was changed")
	}
}

func TestBuildConnectionCandidateErrors(t *testing.T) {
	var (
		taken   *NameTakenError
		unknown *UnknownEntryError
		empty   *EmptyNameError
	)
	newCred := func(d *ConnectionDraft) {
		d.NewCredential, d.Credential, d.Storage = true, "fresh", config.CredentialTypeKeyring
	}
	tests := []struct {
		name string
		edit func(*ConnectionDraft)
		is   any
		eq   error
	}{
		{name: "no provider", edit: func(d *ConnectionDraft) { d.Provider = "" }, eq: ErrChooseProvider},
		{name: "service name taken", edit: func(d *ConnectionDraft) { d.NewService, d.Service = true, "docs-a" }, is: &taken},
		{name: "service name empty", edit: func(d *ConnectionDraft) { d.NewService, d.Service = true, "" }, is: &empty},
		{name: "service of another provider", edit: func(d *ConnectionDraft) { d.Service = "mail-a" }, is: &unknown},
		{name: "unknown service", edit: func(d *ConnectionDraft) { d.Service = "none" }, is: &unknown},
		{name: "credential name taken", edit: func(d *ConnectionDraft) { newCred(d); d.Credential = "docs-key" }, is: &taken},
		{name: "credential name empty", edit: func(d *ConnectionDraft) { newCred(d); d.Credential = "" }, is: &empty},
		{name: "no storage", edit: func(d *ConnectionDraft) { newCred(d); d.Storage = "" }, eq: ErrChooseStorage},
		{name: "credential of another provider", edit: func(d *ConnectionDraft) { d.Credential = "mail-key" }, is: &unknown},
		{name: "credential derived to another provider", edit: func(d *ConnectionDraft) { d.Credential = "derived" }, is: &unknown},
		{name: "payload credential", edit: func(d *ConnectionDraft) { d.Credential = "payload" }, is: &unknown},
		{name: "unknown credential", edit: func(d *ConnectionDraft) { d.Credential = "none" }, is: &unknown},
		{name: "connection name taken", edit: func(d *ConnectionDraft) { d.Name = "mail" }, is: &taken},
		{name: "connection name empty", edit: func(d *ConnectionDraft) { d.Name = "" }, is: &empty},
		{name: "unknown permission", edit: func(d *ConnectionDraft) { d.Effects = "admin" }},
		{name: "tool the provider does not register", edit: func(d *ConnectionDraft) {
			d.ToolsSelected, d.Tools = true, []string{"docs.nothing"}
		}},
		{name: "files without a direction", edit: func(d *ConnectionDraft) { d.Files.Write = []string{"/srv"} }},
		{name: "env role without a variable", edit: func(d *ConnectionDraft) {
			newCred(d)
			d.Storage, d.EnvNames = config.CredentialTypeEnv, map[string]string{"key-id": "DOCS_ID"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := reuse()
			tt.edit(&d)
			cand, err := BuildConnectionCandidate(setupConfig(t), d, StageRights)
			if err == nil {
				t.Fatal("BuildConnectionCandidate() accepted the draft")
			}
			if cand.Config != nil {
				t.Error("an error must not hand out a candidate")
			}
			switch target := tt.is.(type) {
			case nil:
			case **NameTakenError:
				if !errors.As(err, target) {
					t.Errorf("error %T, want NameTakenError", err)
				}
			case **UnknownEntryError:
				if !errors.As(err, target) {
					t.Errorf("error %T, want UnknownEntryError", err)
				}
			case **EmptyNameError:
				if !errors.As(err, target) {
					t.Errorf("error %T, want EmptyNameError", err)
				}
			}
			if tt.eq != nil && !errors.Is(err, tt.eq) {
				t.Errorf("error %v, want %v", err, tt.eq)
			}
		})
	}
}

func TestBuildConnectionCandidateNewEntries(t *testing.T) {
	d := ConnectionDraft{
		Provider: "docs", NewService: true, Service: "docs-b", BaseURL: "https://b.example",
		NewCredential: true, Credential: "fresh", Storage: config.CredentialTypeKeyring,
		Name: "mine", Targets: []string{"one"},
	}
	cand, err := BuildConnectionCandidate(setupConfig(t), d, StageRights)
	if err != nil {
		t.Fatal(err)
	}
	conn := cand.Config.Connections["mine"]
	if !cand.NewService || !cand.NewCredential || !cand.StoresSecrets() || cand.Storage != config.CredentialTypeKeyring {
		t.Errorf("unexpected candidate %+v", cand)
	}
	if !slices.Equal(cand.Roles, []string{"key-id", "key-secret"}) {
		t.Errorf("roles = %v", cand.Roles)
	}
	if conn.Target != "one" || conn.Targets != nil {
		t.Errorf("targets = %q, %v", conn.Target, conn.Targets)
	}
	if cred := cand.Config.Credentials["fresh"]; cred.Provider != "docs" || len(cred.Values) != 0 {
		t.Errorf("credential = %+v, want one that names its provider and holds no value", cred)
	}
	err = cand.MissingSecret(map[string]string{"key-id": "x"})
	var missing *RoleMissingError
	if !errors.As(err, &missing) || missing.Role != "key-secret" {
		t.Errorf("MissingSecret() = %v, want key-secret missing", err)
	}
	if err := cand.MissingSecret(map[string]string{"key-id": "x", "key-secret": "y"}); err != nil {
		t.Errorf("MissingSecret() = %v with every role given", err)
	}
}

func TestBuildConnectionCandidateEnvValues(t *testing.T) {
	d := ConnectionDraft{
		Provider: "docs", Service: "docs-a", NewCredential: true, Credential: "vars",
		Storage: config.CredentialTypeEnv, EnvNames: map[string]string{"key-id": "DOCS_ID", "key-secret": "DOCS_SECRET"},
		Name: "mine",
	}
	cand, err := BuildConnectionCandidate(setupConfig(t), d, StageRights)
	if err != nil {
		t.Fatal(err)
	}
	if got := cand.Config.Credentials["vars"].Values; got["key-id"] != "DOCS_ID" || got["key-secret"] != "DOCS_SECRET" {
		t.Errorf("values = %v", got)
	}
	if cand.StoresSecrets() || cand.MissingSecret(nil) != nil {
		t.Error("an env credential stores no secret")
	}
}

type setupFixture struct {
	svc     *Service
	secrets *fakeSecrets
	store   *config.Store
	cfg     *config.Config
	rev     config.Revision
}

func newSetupFixture(t *testing.T) *setupFixture {
	t.Helper()
	store := config.NewStore(filepath.Join(t.TempDir(), "qatlas", "config.yaml"), setupCatalog{})
	if err := store.Save(setupConfig(t)); err != nil {
		t.Fatal(err)
	}
	f := &setupFixture{secrets: newFakeSecrets(), store: store}
	f.svc = New(store, f.secrets, connlog.SurfaceWeb, &redact.Redactor{}).WithVaultProcessSupport(false)
	var err error
	if f.cfg, f.rev, err = f.svc.Load(); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSaveConnectionSetupStoresSecretsAndConnection(t *testing.T) {
	f := newSetupFixture(t)
	d := ConnectionDraft{
		Provider: "docs", Service: "docs-a", NewCredential: true, Credential: "fresh",
		Storage: config.CredentialTypeKeyring, Name: "mine",
	}
	cand, err := BuildConnectionCandidate(f.cfg, d, StageRights)
	if err != nil {
		t.Fatal(err)
	}
	secrets := map[string]string{"key-id": "id-1", "key-secret": "sec-2"}
	res, err := f.svc.SaveConnectionSetup(f.cfg, cand, f.rev, secrets, nil)
	if err != nil || res.Warning != "" {
		t.Fatalf("SaveConnectionSetup() = %+v, %v", res, err)
	}
	if f.secrets.entries["fresh.key-id"] != "id-1" || f.secrets.entries["fresh.key-secret"] != "sec-2" {
		t.Errorf("entries = %v", f.secrets.entries)
	}
	saved, err := f.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if conn, ok := saved.Connections["mine"]; !ok || conn.Credential != "fresh" {
		t.Errorf("saved connection = %+v, %v", conn, ok)
	}
}

func TestSaveConnectionSetupMissingSecretWritesNothing(t *testing.T) {
	f := newSetupFixture(t)
	d := ConnectionDraft{
		Provider: "docs", Service: "docs-a", NewCredential: true, Credential: "fresh",
		Storage: config.CredentialTypeKeyring, Name: "mine",
	}
	cand, err := BuildConnectionCandidate(f.cfg, d, StageRights)
	if err != nil {
		t.Fatal(err)
	}
	var missing *RoleMissingError
	_, err = f.svc.SaveConnectionSetup(f.cfg, cand, f.rev, map[string]string{"key-id": "id-1"}, nil)
	if !errors.As(err, &missing) || missing.Role != "key-secret" {
		t.Fatalf("SaveConnectionSetup() = %v, want key-secret missing", err)
	}
	if len(f.secrets.entries) != 0 || len(f.secrets.sets) != 0 {
		t.Errorf("secrets were written: %v", f.secrets.sets)
	}
	if saved, _ := f.store.Load(); saved != nil {
		if _, ok := saved.Connections["mine"]; ok {
			t.Error("the connection was saved")
		}
	}
}

func TestSaveConnectionSetupReusedCredential(t *testing.T) {
	f := newSetupFixture(t)
	cand, err := BuildConnectionCandidate(f.cfg, reuse(), StageRights)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SaveConnectionSetup(f.cfg, cand, f.rev, nil, nil); err != nil {
		t.Fatalf("SaveConnectionSetup() = %v", err)
	}
	if len(f.secrets.sets) != 0 {
		t.Errorf("a reused credential wrote secrets: %v", f.secrets.sets)
	}
	if saved, _ := f.store.Load(); saved == nil || saved.Connections["mine"].Service != "docs-a" {
		t.Error("the connection was not saved")
	}
}

func TestSaveConnectionSetupConflict(t *testing.T) {
	f := newSetupFixture(t)
	d := ConnectionDraft{
		Provider: "docs", Service: "docs-a", NewCredential: true, Credential: "fresh",
		Storage: config.CredentialTypeKeyring, Name: "mine",
	}
	cand, err := BuildConnectionCandidate(f.cfg, d, StageRights)
	if err != nil {
		t.Fatal(err)
	}
	other := f.cfg.Clone()
	if err := other.SetService("theirs", config.Service{Provider: "docs", BaseURL: "https://theirs.example"}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SaveIfUnchanged(other, f.rev); err != nil {
		t.Fatal(err)
	}
	secrets := map[string]string{"key-id": "id-1", "key-secret": "sec-2"}
	if _, err := f.svc.SaveConnectionSetup(f.cfg, cand, f.rev, secrets, nil); !errors.Is(err, config.ErrConflict) {
		t.Fatalf("SaveConnectionSetup() = %v, want a conflict", err)
	}
	if len(f.secrets.entries) != 0 {
		t.Errorf("a conflict left secrets behind: %v", f.secrets.entries)
	}
	cand, err = BuildConnectionCandidate(f.cfg, reuse(), StageRights)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SaveConnectionSetup(f.cfg, cand, f.rev, nil, nil); !errors.Is(err, config.ErrConflict) {
		t.Fatalf("SaveConnectionSetup() without secrets = %v, want a conflict", err)
	}
	saved, _ := f.store.Load()
	if _, ok := saved.Services["theirs"]; !ok {
		t.Error("the other change was lost")
	}
}

func TestSaveConnectionSetupRefusesForeignCredential(t *testing.T) {
	f := newSetupFixture(t)
	cand, err := BuildConnectionCandidate(f.cfg, reuse(), StageRights)
	if err != nil {
		t.Fatal(err)
	}
	conn := cand.Config.Connections["mine"]
	conn.Credential = "mail-key"
	cand.Config.Connections["mine"] = conn
	var unknown *UnknownEntryError
	if _, err := f.svc.SaveConnectionSetup(f.cfg, cand, f.rev, nil, nil); !errors.As(err, &unknown) {
		t.Fatalf("SaveConnectionSetup() = %v, want the credential refused", err)
	}
	if saved, _ := f.store.Load(); saved != nil {
		if _, ok := saved.Connections["mine"]; ok {
			t.Error("the connection was saved")
		}
	}
}

func TestBuildConnectionCandidateKeepsTargetLabelsOfItsTargets(t *testing.T) {
	d := reuse()
	d.Targets = []string{"t1"}
	d.TargetLabels = map[string]string{"t1": " Projects ", "gone": "Old"}
	cand, err := BuildConnectionCandidate(setupConfig(t), d, StageRights)
	if err != nil {
		t.Fatalf("BuildConnectionCandidate() = %v", err)
	}
	got := cand.Config.Connections["mine"].TargetLabels
	if len(got) != 1 || got["t1"] != "Projects" {
		t.Errorf("labels = %v, want only the label of t1, normalized", got)
	}
	d.TargetLabels["t1"] = "changed"
	if cand.Config.Connections["mine"].TargetLabels["t1"] != "Projects" {
		t.Error("the candidate shares the draft's label map")
	}
}

type excludeCatalog struct{}

var excludeProviders = []config.ProviderMetadata{{
	ID: "ex", SecretRoles: []config.SecretRole{{Name: "token"}},
	Target: config.TargetMetadata{Label: "repository", Required: true, Multiple: true, Wildcard: "*",
		Kinds:    []config.TargetKind{{Name: "repo", Forms: []string{"repos/OWNER/REPO", "repos/OWNER/*"}, Excludable: true}},
		Validate: func(string) error { return nil }},
}}

func (excludeCatalog) ProviderMetadata(id string) (config.ProviderMetadata, bool) {
	if id == "ex" {
		return excludeProviders[0], true
	}
	return config.ProviderMetadata{}, false
}

func (excludeCatalog) ProviderMetadataAll() []config.ProviderMetadata { return excludeProviders }

func TestBuildConnectionCandidateKeepsExclude(t *testing.T) {
	cfg := config.New(excludeCatalog{})
	if err := cfg.SetService("svc", config.Service{Provider: "ex", BaseURL: "https://ex.example"}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetCredential("key", config.Credential{Provider: "ex", Type: config.CredentialTypeKeyring}); err != nil {
		t.Fatal(err)
	}
	draft := ConnectionDraft{Provider: "ex", Service: "svc", Credential: "key", Name: "mine",
		Targets: []string{"repos/o/*"}, Exclude: []string{"repos/o/b"}}
	cand, err := BuildConnectionCandidate(cfg, draft, StageRights)
	if err != nil {
		t.Fatalf("BuildConnectionCandidate() = %v", err)
	}
	if got := cand.Config.Connections["mine"].Exclude; !slices.Equal(got, []string{"repos/o/b"}) {
		t.Errorf("candidate exclude = %v, want the draft's", got)
	}
	draft.Exclude[0] = "changed"
	if got := cand.Config.Connections["mine"].Exclude; got[0] != "repos/o/b" {
		t.Error("the candidate shares its exclusions with the draft")
	}
}
