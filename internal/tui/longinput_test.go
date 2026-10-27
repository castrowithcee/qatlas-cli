package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// longMark is a rune no screen of the editor draws by itself, so a line holding it holds the typed value.
const longMark = "Ж"

type longInputCase struct {
	name   string
	masked bool
	// open builds an editor of the given size with the input focused and returns the value check to run
	// after confirm.
	open func(t *testing.T, long string) (m *Model, confirm func(t *testing.T, m *Model))
}

func sizedModel(m *Model, w, h int) *Model {
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

func longInputCases() []longInputCase {
	return []longInputCase{
		{name: "secret prompt", masked: true,
			open: func(t *testing.T, long string) (*Model, func(*testing.T, *Model)) {
				m, _, _, _, mem := newStoreModel(t)
				addKeyringCredential(t, m, "reader")
				editEntry(t, m, "reader")
				focusRole(t, m, "token-id")
				press(t, m, "enter")
				if m.screen != screenSecret {
					t.Fatalf("screen = %v, want the secret prompt", m.screen)
				}
				return m, func(t *testing.T, m *Model) {
					pump(t, m, "enter")
					got, err := mem.Get(context.Background(), secret.StoreKey("reader", "token-id"))
					if err != nil || got != long {
						t.Errorf("stored %d characters (%v), want %d", len(got), err, len(long))
					}
				}
			}},
		{name: "vault passphrase prompt", masked: true,
			open: func(t *testing.T, long string) (*Model, func(*testing.T, *Model)) {
				dir := filepath.Join(t.TempDir(), "qatlas")
				store := newTestStore(t, filepath.Join(dir, "config.yaml"))
				secrets, _ := newVaultResolver(t, dir)
				m, err := buildModel(store, nil, secrets, nil)
				if err != nil {
					t.Fatalf("New() = %v", err)
				}
				addVaultCredential(t, m, "reader")
				editEntry(t, m, "reader")
				focusRole(t, m, "token-id")
				press(t, m, "enter")
				typeText(t, m, "first-secret")
				pump(t, m, "enter")
				if m.screen != screenVaultOffer {
					t.Fatalf("screen = %v, want the vault passphrase prompt", m.screen)
				}
				return m, func(t *testing.T, m *Model) {
					pump(t, m, "enter")
					if m.screen == screenVaultOffer && m.vaultOffer.confirming {
						typeText(t, m, long)
						pump(t, m, "enter")
					}
					if m.fail != "" {
						t.Fatalf("storing reported %q", m.fail)
					}
					got, found, _, err := secrets.Vault().Get("reader", "token-id",
						func(string) (string, error) { return long, nil })
					if err != nil || !found || got != "first-secret" {
						t.Errorf("the vault does not open with the full passphrase: %q, %v, %v", got, found, err)
					}
				}
			}},
		{name: "admin prompt", masked: true,
			open: func(t *testing.T, long string) (*Model, func(*testing.T, *Model)) {
				m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), long, true)
				openSectionByName(t, m, sectionServices)
				pressNew(t, m)
				typeText(t, m, "wiki")
				press(t, m, "tab", "tab")
				typeText(t, m, "https://wiki.example.invalid")
				press(t, m, "f2")
				if m.screen != screenAdminAuth {
					t.Fatalf("screen = %v, want the admin prompt", m.screen)
				}
				return m, func(t *testing.T, m *Model) {
					pump(t, m, "enter")
					if m.fail != "" || m.screen == screenAdminAuth {
						t.Errorf("the full passphrase was refused: screen %v, %q", m.screen, m.fail)
					}
				}
			}},
		{name: "unlock prompt", masked: true,
			open: func(t *testing.T, long string) (*Model, func(*testing.T, *Model)) {
				m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), long, false)
				openSectionByName(t, m, sectionServices)
				pump(t, m, "ctrl+l")
				if m.screen != screenVaultUnlock {
					t.Fatalf("screen = %v, want the unlock prompt", m.screen)
				}
				return m, func(t *testing.T, m *Model) {
					pump(t, m, "enter")
					if m.fail != "" || m.screen == screenVaultUnlock {
						t.Errorf("the full passphrase was refused: screen %v, %q", m.screen, m.fail)
					}
				}
			}},
		{name: "masked form field", masked: true,
			open: func(t *testing.T, long string) (*Model, func(*testing.T, *Model)) {
				m, _, _ := newModel(t)
				openSectionByName(t, m, sectionCredentials)
				pressNew(t, m)
				m.fields = append(m.fields, m.maskedField("bookstack", "api-key", true))
				focusField(t, m, "api-key")
				return m, func(t *testing.T, m *Model) {
					if got := m.fields[m.focus].input.Value(); got != long {
						t.Errorf("field holds %d characters, want %d", len(got), len(long))
					}
				}
			}},
		{name: "text form field",
			open: func(t *testing.T, long string) (*Model, func(*testing.T, *Model)) {
				m, _, _ := newModel(t)
				openSectionByName(t, m, sectionServices)
				pressNew(t, m)
				return m, func(t *testing.T, m *Model) {
					if got := m.fields[m.focus].input.Value(); got != long {
						t.Errorf("field holds %d characters, want %d", len(got), len(long))
					}
				}
			}},
		{name: "text form field with a long label",
			open: func(t *testing.T, long string) (*Model, func(*testing.T, *Model)) {
				m, _, _ := newModel(t)
				openSectionByName(t, m, sectionServices)
				pressNew(t, m)
				m.fields = append(m.fields, textField("a label over twelve", "", false))
				focusField(t, m, "a label over twelve")
				return m, func(t *testing.T, m *Model) {
					if got := m.fields[m.focus].input.Value(); got != long {
						t.Errorf("field holds %d characters, want %d", len(got), len(long))
					}
				}
			}},
		{name: "list filter",
			open: func(t *testing.T, long string) (*Model, func(*testing.T, *Model)) {
				m, _, _ := newModel(t)
				openSectionByName(t, m, sectionServices)
				press(t, m, "/")
				return m, func(t *testing.T, m *Model) {
					if got := m.list.input.Value(); got != long {
						t.Errorf("filter holds %d characters, want %d", len(got), len(long))
					}
				}
			}},
	}
}

