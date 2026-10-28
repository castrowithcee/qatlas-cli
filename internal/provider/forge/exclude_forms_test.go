package forge

import (
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestDeclaredExclusionFormsMatchTheParser(t *testing.T) {
	for _, p := range products {
		meta, _ := registry(t).ProviderMetadata(p.id)
		for _, problem := range provider.ExcludeFormProblems(meta.Target, map[string]string{"users/NAME": "users/alice", "orgs/NAME": "orgs/acme", "repos/OWNER/REPO": "repos/acme/app"}, []string{}, func(exclude string) error {
			_, err := parseAllowlist(nil, []string{exclude})
			return err
		}) {
			t.Error(problem)
		}
	}
}
