package config

import (
	"sort"
	"strings"
	"testing"
)

type testProviderCatalog map[string]ProviderMetadata

func (p testProviderCatalog) ProviderMetadata(id string) (ProviderMetadata, bool) {
	metadata, ok := p[id]
	return metadata, ok
}

func (p testProviderCatalog) ProviderMetadataAll() []ProviderMetadata {
	all := make([]ProviderMetadata, 0, len(p))
	for _, metadata := range p {
		all = append(all, metadata)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	return all
}

var testProviders ProviderCatalog = testProviderCatalog{
	"bookstack": {
		ID: "bookstack", Name: "BookStack",
		SecretRoles: []SecretRole{
			{Name: "token-id", Description: "BookStack token ID: the value labeled Token ID when you create an API token"},
			{Name: "token-secret", Description: "BookStack token secret: the value labeled Token Secret when you create the same API token"},
		},
		Target: TargetMetadata{Label: "target"},
		Tools: []ToolMetadata{
			{ID: "bookstack.pages.create", Effect: PermissionCreate},
			{ID: "bookstack.pages.get", Effect: PermissionRead},
			{ID: "bookstack.pages.list", Effect: PermissionRead},
		},
	},
	"lexware": {
		ID: "lexware", Name: "Lexware Office", DefaultBaseURL: "https://api.lexware.io",
		SecretRoles: []SecretRole{{Name: "api-key", Description: "Lexware private API key"}},
		Target:      TargetMetadata{Label: "target", Description: "not used by Lexware"},
	},
	"twentycrm": {
		ID: "twentycrm", Name: "Twenty CRM", DefaultBaseURL: "https://api.twenty.com",
		SecretRoles: []SecretRole{{Name: "api-key", Description: "Twenty API key"}},
		Target:      TargetMetadata{Label: "target", Description: "not used by Twenty CRM"},
	},
	"seatable": {
		ID: "seatable", Name: "SeaTable", DefaultBaseURL: "https://cloud.seatable.io",
		SecretRoles: []SecretRole{{Name: "api-token", Description: "SeaTable API token of one base"}},
		Target: TargetMetadata{
			Label: "table", Required: true, Multiple: true, Wildcard: "*",
			Description: "fixed tables, optionally with a view",
		},
	},
	"nextcloud": {
		ID: "nextcloud", Name: "Nextcloud",
		SecretRoles: []SecretRole{
			{Name: "user-id", Description: "Nextcloud user ID of the identity to read as"},
			{Name: "app-password", Description: "Nextcloud app password of the same identity"},
		},
		Target: TargetMetadata{
			Label: "root folder", Required: true, Description: "fixed folder below the Files of this identity",
		},
	},
	"telegram": {
		ID: "telegram", Name: "Telegram", DefaultBaseURL: "https://api.telegram.org",
		SecretRoles: []SecretRole{{Name: "bot-token", Description: "Telegram bot token"}},
		Target:      TargetMetadata{Label: "chat ID", Required: true},
		Tools:       []ToolMetadata{{ID: "telegram.messages.send", Effect: PermissionCreate}},
	},
}

func TestOnlyProvidersThatDeclareMultipleTargetsAcceptAnAllowList(t *testing.T) {
	const prefix = `version: 1
services:
  main:
    provider: PROVIDER
    base_url: https://example.invalid
credentials:
  reader:
    type: keyring
connections:
  route:
    service: main
    credential: reader
    targets: [id:0000, id:0001]
defaults: {}
`

	seatable := strings.Replace(prefix, "PROVIDER", "seatable", 1)
	cfg, err := Decode(strings.NewReader(seatable), testProviders)
	if err != nil {
		t.Fatalf("SeaTable allow-list = %v", err)
	}
	if got := cfg.Connections["route"].TargetValues(); len(got) != 2 || got[0] != "id:0000" || got[1] != "id:0001" {
		t.Fatalf("targets = %v", got)
	}

	for _, provider := range []string{"bookstack", "lexware", "nextcloud", "telegram", "twentycrm"} {
		input := strings.Replace(prefix, "PROVIDER", provider, 1)
		if _, err := Decode(strings.NewReader(input), testProviders); err == nil ||
			!strings.Contains(err.Error(), "accepts only one") {
			t.Errorf("provider %s allow-list error = %v", provider, err)
		}
	}
}

func TestWildcardMustBeAnExplicitExclusiveSeaTableTarget(t *testing.T) {
	input := `version: 1
services:
  main: {provider: seatable, base_url: https://cloud.seatable.io}
credentials:
  reader: {type: keyring}
connections:
  route:
    service: main
    credential: reader
    targets: ["*", id:0000]
defaults: {}
`
	if _, err := Decode(strings.NewReader(input), testProviders); err == nil ||
		!strings.Contains(err.Error(), "must be the only target") {
		t.Fatalf("mixed wildcard error = %v", err)
	}
}

func TestProviderMetadataValidatesTelegramTargets(t *testing.T) {
	const valid = `version: 1
services:
  telegram-main:
    provider: telegram
    base_url: https://api.telegram.org
credentials:
  notifier:
    type: env
    values:
      bot-token: QATLAS_TELEGRAM_TOKEN
connections:
  operations:
    service: telegram-main
    credential: notifier
    target: "-1001111111111"
  alerts:
    service: telegram-main
    credential: notifier
    target: "-1002222222222"
defaults:
  connections:
    telegram: operations
`
	cfg, err := Decode(strings.NewReader(valid), testProviders)
	if err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	if cfg.Connections["operations"].Target == cfg.Connections["alerts"].Target {
		t.Fatal("two Telegram connections lost their distinct targets")
	}
	if got := cfg.ProviderSecretRoles("telegram"); len(got) != 1 || got[0] != "bot-token" {
		t.Fatalf("Telegram roles = %v, want bot-token", got)
	}

	missing := strings.Replace(valid, `    target: "-1001111111111"`, `    target: ""`, 1)
	if _, err := Decode(strings.NewReader(missing), testProviders); err == nil ||
		!strings.Contains(err.Error(), `provider "telegram" requires chat ID`) {
		t.Fatalf("Decode() error = %v, want required Telegram target", err)
	}
}

func TestProviderMetadataNeverTreatsTargetAsASecret(t *testing.T) {
	metadata, ok := testProviders.ProviderMetadata("telegram")
	if !ok || !metadata.Target.Required || metadata.Target.Label != "chat ID" {
		t.Fatalf("Telegram metadata = %+v, %v", metadata, ok)
	}
	if got := New(testProviders).SecretRoles(); len(got) != 7 || got[0] != "api-key" ||
		got[2] != "app-password" || got[6] != "user-id" {
		t.Fatalf("secret roles = %v", got)
	}
}