func TestLongInputStaysOneScrollingLine(t *testing.T) {
	// Longer than the widest size below, which is all the scrolling needs; every typed rune redraws the screen.
	long := strings.Repeat(longMark, 300)
	sizes := []struct{ w, h int }{{80, 24}, {minimumWidth, minimumHeight}, {208, 60}}
	for _, c := range longInputCases() {
		for _, size := range sizes {
			for _, paste := range []bool{false, true} {
				name := fmt.Sprintf("%s/%dx%d", c.name, size.w, size.h)
				if paste {
					name += "/paste"
				}
				t.Run(name, func(t *testing.T) {
					m, confirm := c.open(t, long)
					sizedModel(m, size.w, size.h)
					if paste {
						m.Update(tea.PasteMsg{Content: long})
					} else {
						typeText(t, m, long)
					}

					view := m.render()
					if strings.Contains(view, "Resize terminal") {
						t.Fatalf("the long value replaced the screen:\n%s", view)
					}
					assertViewFits(t, view, size.w, size.h)
					view = leftOfPane(m, view)

					want := longMark
					if c.masked {
						want = "**"
						if strings.Contains(view, longMark) {
							t.Errorf("the masked value reached the screen:\n%s", view)
						}
					}
					var holding []string
					for _, line := range strings.Split(view, "\n") {
						if strings.Contains(line, want) {
							holding = append(holding, line)
						}
					}
					if len(holding) != 1 {
						t.Fatalf("%d lines hold the value, want 1:\n%s", len(holding), view)
					}
					// Only what the workspace frame holds counts: the tab line draws its own marks.
					inner := holding[0]
					for _, part := range strings.Split(inner, "│") {
						if strings.Contains(part, want) {
							inner = part
						}
					}
					if !strings.HasSuffix(strings.TrimRight(inner, " "), want) {
						t.Errorf("the cursor is not at the visible end of %q", holding[0])
					}
					confirm(t, m)
				})
			}
		}
	}
}

// A vault secret of several hundred characters is stored whole through the secret prompt.
func TestVaultSecretOf500CharactersIsStored(t *testing.T) {
	long := strings.Repeat("k", 500)
	dir := filepath.Join(t.TempDir(), "qatlas")
	store := newTestStore(t, filepath.Join(dir, "config.yaml"))
	secrets, _ := newVaultResolver(t, dir)
	mustNoError(t, secrets.SetVault("other", "role", "seed", nil))
	m, err := buildModel(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	sizedModel(m, 80, 24)
	addVaultCredential(t, m, "reader")
	editEntry(t, m, "reader")
	focusRole(t, m, "token-id")
	press(t, m, "enter")
	if m.screen != screenSecret {
		t.Fatalf("screen = %v, want the secret prompt", m.screen)
	}
	typeText(t, m, long)
	if view := m.render(); strings.Contains(view, "Resize terminal") {
		t.Fatalf("the long secret replaced the screen:\n%s", view)
	}
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("storing reported %q", m.fail)
	}
	got, found, _, err := secrets.Vault().Get("reader", "token-id", nil)
	if err != nil || !found || got != long {
		t.Errorf("stored %d characters (found %v, %v), want %d", len(got), found, err, len(long))
	}
}
