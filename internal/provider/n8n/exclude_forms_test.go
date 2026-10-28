package n8n

import (
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestDeclaredExclusionFormsMatchTheParser(t *testing.T) {
	meta, ok := registry(t).ProviderMetadata(Provider)
	if !ok {
		t.Fatal("provider is not registered")
	}
	for _, problem := range provider.ExcludeFormProblems(meta.Target, map[string]string{"project/PROJECT_ID": "project/p1", "workflow/WORKFLOW_ID": "workflow/w1"}, []string{}, func(exclude string) error {
		_, err := parseScope(nil, exclude)
		return err
	}) {
		t.Error(problem)
	}
}
