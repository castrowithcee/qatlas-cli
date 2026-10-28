package excalidrawplus

import (
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestDeclaredExclusionFormsMatchTheParser(t *testing.T) {
	meta, ok := registry(t).ProviderMetadata(Provider)
	if !ok {
		t.Fatal("provider is not registered")
	}
	for _, problem := range provider.ExcludeFormProblems(meta.Target, map[string]string{"collection/COLLECTION_ID": "collection/B", "scene/SCENE_ID": "scene/X"}, []string{"*"}, func(exclude string) error {
		_, err := parseScope([]string{"collection/A"}, exclude)
		return err
	}) {
		t.Error(problem)
	}
}
