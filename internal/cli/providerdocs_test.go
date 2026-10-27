package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// Every registered provider has a user page the editor links (config.ProviderMetadata.DocPath), and the page
// exists in the repository; every provider folder belongs to a registered provider.
func TestEveryProviderHasItsLinkedDocPage(t *testing.T) {
	root := filepath.Join("..", "..", "docs", "providers")
	linked := map[string]bool{}
	for _, metadata := range defaultRegistry().ProviderMetadataAll() {
		path := metadata.DocPath()
		linked[filepath.Dir(path)] = true
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			t.Errorf("provider %q links %s: %v", metadata.ID, path, err)
		}
		// setup.md is the target as long as it exists.
		if _, err := os.Stat(filepath.Join(root, filepath.Dir(path), "setup.md")); err == nil && filepath.Base(path) != "setup.md" {
			t.Errorf("provider %q links %s although setup.md exists", metadata.ID, path)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() && !linked[e.Name()] {
			t.Errorf("docs/providers/%s belongs to no registered provider", e.Name())
		}
	}
}
