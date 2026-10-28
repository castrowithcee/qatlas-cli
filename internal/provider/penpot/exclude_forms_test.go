package penpot

import (
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestDeclaredExclusionFormsMatchTheParser(t *testing.T) {
	meta, ok := registry(t).ProviderMetadata(Provider)
	if !ok {
		t.Fatal("provider is not registered")
	}
	const (
		u = "11111111-1111-4111-8111-111111111111"
		v = "22222222-2222-4222-8222-222222222222"
		w = "33333333-3333-4333-8333-333333333333"
	)
	for _, problem := range provider.ExcludeFormProblems(meta.Target, map[string]string{"project/PROJECT_ID": "project/" + v, "file/FILE_ID": "file/" + w}, []string{}, func(exclude string) error {
		_, err := parseScope([]string{"team/" + u}, exclude)
		return err
	}) {
		t.Error(problem)
	}
}
