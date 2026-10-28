package makeapi

import (
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestDeclaredExclusionFormsMatchTheParser(t *testing.T) {
	meta, ok := registry(t).ProviderMetadata(Provider)
	if !ok {
		t.Fatal("provider is not registered")
	}
	for _, problem := range provider.ExcludeFormProblems(meta.Target, map[string]string{"team/TEAM_ID": "team/7", "folder/FOLDER_ID": "folder/6", "scenario/SCENARIO_ID": "scenario/5"}, []string{}, func(exclude string) error {
		targets := []string{"team/1"}
		if exclude == "team/7" {
			targets = []string{"organization/1"}
		}
		_, err := parseScope(targets, exclude)
		return err
	}) {
		t.Error(problem)
	}
}
