package matomo

import (
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestDeclaredExclusionFormsMatchTheParser(t *testing.T) {
	meta, ok := registry(t).ProviderMetadata(Provider)
	if !ok {
		t.Fatal("provider is not registered")
	}
	for _, problem := range provider.ExcludeFormProblems(meta.Target, map[string]string{"site/ID": "site/7"}, []string{"site"}, func(exclude string) error {
		_, err := parseScope([]string{"site/2"}, exclude)
		return err
	}) {
		t.Error(problem)
	}
}
