package tui

import (
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/i18n"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// A germanScreen opens one screen of the editor in the language the test runs in, so the same list serves the
// layout test in both languages and the check that no English sentence of the editor is left in German.
// Translating more screens means adding them to germanScreens; nothing else changes.
type germanScreen struct {
	name string
	open func(t *testing.T) *Model
}

// germanScreenSizes are the terminal sizes every screen of germanScreens must fit.
var germanScreenSizes = []struct{ w, h int }{{80, 24}, {100, 30}, {140, 40}}

// germanScreens are the screens whose own texts are translated.
func germanScreens() []germanScreen {
	return slices.Concat(vaultGermanScreens(), accessGermanScreens(), settingsGermanScreens(), logsGermanScreens(),
		transferGermanScreens(), updateGermanScreens(), selftestGermanScreens())
}

func settingsGermanScreens() []germanScreen {
	return []germanScreen{
		{"general", func(t *testing.T) *Model {
			m := unencryptedVaultModel(t)
			openSectionByName(t, m, sectionGeneral)
			return m
		}},
		{"defaults", func(t *testing.T) *Model {
			m, _, _ := newModel(t)
			seedConnections(t, m, "docs")
			openSectionByName(t, m, sectionDefaults)
			return m
		}},
		{"defaults form", func(t *testing.T) *Model {
			m, _, _ := newModel(t)
			seedConnections(t, m, "docs")
			openSectionByName(t, m, sectionDefaults)
			pressNew(t, m)
			return m
		}},
	}
}

func vaultGermanScreens() []germanScreen {
	vaultForm := func(t *testing.T, m *Model) *Model {
		openVaultSection(t, m)
		return m
	}
	return []germanScreen{
		{"vault unencrypted", func(t *testing.T) *Model { return vaultForm(t, unencryptedVaultModel(t)) }},
		{"vault locked", func(t *testing.T) *Model {
			m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
			return vaultForm(t, m)
		}},
		{"vault unlocked", func(t *testing.T) *Model {
			m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", true)
			return vaultForm(t, m)
		}},
		{"vault with credentials.yaml", func(t *testing.T) *Model {
			dir := filepath.Join(t.TempDir(), "qatlas")
			store := newTestStore(t, filepath.Join(dir, "config.yaml"))
			secrets, _ := newVaultResolver(t, dir)
			writeLegacyCredentialsYAML(t, dir, map[string]map[string]string{"reader": {"token-id": "canary-german-1"}})
			m, err := buildModel(store, nil, secrets, nil)
			mustNoError(t, err)
			return vaultForm(t, m)
		}},
		{"migrate preview", func(t *testing.T) *Model {
			dir := filepath.Join(t.TempDir(), "qatlas")
			store := newTestStore(t, filepath.Join(dir, "config.yaml"))
			secrets, _ := newVaultResolver(t, dir)
			cfg := newTestConfig(t)
			mustNoError(t, cfg.SetCredential("reader", config.Credential{Type: config.CredentialTypeKeyring}))
			mustNoError(t, store.Save(cfg))
			writeLegacyCredentialsYAML(t, dir, map[string]map[string]string{"reader": {"token-id": "canary-german-2"}})
			m, err := buildModel(store, nil, secrets, nil)
			mustNoError(t, err)
			vaultForm(t, m)
			focusRole(t, m, "migrate credentials.yaml")
			pump(t, m, "enter")
			if !m.migratePlanConfirm {
				t.Fatalf("enter did not open the migration preview: %q", m.fail)
			}
			return m
		}},
		{"migrate result", func(t *testing.T) *Model {
			m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", true)
			vaultForm(t, m)
			m.migrate = &migratePlan{}
			m.migrateResult = tr(tMigrateDone, countNoun(2))
			m.migrateDeleteConfirm, m.screen = true, screenConfirm
			return m
		}},
		{"unlock dialog", func(t *testing.T) *Model {
			m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
			openSectionByName(t, m, sectionServices)
			pump(t, m, "ctrl+l")
			typeText(t, m, "wrong-passphrase")
			pump(t, m, "enter")
			return m
		}},
		{"lock question", func(t *testing.T) *Model {
			m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", true)
			openSectionByName(t, m, sectionServices)
			pump(t, m, "ctrl+l")
			return m
		}},
		{"admin dialog", func(t *testing.T) *Model {
			m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
			vaultForm(t, m)
			focusField(t, m, "idle timeout")
			typeText(t, m, "1")
			pump(t, m, "f2")
			return m
		}},
		{"passphrase offer", func(t *testing.T) *Model {
			m := vaultForm(t, unencryptedVaultModel(t))
			focusRole(t, m, "encrypt")
			pump(t, m, "enter")
			typeText(t, m, "one passphrase")
			pump(t, m, "enter")
			typeText(t, m, "another passphrase")
			pump(t, m, "enter")
			return m
		}},
	}
}

// newTokenForm opens the form of a new agent token, starting empty where the editor asks how to start.
func newTokenForm(t *testing.T, m *Model) {
	t.Helper()
	pump(t, m, "n")
	if m.screen == screenTemplate {
		pump(t, m, "enter")
	}
	if m.screen != screenForm {
		t.Fatalf("n opened screen %v, fail %q", m.screen, m.fail)
	}
}

func accessGermanScreens() []germanScreen {
	tokens := func(t *testing.T) (*Model, string) {
		store, dir, v := newTokenFixture(t)
		fixtureToken(t, v, "agent", nil, "personal")
		m := openApprovalModel(t, store, dir)
		openTokensUnlocked(t, m)
		return m, dir
	}
	approvals := func(t *testing.T) *Model {
		dir := filepath.Join(t.TempDir(), "qatlas")
		store, _, _ := newApprovalFixture(t, dir, tokenPassphrase)
		m := openApprovalModel(t, store, dir)
		openSectionByName(t, m, sectionApprovals)
		pump(t, m, "enter")
		unlockWithAdminPassphrase(t, m, tokenPassphrase)
		return m
	}
	return []germanScreen{
		{"tokens without vault", func(t *testing.T) *Model {
			m := unencryptedVaultModel(t)
			openSectionByName(t, m, sectionTokens)
			return m
		}},
		{"tokens locked", func(t *testing.T) *Model {
			store, dir, _ := newTokenFixture(t)
			m := openApprovalModel(t, store, dir)
			openSectionByName(t, m, sectionTokens)
			return m
		}},
		{"tokens list", func(t *testing.T) *Model { m, _ := tokens(t); return m }},
		{"token form", func(t *testing.T) *Model {
			m, _ := tokens(t)
			newTokenForm(t, m)
			return m
		}},
		{"token form refused", func(t *testing.T) *Model {
			m, _ := tokens(t)
			newTokenForm(t, m)
			pump(t, m, "f2")
			return m
		}},
		{"token detail", func(t *testing.T) *Model {
			m, _ := tokens(t)
			pump(t, m, "enter")
			return m
		}},
		{"token detail revealed", func(t *testing.T) *Model {
			m, _ := tokens(t)
			pump(t, m, "enter", "s")
			return m
		}},
		{"token revoke question", func(t *testing.T) *Model {
			m, _ := tokens(t)
			pump(t, m, keyOf(actDelete))
			return m
		}},
		{"approvals locked", func(t *testing.T) *Model {
			dir := filepath.Join(t.TempDir(), "qatlas")
			store, _, _ := newApprovalFixture(t, dir, tokenPassphrase)
			m := openApprovalModel(t, store, dir)
			openSectionByName(t, m, sectionApprovals)
			return m
		}},
		{"approvals list", approvals},
		{"approval detail", func(t *testing.T) *Model {
			m := approvals(t)
			m.list.selectName("personal")
			pump(t, m, "enter")
			return m
		}},
		{"approve all question", func(t *testing.T) *Model {
			m := approvals(t)
			pump(t, m, "a")
			return m
		}},
		{"rename prompt", func(t *testing.T) *Model {
			m, _, _ := newModel(t)
			seedConnections(t, m, "docs")
			startRename(t, m, sectionServices, "wiki", "handbook")
			return m
		}},
		{"rename service dry run", func(t *testing.T) *Model {
			m, _, _ := newModel(t)
			seedConnections(t, m, "docs")
			startRename(t, m, sectionServices, "wiki", "handbook")
			pump(t, m, "enter")
			return m
		}},
		{"rename credential dry run", func(t *testing.T) *Model {
			m, _, _ := newModel(t)
			seedConnections(t, m, "docs")
			startRename(t, m, sectionCredentials, "reader", "keyholder")
			pump(t, m, "enter")
			return m
		}},
		{"rename connection dry run", func(t *testing.T) *Model {
			m, _, _ := newModel(t)
			seedConnections(t, m, "docs")
			startRename(t, m, sectionConnections, "docs", "manual")
			pump(t, m, "enter")
			return m
		}},
		{"renames found list", func(t *testing.T) *Model {
			s := newFindScene(t)
			s.renameByHand(t, "connection", "personal", "renamed")
			m := s.open(t)
			pump(t, m, "f")
			return m
		}},
		{"renames found adopt dry run", func(t *testing.T) *Model {
			s := newFindScene(t)
			s.renameByHand(t, "connection", "personal", "renamed")
			m := s.open(t)
			pump(t, m, "f", "enter")
			return m
		}},
	}
}

// Every screen of germanScreens fits the terminal in both languages.
func TestGermanScreensFit(t *testing.T) {
	for _, screen := range germanScreens() {
		t.Run(screen.name, func(t *testing.T) {
			eachLanguage(t, func(t *testing.T) {
				m := screen.open(t)
				for _, size := range germanScreenSizes {
					sizedModel(m, size.w, size.h)
					assertViewFits(t, m.View(), size.w, size.h)
				}
			})
		})
	}
}

var germanVerb = regexp.MustCompile(`%[-+# 0-9.*]*[a-zA-Z]`)

// englishSentences are the English texts of the catalog that have a German text of their own: more than one
// word once the format verbs are taken out, as a pattern that matches them with any one-word arguments. A single word
// is left out because the same word is also a technical term, a key, or a name.
func englishSentences() map[textID]*regexp.Regexp {
	out := map[textID]*regexp.Regexp{}
	for id, english := range textEN {
		german, ok := textDE[id]
		if !ok || german == english {
			continue
		}
		literal := strings.TrimSpace(germanVerb.ReplaceAllString(english, ""))
		if len(literal) < 10 || !strings.Contains(literal, " ") {
			continue
		}
		pattern := regexp.QuoteMeta(english)
		pattern = strings.NewReplacer(`%s`, `\S+`, `%d`, `[0-9]+`, `%q`, `\S+`, `%-11s`, `\S+`).Replace(pattern)
		// A word boundary at both ends: "up/down scroll" is not found in "up/down scrollen".
		if first, _ := utf8.DecodeRuneInString(english); unicode.IsLetter(first) {
			pattern = `\b` + pattern
		}
		if last, _ := utf8.DecodeLastRuneInString(english); unicode.IsLetter(last) {
			pattern += `\b`
		}
		out[id] = regexp.MustCompile(pattern)
	}
	return out
}

// germanCoreText hides the text of the core on a screen before the English sentences are searched for: the core
// words it in English, and a label of the catalog may be a phrase of it.
var germanCoreText = map[string]func(*Model){
	"update question": func(m *Model) { m.updateText = "(Text des Kerns)" },
}

// In German the views of germanScreens hold no English sentence of the editor's own: every catalog text that
// differs between the languages is searched for in its English form. Texts of the core and of the providers
// are not in the catalog and stay as they are.
func TestGermanScreensHoldNoEnglishSentence(t *testing.T) {
	sentences := englishSentences()
	ids := make([]string, 0, len(sentences))
	for id := range sentences {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, screen := range germanScreens() {
		t.Run(screen.name, func(t *testing.T) {
			useLanguage(t, i18n.German)
			m := screen.open(t)
			if hide, ok := germanCoreText[screen.name]; ok {
				hide(m)
			}
			sizedModel(m, 140, 40)
			view := strings.Join(strings.Fields(m.View()+"\n"+m.status+"\n"+m.fail), " ")
			for _, id := range ids {
				if loc := sentences[textID(id)].FindString(view); loc != "" {
					t.Errorf("the German view of %q holds the English text of %q: %q\n%s", screen.name, id, loc, view)
				}
			}
		})
	}
}

// The short texts built from parts of tokens and approvals read in German with their counts and names.
func TestGermanTokenAndApprovalPhrases(t *testing.T) {
	useLanguage(t, i18n.German)
	expired := time.Now().Add(-time.Hour)
	entry := tokenEntry{token: vault.Token{Name: "agent", Expires: &expired}, lapsed: []string{"a", "b"}}
	if got, want := tokenWarning(entry), "⚠ abgelaufen, 2 Vorbilder decken nichts ab"; got != want {
		t.Errorf("tokenWarning = %q, want %q", got, want)
	}
	if got, want := tokenExpiry(vault.Token{}), "kein Ablauf"; got != want {
		t.Errorf("tokenExpiry = %q, want %q", got, want)
	}
	change := approval.Change{Fields: []approval.FieldChange{{Field: approval.FieldTools, Added: []string{"a.b"}, Removed: []string{"c.d"}}}}
	if got, want := approvalSummary(change), "tools geändert"; got != want {
		t.Errorf("approvalSummary = %q, want %q", got, want)
	}
	if got, want := approvalCounts(change.Fields[0]), "1 neu, 1 entfernt"; got != want {
		t.Errorf("approvalCounts = %q, want %q", got, want)
	}
	if got, want := countNoun(1), "1 Eintrag"; got != want {
		t.Errorf("countNoun(1) = %q, want %q", got, want)
	}
	if got, want := kindText("credential"), "Credential"; got != want {
		t.Errorf("kindText = %q, want %q", got, want)
	}
}
