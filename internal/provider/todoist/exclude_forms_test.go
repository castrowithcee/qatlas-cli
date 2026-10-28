package todoist

import (
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestDeclaredExclusionFormsMatchTheParser(t *testing.T) {
	meta, ok := registry(t).ProviderMetadata(Provider)
	if !ok {
		t.Fatal("provider is not registered")
	}
	for _, problem := range provider.ExcludeFormProblems(meta.Target, map[string]string{projectForm: "123", treeForm: "project/123", sectionForm: "section/456"}, []string{"*"}, func(exclude string) error {
		_, err := parseScopeExcluding([]string{"*"}, []string{exclude})
		return err
	}) {
		t.Error(problem)
	}
}
