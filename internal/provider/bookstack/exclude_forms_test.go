package bookstack

import (
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestDeclaredExclusionFormsMatchTheParser(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	meta, _ := reg.ProviderMetadata(Provider)
	for _, problem := range provider.ExcludeFormProblems(meta.Target, map[string]string{"shelf/SHELF_ID": "shelf/2", "book/BOOK_ID": "book/3", "chapter/CHAPTER_ID": "chapter/4", "page/PAGE_ID": "page/5"}, []string{}, func(exclude string) error {
		bound, err := parseScope([]string{"book/1"})
		if err != nil {
			return err
		}
		_, err = bound.withExcludes([]string{exclude})
		return err
	}) {
		t.Error(problem)
	}
}
