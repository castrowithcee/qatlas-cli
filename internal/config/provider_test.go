package config

import (
	"errors"
	"sort"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
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
			{ID: "bookstack.pages.create", Effect: EffectCreate},
			{ID: "bookstack.pages.get", Effect: EffectRead},
			{ID: "bookstack.pages.list", Effect: EffectRead},
		},
		RemovedTools: []string{"bookstack.pages.publish"},
	},
	"lexware": {
		ID: "lexware", Name: "Lexware Office", DefaultBaseURL: "https://api.lexware.io",
		SecretRoles: []SecretRole{{Name: "api-key", Description: "Lexware private API key"}},
		Target:      TargetMetadata{Label: "target", Description: "not used by Lexware"},
	},
	"twenty-crm": {
		ID: "twenty-crm", Name: "Twenty CRM", DefaultBaseURL: "https://api.twenty.com",
		SecretRoles: []SecretRole{{Name: "api-key", Description: "Twenty API key"}},
		Target:      TargetMetadata{Label: "target", Description: "not used by Twenty CRM"},
	},
	"seatable.base": {
		ID: "seatable.base", Name: "SeaTable", DefaultBaseURL: "https://cloud.seatable.io",
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
	"github": {
		ID: "github", Name: "GitHub", DefaultBaseURL: "https://api.github.com",
		ValidateBaseURL: func(raw string) error {
			if strings.HasPrefix(raw, "https://api.") || strings.HasSuffix(raw, "/api/v3") {
				return nil
			}
			return errors.New("a GitHub base URL must be an API host or end in /api/v3")
		},
		SecretRoles: []SecretRole{{Name: "token", Description: "GitHub personal access token"}},
		Target: TargetMetadata{Label: "project or repository", Required: true, Multiple: true,
			Validate: func(target string) error {
				if strings.HasPrefix(target, "repos/") || strings.HasPrefix(target, "orgs/") {
					return nil
				}
				return errors.New("a GitHub target must name a project or a repository")
			},
			ValidateSet: func(targets []string) error {
				if len(targets) > 1 && !strings.HasPrefix(targets[0], "orgs/") {
					return errors.New("several GitHub targets must start with one project")
				}
				return nil
			}},
		Tools: []ToolMetadata{
			{ID: "github.actions-permissions.get", Effect: EffectRead, RequiresToolAllowList: true},
			{ID: "github.actions-permissions.update", Effect: EffectUpdate, RequiresToolAllowList: true},
			{ID: "github.comments.create", Effect: EffectCreate},
			{ID: "github.comments.list", Effect: EffectRead},
			{ID: "github.issues.close", Effect: EffectUpdate},
			{ID: "github.issues.create", Effect: EffectCreate},
			{ID: "github.issues.get", Effect: EffectRead},
			{ID: "github.issues.list", Effect: EffectRead},
			{ID: "github.issues.reopen", Effect: EffectUpdate},
			{ID: "github.issues.update", Effect: EffectUpdate},
			{ID: "github.project-drafts.create", Effect: EffectCreate},
			{ID: "github.project-issues.create", Effect: EffectCreate},
			{ID: "github.project-items.add", Effect: EffectCreate},
			{ID: "github.project-items.archive", Effect: EffectUpdate},
			{ID: "github.project-items.get", Effect: EffectRead},
			{ID: "github.project-items.list", Effect: EffectRead},
			{ID: "github.project-items.update", Effect: EffectUpdate},
			{ID: "github.repository-settings.get", Effect: EffectRead, RequiresToolAllowList: true},
			{ID: "github.repository-settings.update", Effect: EffectUpdate, RequiresToolAllowList: true},
			{ID: "github.workflow-artifacts.list", Effect: EffectRead},
			{ID: "github.workflow-files.create", Effect: EffectCreate, RequiresToolAllowList: true},
			{ID: "github.workflow-files.get", Effect: EffectRead, RequiresToolAllowList: true},
			{ID: "github.workflow-files.list", Effect: EffectRead, RequiresToolAllowList: true},
			{ID: "github.workflow-files.update", Effect: EffectUpdate, RequiresToolAllowList: true},
			{ID: "github.workflow-jobs.get", Effect: EffectRead},
			{ID: "github.workflow-jobs.list", Effect: EffectRead},
			{ID: "github.workflow-jobs.log", Effect: EffectRead},
			{ID: "github.workflow-permissions.get", Effect: EffectRead, RequiresToolAllowList: true},
			{ID: "github.workflow-permissions.update", Effect: EffectUpdate, RequiresToolAllowList: true},
			{ID: "github.workflow-runs.cancel", Effect: EffectExecute},
			{ID: "github.workflow-runs.get", Effect: EffectRead},
			{ID: "github.workflow-runs.list", Effect: EffectRead},
			{ID: "github.workflow-runs.rerun", Effect: EffectExecute},
			{ID: "github.workflow-runs.rerun-failed", Effect: EffectExecute},
			{ID: "github.workflows.disable", Effect: EffectUpdate, RequiresToolAllowList: true},
			{ID: "github.workflows.dispatch", Effect: EffectExecute},
			{ID: "github.workflows.enable", Effect: EffectUpdate, RequiresToolAllowList: true},
			{ID: "github.workflows.get", Effect: EffectRead},
			{ID: "github.workflows.list", Effect: EffectRead},
		},
	},
	"telegram": {
		ID: "telegram", Name: "Telegram", DefaultBaseURL: "https://api.telegram.org",
		SecretRoles: []SecretRole{{Name: "bot-token", Description: "Telegram bot token"}},
		Target:      TargetMetadata{Label: "chat ID", Required: true},
		Tools:       []ToolMetadata{{ID: "telegram.messages.send", Effect: EffectCreate}},
	},
	"todoist": {
		ID: "todoist", Name: "Todoist", DefaultBaseURL: "https://api.todoist.com/api/v1",
		SecretRoles: []SecretRole{{Name: "token", Description: "Todoist personal API token"}},
		Target: TargetMetadata{Label: "project", Required: true, Multiple: true, Wildcard: "*",
			Description: "project IDs or the whole account"},
		Tools: []ToolMetadata{
			{ID: "todoist.comments.create", Effect: EffectCreate},
			{ID: "todoist.comments.delete", Effect: EffectDelete},
			{ID: "todoist.comments.list", Effect: EffectRead},
			{ID: "todoist.comments.update", Effect: EffectUpdate},
			{ID: "todoist.completed-tasks.list", Effect: EffectRead},
			{ID: "todoist.filters.list", Effect: EffectRead},
			{ID: "todoist.labels.create", Effect: EffectCreate},
			{ID: "todoist.labels.delete", Effect: EffectDelete, RequiresToolAllowList: true},
			{ID: "todoist.labels.list", Effect: EffectRead},
			{ID: "todoist.labels.reorder", Effect: EffectUpdate},
			{ID: "todoist.labels.update", Effect: EffectUpdate},
			{ID: "todoist.projects.archive", Effect: EffectUpdate},
			{ID: "todoist.projects.create", Effect: EffectCreate},
			{ID: "todoist.projects.delete", Effect: EffectDelete, RequiresToolAllowList: true},
			{ID: "todoist.projects.get", Effect: EffectRead},
			{ID: "todoist.projects.list", Effect: EffectRead},
			{ID: "todoist.projects.unarchive", Effect: EffectUpdate},
			{ID: "todoist.projects.update", Effect: EffectUpdate},
			{ID: "todoist.reminders.create", Effect: EffectCreate},
			{ID: "todoist.reminders.delete", Effect: EffectDelete},
			{ID: "todoist.reminders.get", Effect: EffectRead},
			{ID: "todoist.reminders.list", Effect: EffectRead},
			{ID: "todoist.reminders.update", Effect: EffectUpdate},
			{ID: "todoist.sections.create", Effect: EffectCreate},
			{ID: "todoist.sections.delete", Effect: EffectDelete, RequiresToolAllowList: true},
			{ID: "todoist.sections.get", Effect: EffectRead},
			{ID: "todoist.sections.list", Effect: EffectRead},
			{ID: "todoist.sections.move", Effect: EffectUpdate},
			{ID: "todoist.sections.reorder", Effect: EffectUpdate},
			{ID: "todoist.sections.update", Effect: EffectUpdate},
			{ID: "todoist.tasks.close", Effect: EffectUpdate},
			{ID: "todoist.tasks.create", Effect: EffectCreate},
			{ID: "todoist.tasks.delete", Effect: EffectDelete},
			{ID: "todoist.tasks.filter", Effect: EffectRead},
			{ID: "todoist.tasks.get", Effect: EffectRead},
			{ID: "todoist.tasks.list", Effect: EffectRead},
			{ID: "todoist.tasks.move", Effect: EffectUpdate},
			{ID: "todoist.tasks.reopen", Effect: EffectUpdate},
			{ID: "todoist.tasks.update", Effect: EffectUpdate},
		},
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

	seatable := strings.Replace(prefix, "PROVIDER", "seatable.base", 1)
	cfg, err := Decode(strings.NewReader(seatable), testProviders)
	if err != nil {
		t.Fatalf("SeaTable allow-list = %v", err)
	}
	if got := cfg.Connections["route"].TargetValues(); len(got) != 2 || got[0] != "id:0000" || got[1] != "id:0001" {
		t.Fatalf("targets = %v", got)
	}

	for _, provider := range []string{"bookstack", "lexware", "nextcloud", "telegram", "twenty-crm"} {
		input := strings.Replace(prefix, "PROVIDER", provider, 1)
		if _, err := Decode(strings.NewReader(input), testProviders); err == nil ||
			!strings.Contains(err.Error(), "accepts only one") {
			t.Errorf("provider %s allow-list error = %v", provider, err)
		}
	}
}

// A provider that validates its target form refuses a malformed target at load time without quoting it.
func TestProviderTargetValidationRefusesMalformedTargets(t *testing.T) {
	const prefix = `version: 1
services:
  main: {provider: github, base_url: https://api.github.com}
credentials:
  reader: {type: keyring}
connections:
  route:
    service: main
    credential: reader
    target: TARGET
defaults: {}
`
	if _, err := Decode(strings.NewReader(strings.Replace(prefix, "TARGET", "repos/octo-org/example", 1)),
		testProviders); err != nil {
		t.Fatalf("valid target = %v", err)
	}
	const canary = "canary-target-7c1d"
	_, err := Decode(strings.NewReader(strings.Replace(prefix, "TARGET", canary, 1)), testProviders)
	if err == nil || !strings.Contains(err.Error(), "connections.route.target: a GitHub target must name") ||
		strings.Contains(err.Error(), canary) {
		t.Fatalf("malformed target error = %v", err)
	}
}

// A provider that validates its target list as a whole sees it only once every target has a valid form, and
// its refusal names the list without quoting it.
func TestProviderTargetSetValidation(t *testing.T) {
	const prefix = `version: 1
services:
  main: {provider: github, base_url: https://api.github.com}
credentials:
  reader: {type: keyring}
connections:
  route:
    service: main
    credential: reader
    targets: TARGETS
defaults: {}
`
	if _, err := Decode(strings.NewReader(strings.Replace(prefix, "TARGETS", "[orgs/octo-org/projects/7, "+
		"repos/octo-org/example]", 1)), testProviders); err != nil {
		t.Fatalf("valid target list = %v", err)
	}
	_, err := Decode(strings.NewReader(strings.Replace(prefix, "TARGETS", "[repos/octo-org/canary-set-3f1a, "+
		"orgs/octo-org/projects/7]", 1)), testProviders)
	if err == nil || !strings.Contains(err.Error(), "connections.route.targets: several GitHub targets") ||
		strings.Contains(err.Error(), "canary-set-3f1a") {
		t.Fatalf("refused target list error = %v", err)
	}
	_, err = Decode(strings.NewReader(strings.Replace(prefix, "TARGETS", "[orgs/octo-org/projects/7, bad]", 1)),
		testProviders)
	if err == nil || strings.Contains(err.Error(), "several GitHub targets") ||
		!strings.Contains(err.Error(), "must name a project or a repository") {
		t.Fatalf("malformed target list error = %v, want only the form refusal", err)
	}
}

// A connection's targets list gets one path per entry, so several bad entries in one list are all visible at
// once and each points at the entry that caused it. A single target keeps the plain target path.
func TestTargetListErrorsNameTheIndex(t *testing.T) {
	const document = `version: 1
services:
  main: {provider: github, base_url: https://api.github.com}
credentials:
  reader: {type: keyring}
connections:
  route:
    service: main
    credential: reader
    targets: ["repos/octo-org/example", "", "repos/octo-org/example", "bad"]
defaults: {}
`
	_, err := Decode(strings.NewReader(document), testProviders)
	if err == nil {
		t.Fatal("Decode() = nil, want an error")
	}
	for _, want := range []string{
		"connections.route.targets[1]: targets must not be empty",
		"connections.route.targets[2]: a target is listed more than once",
		"connections.route.targets[3]: a GitHub target must name",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q:\n%s", want, err)
		}
	}
	if strings.Contains(err.Error(), "connections.route.target:") {
		t.Errorf("a list error still used the singular target path:\n%s", err)
	}
}

