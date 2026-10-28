package nextcloud

import (
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestDeclaredExclusionFormsMatchTheParser(t *testing.T) {
	meta, ok := registry(t).ProviderMetadata(Provider)
	if !ok {
		t.Fatal("provider is not registered")
	}
	kinds := []string{"folder", "notes", "calendar", "addressbook", "talk", "deck"}
	accepted := map[string]string{"folder/PATH": "folder/Private", "notes/PATH": "notes/Work",
		"calendar/URI": "calendar/work", "addressbook/URI": "addressbook/book", "talk/TOKEN": "talk/room7",
		"deck/BOARD_ID": "deck/42"}
	for _, problem := range provider.ExcludeFormProblems(meta.Target, accepted, kinds, func(exclude string) error {
		_, err := parseScopeExcluding(kinds, []string{exclude})
		return err
	}) {
		t.Error(problem)
	}
}
