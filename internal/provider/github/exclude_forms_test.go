package github

import (
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestDeclaredExclusionFormsMatchTheParser(t *testing.T) {
	meta, ok := registry(t).ProviderMetadata(Provider)
	if !ok {
		t.Fatal("provider is not registered")
	}
	for _, problem := range provider.ExcludeFormProblems(meta.Target, map[string]string{"repos/OWNER/REPO": "repos/acme/app", "repos/OWNER/*": "repos/acme/*", "users/LOGIN/projects/NUMBER": "users/alice/projects/1", "orgs/LOGIN/projects/NUMBER": "orgs/acme/projects/1", "users/LOGIN/projects/*": "users/alice/projects/*", "orgs/LOGIN/projects/*": "orgs/acme/projects/*", "users/LOGIN": "users/alice", "orgs/LOGIN": "orgs/acme"}, []string{}, func(exclude string) error {
		_, err := parseAllowlistExcluding(nil, []string{exclude})
		return err
	}) {
		t.Error(problem)
	}
}
