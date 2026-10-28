package baserow

import (
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestDeclaredExclusionFormsMatchTheParser(t *testing.T) {
	meta, ok := registry(t).ProviderMetadata(Provider)
	if !ok {
		t.Fatal("provider is not registered")
	}
	for _, problem := range provider.ExcludeFormProblems(meta.Target, map[string]string{"database/DATABASE_ID": "database/2", "table/TABLE_ID": "table/3"}, []string{"*"}, func(exclude string) error {
		_, err := parseScope([]string{"table/1"}, exclude)
		return err
	}) {
		t.Error(problem)
	}
}
