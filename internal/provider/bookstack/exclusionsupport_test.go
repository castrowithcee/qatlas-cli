package bookstack

import (
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

func TestExclusionsAreDeclaredForEveryKind(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	meta, _ := reg.ProviderMetadata(Provider)
	if !meta.Target.Excludes() || len(meta.Target.Kinds) != 4 {
		t.Fatalf("excludes = %t, kinds = %d", meta.Target.Excludes(), len(meta.Target.Kinds))
	}
	for _, kind := range meta.Target.Kinds {
		if !(len(kind.ExcludeForms) > 0) {
			t.Errorf("kind %s is not excludable", kind.Name)
		}
	}
	if !strings.Contains(meta.Target.Description, "excluded") {
		t.Errorf("the target description does not explain exclusions: %s", meta.Target.Description)
	}
}

func TestExcludeConfigurationIsCheckedByTheCore(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	// A provider that declares no excludable kind.
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: "plain", Name: "Plain", DefaultBaseURL: "https://plain.example.com",
		SecretRoles: []config.SecretRole{{Name: "token", Description: "token"}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	document := func(provider, role, targets, excludes string) string {
		return "version: 1\nservices:\n  s:\n    provider: " + provider + "\n    base_url: https://wiki.example.com\n" +
			"credentials:\n  k:\n    provider: " + provider + "\n    type: env\n    values:\n      " + role + ": TEST_TOKEN_ID\n" +
			"connections:\n  c:\n    service: s\n    credential: k\n" + targets + "    exclude: " + excludes + "\n"
	}
	bookstack := func(targets, excludes string) error {
		_, err := config.Decode(strings.NewReader(document(Provider, roleTokenID+": TEST_TOKEN_ID\n      "+roleTokenSecret,
			targets, excludes)), reg)
		return err
	}
	for _, c := range [][2]string{
		{"", "[shelf/3, book/4, chapter/5, page/6]"},
		{"    targets: [book/7]\n", "[chapter/5, page/6]"},
		{"    targets: [shelf/3]\n", "[book/4]"},
	} {
		if err := bookstack(c[0], c[1]); err != nil {
			t.Errorf("targets %q exclude %s: %v", c[0], c[1], err)
		}
	}
	for _, c := range [][2]string{
		{"    targets: [book/7]\n", "[book/7]"},
		{"", "[book/4, book/4]"},
		{"", "[chapter/*]"},
		{"", "[user/4]"},
	} {
		if err := bookstack(c[0], c[1]); err == nil {
			t.Errorf("targets %q exclude %s were accepted", c[0], c[1])
		}
	}
	// A provider that declares no exclusions keeps refusing them.
	_, err := config.Decode(strings.NewReader(document("plain", "token", "", "[x]")), reg)
	if err == nil || !strings.Contains(err.Error(), "exclude") {
		t.Errorf("exclusions of a provider without support: err = %v", err)
	}
}
