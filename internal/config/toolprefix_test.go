package config

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// prefixCatalog has a dotted provider ID, a listed-only tool, a segment that merely starts like another
// ("iam" and "iamx"), and a second provider.
func prefixCatalog() testProviderCatalog {
	return testProviderCatalog{
		"acme.cloud": {
			ID: "acme.cloud", Name: "Acme Cloud",
			SecretRoles: []SecretRole{{Name: "token", Description: "token"}},
			Target:      TargetMetadata{Label: "target"},
			Tools: []ToolMetadata{
				{ID: "acme.cloud.iam.users.create", Effect: EffectCreate},
				{ID: "acme.cloud.iam.users.get", Effect: EffectRead},
				{ID: "acme.cloud.iam.policies.update", Effect: EffectUpdate, RequiresToolAllowList: true},
				{ID: "acme.cloud.iamx.things.get", Effect: EffectRead},
				{ID: "acme.cloud.storage.buckets.get", Effect: EffectRead},
				{ID: "acme.cloud.guarded.things.get", Effect: EffectRead, RequiresToolAllowList: true},
			},
			FormerIDs: []string{"acmecloud"},
		},
		"other": {
			ID: "other", Name: "Other",
			SecretRoles: []SecretRole{{Name: "token", Description: "token"}},
			Target:      TargetMetadata{Label: "target"},
			Tools:       []ToolMetadata{{ID: "other.pages.get", Effect: EffectRead}},
		},
	}
}

func prefixConfig(t *testing.T, catalog ProviderCatalog, tools []string) *Config {
	t.Helper()
	cfg := New(catalog)
	cfg.Services = map[string]Service{"cloud": {Provider: "acme.cloud", BaseURL: "https://cloud.example.invalid"}}
	cfg.Credentials = map[string]Credential{
		"key": {Type: CredentialTypeEnv, Values: map[string]string{"token": "CLOUD_TOKEN"}}}
	cfg.Connections = map[string]Connection{"cloud": {Service: "cloud", Credential: "key", Tools: tools}}
	return cfg
}

func TestToolPrefixGrammarAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tools []string
		code  string // "" is valid
		field string
	}{
		{"service prefix", []string{"acme.cloud.iam.*"}, "", ""},
		{"object prefix", []string{"acme.cloud.iam.users.*"}, "", ""},
		{"prefix and an exact tool of it", []string{"acme.cloud.iam.*", "acme.cloud.iam.users.get"}, "", ""},
		{"prefix and the exact listed-only tool", []string{"acme.cloud.iam.*", "acme.cloud.iam.policies.update"}, "", ""},
		{"provider only", []string{"acme.cloud.*"}, CodeInvalidToolPrefix, "tools[1]"},
		{"star alone", []string{"*"}, CodeInvalidToolPrefix, "tools[1]"},
		{"partial segment", []string{"acme.cloud.ia*"}, CodeInvalidToolPrefix, "tools[1]"},
		{"star in the middle", []string{"acme.cloud.*.get"}, CodeInvalidToolPrefix, "tools[1]"},
		{"two stars", []string{"acme.cloud.*.*"}, CodeInvalidToolPrefix, "tools[1]"},
		{"shorter than the provider", []string{"acme.*"}, CodeInvalidToolPrefix, "tools[1]"},
		{"empty segment", []string{"acme.cloud..*"}, CodeInvalidToolPrefix, "tools[1]"},
		{"former provider ID", []string{"acmecloud.iam.*"}, CodeInvalidToolPrefix, "tools[1]"},
		{"no tool below it", []string{"acme.cloud.nothing.*"}, CodeToolPrefixNoMatch, "tools[1]"},
		{"only a listed-only tool below it", []string{"acme.cloud.guarded.*"}, CodeToolPrefixNoMatch, "tools[1]"},
		{"prefix of another provider", []string{"other.pages.*"}, CodeToolOfOtherProvider, "tools"},
		{"provider prefix of another provider", []string{"other.*"}, CodeToolOfOtherProvider, "tools"},
		{"duplicate prefix", []string{"acme.cloud.iam.*", "acme.cloud.iam.*"}, CodeDuplicateTool, "tools"},
		{"empty list", []string{}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := prefixConfig(t, prefixCatalog(), tc.tools).Validate()
			if tc.code == "" {
				if err != nil {
					t.Fatalf("Validate() = %v", err)
				}
				return
			}
			var p Problem
			if !errors.As(err, &p) || p.Code != tc.code {
				t.Fatalf("Validate() = %v, want code %s", err, tc.code)
			}
			if p.Hint() == "" {
				t.Errorf("code %s has no hint", tc.code)
			}
			if tc.code == CodeInvalidToolPrefix || tc.code == CodeToolPrefixNoMatch {
				if strings.Contains(err.Error(), tc.tools[0]) {
					t.Errorf("an entry that is no registered ID is quoted: %v", err)
				}
			}
		})
	}
}

func TestToolPrefixWithEffectsIsRefused(t *testing.T) {
	cfg := prefixConfig(t, prefixCatalog(), []string{"acme.cloud.iam.*"})
	conn := cfg.Connections["cloud"]
	conn.Effects = []Effect{EffectRead}
	cfg.Connections["cloud"] = conn
	var p Problem
	if err := cfg.Validate(); !errors.As(err, &p) || p.Code != CodeEffectsAndTools {
		t.Fatalf("Validate() = %v, want %s", err, CodeEffectsAndTools)
	}
}

