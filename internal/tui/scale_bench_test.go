package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Load profiles for the editor lists. The fixture is synthetic: invented environment variable names, a
// reserved .invalid host, a temporary directory. The sizes are probe points, not limits.
//
//	go test ./internal/tui -run '^$' -bench BenchmarkScale -benchtime 5x -count 3

// scaleUnits are the numbers of services, credentials, and connections each; 333, 1667, and 3333 make a
// total of about 1.000, 5.000, and 10.000 entries.
var scaleUnits = []int{333, 1667, 3333, 1000, 5000, 10000}

func scaleConfigYAML(units int) []byte {
	var b strings.Builder
	b.WriteString("version: 1\nservices:\n")
	for i := 0; i < units; i++ {
		fmt.Fprintf(&b, "  svc-%05d:\n    provider: bookstack\n    base_url: https://wiki-%d.example.invalid\n", i, i)
	}
	b.WriteString("credentials:\n")
	for i := 0; i < units; i++ {
		fmt.Fprintf(&b, "  cred-%05d:\n    provider: bookstack\n    type: env\n    values:\n      token-id: SCALE_%05d_ID\n"+
			"      token-secret: SCALE_%05d_SECRET\n", i, i, i)
	}
	b.WriteString("connections:\n")
	for i := 0; i < units; i++ {
		fmt.Fprintf(&b, "  conn-%05d:\n    service: svc-%05d\n    credential: cred-%05d\n    description: synthetic route %d\n",
			i, i, i, i)
	}
	b.WriteString("defaults: {}\n")
	return []byte(b.String())
}

func scaleStore(tb testing.TB, units int) *config.Store {
	tb.Helper()
	dir := filepath.Join(tb.TempDir(), "qatlas")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		tb.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, scaleConfigYAML(units), 0o600); err != nil {
		tb.Fatal(err)
	}
	catalog := capability.NewRegistry()
	if err := catalog.RegisterProvider(config.ProviderMetadata{
		ID: "bookstack", Name: "BookStack",
		SecretRoles: []config.SecretRole{{Name: "token-id"}, {Name: "token-secret"}},
		Target:      config.TargetMetadata{Label: "target"},
	}, nil); err != nil {
		tb.Fatalf("register test provider: %v", err)
	}
	return config.NewStore(path, catalog)
}

func scaleOpen(tb testing.TB, store *config.Store) *Model {
	tb.Helper()
	secrets := secret.NewWith(func(string) string { return "" }, secret.NewMemoryStore(), nil, nil)
	model, err := buildModel(store, nil, secrets, nil)
	if err != nil {
		tb.Fatalf("New() = %v", err)
	}
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return model
}

func scaleModel(tb testing.TB, units int) *Model { return scaleOpen(tb, scaleStore(tb, units)) }

// BenchmarkScaleOpen builds the editor from the file: load, validate, and the first list.
func BenchmarkScaleOpen(b *testing.B) {
	for _, units := range scaleUnits {
		b.Run(fmt.Sprintf("units-%d", units), func(b *testing.B) {
			store := scaleStore(b, units)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				scaleOpen(b, store)
			}
		})
	}
}

// BenchmarkScaleList measures one section of connections once the editor is open: opening the section,
// rendering the viewport, moving the selection, and typing a filter that narrows the list key by key.
func BenchmarkScaleList(b *testing.B) {
	for _, units := range scaleUnits {
		b.Run(fmt.Sprintf("units-%d", units), func(b *testing.B) {
			model := scaleModel(b, units)
			b.Run("open-section", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					model.openSection(sectionConnections)
				}
			})
			b.Run("view", func(b *testing.B) {
				model.openSection(sectionConnections)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if view := model.View(); view == "" {
						b.Fatal("empty view")
					}
				}
			})
			b.Run("move-and-view", func(b *testing.B) {
				model.openSection(sectionConnections)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					model.Update(tea.KeyMsg{Type: tea.KeyDown})
					model.View()
				}
			})
			b.Run("filter-typing", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					model.openSection(sectionConnections)
					model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
					for _, r := range "conn-001" {
						model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
					}
					model.View()
				}
			})
		})
	}
}

// TestScaleModelSmall keeps the fixture honest at a size that costs nothing.
func TestScaleModelSmall(t *testing.T) {
	model := scaleModel(t, 25)
	model.openSection(sectionConnections)
	if got := len(model.list.all); got != 25 {
		t.Fatalf("connections = %d, want 25", got)
	}
	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range "conn-0001" {
		model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := len(model.list.matches); got != 10 {
		t.Fatalf("matches = %d, want 10", got)
	}
}
