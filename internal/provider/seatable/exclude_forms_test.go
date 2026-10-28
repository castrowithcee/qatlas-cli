package seatable

import (
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestDeclaredExclusionFormsMatchTheParser(t *testing.T) {
	meta, ok := registry(t).ProviderMetadata(Provider)
	if !ok {
		t.Fatal("provider is not registered")
	}
	for _, problem := range provider.ExcludeFormProblems(meta.Target, map[string]string{"TABLE": "orders", "TABLE/VIEW": "orders/Grid"}, []string{"*"}, func(exclude string) error {
		_, err := parseExcludes(scope{}, []string{exclude})
		return err
	}) {
		t.Error(problem)
	}
}
