package infomaniakmail

import (
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestDeclaredExclusionFormsMatchTheParser(t *testing.T) {
	meta, ok := registry(t).ProviderMetadata(Provider)
	if !ok {
		t.Fatal("provider is not registered")
	}
	for _, problem := range provider.ExcludeFormProblems(meta.Target, map[string]string{"folder/PATH": "folder/Trash", "folder/PATH/*": "folder/Trash/*", "sender/ADDRESS": "sender/a@example.com"}, []string{}, func(exclude string) error {
		_, err := parseScopeExcluding([]string{"mailbox/me@example.com"}, []string{exclude})
		return err
	}) {
		t.Error(problem)
	}
}