func TestToolPrefixDecodesFromYAMLAndKeepsDottedProviderBoundary(t *testing.T) {
	cfg := prefixConfig(t, prefixCatalog(), []string{"acme.cloud.iam.*"})
	// The segment border: "iam" never covers "iamx".
	for id, want := range map[string]bool{
		"acme.cloud.iam.users.create":    true,
		"acme.cloud.iam.users.get":       true,
		"acme.cloud.iam.policies.update": false, // listed-only: never by a prefix
		"acme.cloud.iamx.things.get":     false,
		"acme.cloud.storage.buckets.get": false,
	} {
		tool := toolOf(t, prefixCatalog(), id)
		if got := cfg.ConnectionAllows("cloud", tool, time.Time{}); got != want {
			t.Errorf("ConnectionAllows(%s) = %t, want %t", id, got, want)
		}
	}
}

func toolOf(t *testing.T, catalog testProviderCatalog, id string) ToolMetadata {
	t.Helper()
	for _, metadata := range catalog {
		for _, tool := range metadata.Tools {
			if tool.ID == id {
				return tool
			}
		}
	}
	t.Fatalf("no tool %s", id)
	return ToolMetadata{}
}

func TestToolPrefixListedOnlyNeedsTheExactID(t *testing.T) {
	catalog := prefixCatalog()
	guarded := toolOf(t, catalog, "acme.cloud.iam.policies.update")
	cfg := prefixConfig(t, catalog, []string{"acme.cloud.iam.*"})
	if got := cfg.ConnectionRefusal("cloud", guarded, time.Time{}); got != RefusalToolsList {
		t.Errorf("prefix only: refusal = %q, want %q", got, RefusalToolsList)
	}
	cfg = prefixConfig(t, catalog, []string{"acme.cloud.iam.*", guarded.ID})
	if got := cfg.ConnectionRefusal("cloud", guarded, time.Time{}); got != "" {
		t.Errorf("prefix and exact ID: refusal = %q, want none", got)
	}
	if got := cfg.ConnectionEffects("cloud"); !reflect.DeepEqual(got, []Effect{EffectRead, EffectCreate, EffectUpdate}) {
		t.Errorf("effects = %v", got)
	}
}

func TestToolPrefixEffectsAndFutureTools(t *testing.T) {
	catalog := prefixCatalog()
	cfg := prefixConfig(t, catalog, []string{"acme.cloud.iam.*"})
	if got := cfg.ConnectionEffects("cloud"); !reflect.DeepEqual(got, []Effect{EffectRead, EffectCreate}) {
		t.Errorf("effects = %v, want read and create; the listed-only update tool adds none", got)
	}
	// A tool a later release registers below the prefix is covered at once, with its effect.
	metadata := catalog["acme.cloud"]
	metadata.Tools = append(metadata.Tools, ToolMetadata{ID: "acme.cloud.iam.roles.delete", Effect: EffectDelete})
	catalog["acme.cloud"] = metadata
	if got := cfg.ConnectionEffects("cloud"); !reflect.DeepEqual(got, []Effect{EffectRead, EffectCreate, EffectDelete}) {
		t.Errorf("effects after a new tool = %v", got)
	}
	if !cfg.ConnectionAllows("cloud", ToolMetadata{ID: "acme.cloud.iam.roles.delete", Effect: EffectDelete}, time.Time{}) {
		t.Error("a new tool below the prefix is not covered")
	}
	// An empty list stays empty.
	empty := prefixConfig(t, catalog, []string{})
	if got := empty.ConnectionEffects("cloud"); len(got) != 0 {
		t.Errorf("effects of tools: [] = %v", got)
	}
	if empty.ConnectionAllows("cloud", toolOf(t, catalog, "acme.cloud.iam.users.get"), time.Time{}) {
		t.Error("tools: [] offers a tool")
	}
}

func TestToolPrefixMatchesCount(t *testing.T) {
	metadata := prefixCatalog()["acme.cloud"]
	for entry, want := range map[string]int{
		"acme.cloud.iam.*":         2, // the listed-only update tool is not counted
		"acme.cloud.guarded.*":     0,
		"acme.cloud.*":             0,
		"acme.cloud.iam.users.*":   2,
		"acme.cloud.iam.users.get": 0, // an exact ID is no prefix
	} {
		if got := ToolPrefixMatches(metadata, entry); got != want {
			t.Errorf("ToolPrefixMatches(%q) = %d, want %d", entry, got, want)
		}
	}
}

func TestToolsCoveredCountsToolsNotEntries(t *testing.T) {
	metadata := prefixCatalog()["acme.cloud"]
	// The prefix and the exact ID cover the same tool, which counts once; the listed-only tool counts
	// only when it is named.
	for _, tc := range []struct {
		tools []string
		want  int
	}{
		{nil, 0},
		{[]string{"acme.cloud.iam.*"}, 2},
		{[]string{"acme.cloud.iam.*", "acme.cloud.iam.users.get"}, 2},
		{[]string{"acme.cloud.iam.*", "acme.cloud.iam.policies.update"}, 3},
	} {
		if got := ToolsCovered(metadata, tc.tools); got != tc.want {
			t.Errorf("ToolsCovered(%v) = %d, want %d", tc.tools, got, tc.want)
		}
	}
}