// A provider's ValidateBaseURL runs against the effective base URL, default included, and its refusal is
// reported under the service, not the connection.
func TestProviderMetadataValidatesTheServiceBaseURL(t *testing.T) {
	const document = `version: 1
services:
  main:
    provider: github
    base_url: BASEURL
credentials:
  reader: {type: keyring}
connections:
  route: {service: main, credential: reader, target: repos/octo-org/example}
defaults: {}
`
	if _, err := Decode(strings.NewReader(strings.Replace(document, "BASEURL", "https://api.github.com", 1)),
		testProviders); err != nil {
		t.Fatalf("valid base_url = %v", err)
	}
	_, err := Decode(strings.NewReader(strings.Replace(document, "BASEURL", "https://github.example.com", 1)),
		testProviders)
	if err == nil || !strings.Contains(err.Error(), "services.main.base_url: a GitHub base URL must be an API host") {
		t.Fatalf("invalid base_url error = %v", err)
	}
}

func TestWildcardMustBeAnExplicitExclusiveSeaTableTarget(t *testing.T) {
	input := `version: 1
services:
  main: {provider: seatable.base, base_url: https://cloud.seatable.io}
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
	if got := New(testProviders).SecretRoles(); len(got) != 8 || got[0] != "api-key" ||
		got[2] != "app-password" || got[4] != "token" || got[7] != "user-id" {
		t.Fatalf("secret roles = %v", got)
	}
}

// A service may leave base_url out when its provider declares a default endpoint. The default applies where
// the endpoint is read and is never written into the file; a provider without a default still needs one.
func TestAnOmittedBaseURLTakesTheProviderDefault(t *testing.T) {
	const prefix = `version: 1
services:
  main: {provider: PROVIDER}
  enterprise: {provider: github, base_url: https://ghe.example.invalid/api/v3}
credentials:
  reader: {type: keyring}
connections:
  route: {service: main, credential: reader, target: TARGET}
  other: {service: enterprise, credential: reader, target: repos/octo-org/example}
defaults: {}
`
	input := strings.NewReplacer("PROVIDER", "github", "TARGET", "repos/octo-org/example").Replace(prefix)
	cfg, err := Decode(strings.NewReader(input), testProviders)
	if err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	if got := cfg.Services["main"].BaseURL; got != "" {
		t.Errorf("stored base_url = %q, want it left out", got)
	}
	for connection, want := range map[string]string{
		"route": "https://api.github.com", "other": "https://ghe.example.invalid/api/v3",
	} {
		resolved, err := cfg.Resolve(connection, "")
		if err != nil {
			t.Fatalf("Resolve(%s) = %v", connection, err)
		}
		if resolved.BaseURL != want {
			t.Errorf("Resolve(%s).BaseURL = %q, want %q", connection, resolved.BaseURL, want)
		}
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal() = %v", err)
	}
	if strings.Count(string(data), "base_url") != 1 {
		t.Errorf("saved configuration writes the default:\n%s", data)
	}

	for _, provider := range []string{"bookstack", "nextcloud"} {
		input := strings.NewReplacer("PROVIDER", provider, "TARGET", "books").Replace(prefix)
		if _, err := Decode(strings.NewReader(input), testProviders); err == nil ||
			!strings.Contains(err.Error(), "services.main.base_url: must not be empty") {
			t.Errorf("provider %s without base_url error = %v", provider, err)
		}
	}
}

// ValidateSingle checks only the single target field; the targets list keeps using Validate.
func TestValidateSingleChecksOnlyTheSingleTargetField(t *testing.T) {
	metadata := testProviders.(testProviderCatalog)["github"]
	metadata.Target.ValidateSingle = func(string) error { return errors.New("single refused") }
	catalog := testProviderCatalog{"github": metadata}
	const document = `version: 1
services:
  main: {provider: github, base_url: https://api.github.com}
credentials:
  reader: {type: keyring}
connections:
  route:
    service: main
    credential: reader
    FIELD
defaults: {}
`
	if _, err := Decode(strings.NewReader(strings.Replace(document, "FIELD", "target: repos/o/r", 1)), catalog); err == nil ||
		!strings.Contains(err.Error(), "connections.route.target: single refused") {
		t.Errorf("single field error = %v", err)
	}
	if _, err := Decode(strings.NewReader(strings.Replace(document, "FIELD", "targets: [repos/o/r]", 1)), catalog); err != nil {
		t.Errorf("targets list = %v", err)
	}
}
