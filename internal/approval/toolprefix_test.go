package approval

import (
	"context"
	"reflect"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/vault"
)

// A prefix in the tools list selects tools a later release may add. The approval is bound to the effects the
// selected tools derive, so a release that adds a tool of an effect the approval did not cover leaves the
// connection open and names the effect, while a further tool of an approved effect, which widens nothing the
// approval stood for, does not.
func TestToolPrefixGrowthOfAnEffectNeedsANewApproval(t *testing.T) {
	catalog := renameCatalog{"acme.cloud": {
		ID: "acme.cloud", SupportedEffects: config.Effects(),
		Tools: []config.ToolMetadata{
			{ID: "acme.cloud.iam.users.get", Effect: config.EffectRead},
			{ID: "acme.cloud.iam.policies.update", Effect: config.EffectUpdate, RequiresToolAllowList: true},
		},
	}}
	cfg := config.New(catalog)
	cfg.Services["cloud"] = config.Service{Provider: "acme.cloud", BaseURL: "https://cloud.example.test"}
	cfg.Credentials["key"] = config.Credential{Type: config.CredentialTypeVault}
	cfg.Connections["cloud"] = config.Connection{Service: "cloud", Credential: "key", Tools: []string{"acme.cloud.iam.*"}}
	v := vault.New(t.TempDir())
	if err := v.Set("key", "token", "synthetic-token", func(string) (string, error) { return passphrase, nil }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatal(err)
	}
	open := func() Report {
		t.Helper()
		report, err := Pending(cfg, v)
		if err != nil {
			t.Fatal(err)
		}
		return report
	}
	register := func(tool config.ToolMetadata) {
		metadata := catalog["acme.cloud"]
		metadata.Tools = append(append([]config.ToolMetadata(nil), metadata.Tools...), tool)
		catalog["acme.cloud"] = metadata
	}
	if got := open(); len(got.Open) != 0 {
		t.Fatalf("open after approving = %v", openNames(got))
	}

	register(config.ToolMetadata{ID: "acme.cloud.iam.users.list", Effect: config.EffectRead})
	if got := open(); len(got.Open) != 0 {
		t.Errorf("a further tool of an approved effect opened %v", openNames(got))
	}

	register(config.ToolMetadata{ID: "acme.cloud.iam.users.delete", Effect: config.EffectDelete})
	report := open()
	if got := openNames(report); !reflect.DeepEqual(got, []string{"cloud"}) {
		t.Fatalf("open after a tool of a new effect = %v", got)
	}
	f := fieldOf(t, report.Open[0].Fields, FieldEffects)
	if !reflect.DeepEqual(f.Added, []string{"delete"}) || len(f.Removed) != 0 {
		t.Errorf("effects change = %+v, want delete added", f)
	}

	if _, _, err := Approve(context.Background(), cfg, v, []string{"cloud"}); err != nil {
		t.Fatal(err)
	}
	if got := open(); len(got.Open) != 0 {
		t.Errorf("open after approving the growth = %v", openNames(got))
	}
}
