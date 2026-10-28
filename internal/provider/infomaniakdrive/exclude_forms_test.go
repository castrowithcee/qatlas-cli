package infomaniakdrive

import (
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestDeclaredExclusionFormsMatchTheParser(t *testing.T) {
	meta, ok := registry(t).ProviderMetadata(Provider)
	if !ok {
		t.Fatal("provider is not registered")
	}
	for _, problem := range provider.ExcludeFormProblems(meta.Target, map[string]string{"drive/DRIVE_ID": "drive/2", "drive/DRIVE_ID/files/FILE_ID": "drive/2/files/5"}, []string{}, func(exclude string) error {
		_, err := parseScope([]string{"account/1"}, []string{exclude})
		return err
	}) {
		t.Error(problem)
	}
}
