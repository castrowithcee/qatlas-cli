package selftest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// syntheticProvider is the provider ID of every fixture. The catalog below stands for it, so the
// measurement needs no real provider.
const syntheticProvider = "synthetic"

type syntheticCatalog struct{}

func (syntheticCatalog) ProviderMetadata(id string) (config.ProviderMetadata, bool) {
	if id != syntheticProvider {
		return config.ProviderMetadata{}, false
	}
	return syntheticMetadata(), true
}

func (syntheticCatalog) ProviderMetadataAll() []config.ProviderMetadata {
	return []config.ProviderMetadata{syntheticMetadata()}
}

func syntheticMetadata() config.ProviderMetadata {
	return config.ProviderMetadata{
		ID: syntheticProvider, Name: "Synthetic",
		SecretRoles: []config.SecretRole{
			{Name: "token-id", Description: "synthetic token ID"},
			{Name: "token-secret", Description: "synthetic token secret"},
		},
		Target: config.TargetMetadata{Label: "target"},
		Tools:  []config.ToolMetadata{{ID: syntheticProvider + ".items.get", Effect: config.EffectRead}},
	}
}

// storageYAML renders a balanced configuration of total entries: one third services, one third
// credentials, the rest connections.
func storageYAML(total int) []byte {
	third := total / 3
	var b strings.Builder
	b.WriteString("version: 1\nservices:\n")
	for i := 0; i < third; i++ {
		fmt.Fprintf(&b, "  svc-%05d:\n    provider: %s\n    base_url: https://wiki-%d.example.invalid\n", i, syntheticProvider, i)
	}
	b.WriteString("credentials:\n")
	for i := 0; i < third; i++ {
		fmt.Fprintf(&b, "  cred-%05d:\n    provider: %s\n    type: env\n    values:\n"+
			"      token-id: SCALE_CRED_%05d_TOKEN_ID\n      token-secret: SCALE_CRED_%05d_TOKEN_SECRET\n",
			i, syntheticProvider, i, i)
	}
	b.WriteString("connections:\n")
	for i := 0; i < total-2*third; i++ {
		fmt.Fprintf(&b, "  conn-%05d:\n    service: svc-%05d\n    credential: cred-%05d\n"+
			"    description: synthetic route %d\n    effects: [read]\n", i, i%third, i%third, i)
	}
	b.WriteString("defaults: {}\n")
	return []byte(b.String())
}

// StorageFixture is a store-canonical synthetic configuration of a given size in its own directory. The
// first save of the measurement then does not take the hand-edit backup path.
type StorageFixture struct {
	dir   string
	path  string
	data  []byte
	store *config.Store
}

// NewStorageFixture creates the fixture with total entries below dir, which must exist.
func NewStorageFixture(dir string, total int) (*StorageFixture, error) {
	catalog := syntheticCatalog{}
	cfg, err := config.Decode(bytes.NewReader(storageYAML(total)), catalog)
	if err != nil {
		return nil, fmt.Errorf("the synthetic configuration does not load: %w", err)
	}
	path := filepath.Join(dir, "qatlas", "config.yaml")
	store := config.NewStore(path, catalog)
	if err := store.Save(cfg); err != nil {
		return nil, fmt.Errorf("the synthetic configuration does not save: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &StorageFixture{dir: dir, path: path, data: data, store: store}, nil
}

// Path is the configuration file of the fixture.
func (f *StorageFixture) Path() string { return f.path }

// Reset puts the file back to its canonical bytes and drops everything a measurement left next to it, so
// every round starts from the same size.
func (f *StorageFixture) Reset() error {
	dir := filepath.Dir(f.path)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(f.path, f.data, 0o600)
}
