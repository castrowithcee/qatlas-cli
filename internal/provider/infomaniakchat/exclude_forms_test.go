package infomaniakchat

import (
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestDeclaredExclusionFormsMatchTheParser(t *testing.T) {
	meta, ok := registry(t).ProviderMetadata(Provider)
	if !ok {
		t.Fatal("provider is not registered")
	}
	for _, problem := range provider.ExcludeFormProblems(meta.Target, map[string]string{"channel/CHANNEL_ID": "channel/abc123"}, []string{}, func(exclude string) error {
		_, err := parseScope([]string{"team/1"}, exclude)
		return err
	}) {
		t.Error(problem)
	}
}
