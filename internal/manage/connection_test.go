package manage

import (
	"slices"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

func connectionFixture() *config.Config {
	return &config.Config{
		Services: map[string]config.Service{
			"wiki-b": {Provider: "wiki"}, "wiki-a": {Provider: "wiki"}, "mail-a": {Provider: "mail"},
		},
		Credentials: map[string]config.Credential{
			"named":    {Provider: "wiki"},
			"other":    {Provider: "mail"},
			"open":     {},
			"derived":  {},
			"foreign":  {},
			"payload":  {Forward: true},
			"disputed": {},
		},
		Connections: map[string]config.Connection{
			"c1": {Service: "wiki-a", Credential: "derived"},
			"c2": {Service: "mail-a", Credential: "foreign"},
			"c3": {Service: "wiki-a", Credential: "disputed"},
			"c4": {Service: "mail-a", Credential: "disputed"},
		},
	}
}

func TestProviderServices(t *testing.T) {
	cfg := connectionFixture()
	for provider, want := range map[string][]string{
		"wiki": {"wiki-a", "wiki-b"}, "mail": {"mail-a"}, "none": nil,
	} {
		if got := ProviderServices(cfg, provider); !slices.Equal(got, want) {
			t.Errorf("ProviderServices(%q) = %v, want %v", provider, got, want)
		}
	}
}

func TestProviderCredentials(t *testing.T) {
	cfg := connectionFixture()
	tests := []struct {
		provider string
		want     []string
	}{
		// "foreign" is derived to mail, "payload" never serves a connection, "other" names mail.
		{"wiki", []string{"derived", "disputed", "named", "open"}},
		{"mail", []string{"disputed", "foreign", "open", "other"}},
	}
	for _, tt := range tests {
		if got := ProviderCredentials(cfg, tt.provider); !slices.Equal(got, tt.want) {
			t.Errorf("ProviderCredentials(%q) = %v, want %v", tt.provider, got, tt.want)
		}
	}
}

func TestSplitTargets(t *testing.T) {
	tests := []struct {
		in         []string
		wantTarget string
		wantList   []string
	}{
		{nil, "", nil},
		{[]string{"a"}, "a", nil},
		{[]string{"a", "b"}, "", []string{"a", "b"}},
	}
	for _, tt := range tests {
		target, list := SplitTargets(tt.in)
		if target != tt.wantTarget || !slices.Equal(list, tt.wantList) {
			t.Errorf("SplitTargets(%v) = %q, %v", tt.in, target, list)
		}
	}
	in := []string{"a", "b"}
	_, list := SplitTargets(in)
	list[0] = "x"
	if in[0] != "a" {
		t.Error("SplitTargets shares its input")
	}
}

func TestDefaultPermissions(t *testing.T) {
	declared := config.ProviderMetadata{DefaultPermissions: []config.Permission{config.PermissionRead}}
	for name, metadata := range map[string]config.ProviderMetadata{"none": {}, "declared": declared} {
		got := DefaultPermissions(metadata)
		if !slices.Equal(got, []config.Permission{config.PermissionRead}) {
			t.Errorf("%s: DefaultPermissions = %v, want read only", name, got)
		}
	}
}

func TestConnectionDefaults(t *testing.T) {
	cfg := connectionFixture()
	start := ConnectionDefaults(cfg, "wiki")
	if start.Name != "wiki" || start.Storage != cfg.SecretStore() {
		t.Errorf("unexpected start %+v", start)
	}
	if start.ProfileID != "" || start.Permissions != nil || start.Tools != nil {
		t.Errorf("a provider without profiles must start without ticks: %+v", start)
	}
	cfg.Connections["wiki"] = config.Connection{Service: "wiki-a", Credential: "named"}
	if got := ConnectionDefaults(cfg, "wiki").Name; got != "" {
		t.Errorf("a taken name must not be proposed, got %q", got)
	}
}

type profileCatalog struct{}

var profileProvider = config.ProviderMetadata{
	ID:             "docs",
	DefaultBaseURL: "https://docs.example",
	Tools: []config.ToolMetadata{
		{ID: "docs.read", Effect: config.PermissionRead},
		{ID: "docs.write", Effect: config.PermissionUpdate},
	},
	Profiles: []config.ToolProfile{
		{ID: "all", Tools: []string{"docs.read", "docs.write"}},
		{ID: "reader", Recommended: true, Tools: []string{"docs.read"}},
	},
}

func (profileCatalog) ProviderMetadata(id string) (config.ProviderMetadata, bool) {
	return profileProvider, id == "docs"
}

func (profileCatalog) ProviderMetadataAll() []config.ProviderMetadata {
	return []config.ProviderMetadata{profileProvider}
}

func TestConnectionDefaultsProfile(t *testing.T) {
	start := ConnectionDefaults(config.New(profileCatalog{}), "docs")
	if start.ProfileID != "reader" || start.BaseURL != "https://docs.example" {
		t.Fatalf("unexpected start %+v", start)
	}
	if !slices.Equal(start.Permissions, []config.Permission{config.PermissionRead}) ||
		!slices.Equal(start.Tools, []string{"docs.read"}) {
		t.Errorf("the profile must tick only its own tools and their effects: %+v", start)
	}
	for _, p := range start.Permissions {
		if p != config.PermissionRead {
			t.Errorf("unexpected permission %s", p)
		}
	}
}
