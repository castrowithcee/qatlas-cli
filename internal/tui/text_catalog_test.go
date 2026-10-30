package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

var placeholderPattern = regexp.MustCompile(`\{[a-z]+\}`)

// declaredTextIDs are the ids declared as constants of text_keys.go.
func declaredTextIDs(t *testing.T) map[string]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "text_keys.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok {
					continue
				}
				id, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				ids[id] = name.Name
			}
		}
	}
	return ids
}

// Every id the editor declares has a text in both languages, and no text is empty. The only empty German
// texts are footer groups that are folded into a neighbour's on purpose.
func TestEveryCatalogIDHasANonEmptyTextInBothLanguages(t *testing.T) {
	ids := declaredTextIDs(t)
	if len(ids) < 500 {
		t.Fatalf("found %d declared ids, the scan is broken", len(ids))
	}
	for id, name := range ids {
		if textEN[textID(id)] == "" {
			t.Errorf("%s (%q) has no English text", name, id)
		}
		if textDE[textID(id)] == "" {
			t.Errorf("%s (%q) has no German text", name, id)
		}
	}
	for lang, catalog := range map[string]map[textID]string{"English": textEN, "German": textDE} {
		for id, text := range catalog {
			if strings.TrimSpace(text) == "" && !(lang == "German" && strings.HasPrefix(string(id), "footer.")) {
				t.Errorf("%s text of %q is empty", lang, id)
			}
		}
	}
}

// A text names a key by its placeholder, so a rebound key shows in the prose and the standard key does not.
func TestProseNamesTheKeysInForce(t *testing.T) {
	keepKeys(t)
	eachLanguage(t, func(t *testing.T) {
		applyKeys(uiprefs.Keys{Save: "f4", Lock: "f6"})
		rebind(t, actSetup, "G")
		rebind(t, actBack, "ctrl+b")
		for _, c := range []struct {
			id        textID
			want, not string
		}{
			{tVaultLockedUnlockKey, "F6", "ctrl+l"},
			{tFormSaveTheCredentialWithF2, "F4", "F2"},
			{tStepSetup, "G", " g"},
			{tKeyDetailSuffix, "ctrl+b", "esc"},
			{tSearchField, "/", ""},
		} {
			got := tr(c.id)
			if !strings.Contains(got, c.want) || (c.not != "" && strings.Contains(got, c.not)) {
				t.Errorf("%s = %q, want %q and not %q", c.id, got, c.want, c.not)
			}
		}
		if got := tr(tCmdCannot, "quit"); !strings.Contains(got, "ctrl+b") || strings.Contains(got, "esc") {
			t.Errorf("the command line refusal = %q, want the rebound back key", got)
		}
	})
}

// No placeholder is left unresolved in a prose text and none is unknown to the key table.
func TestProsePlaceholdersAreActions(t *testing.T) {
	keepKeys(t)
	for lang, catalog := range map[string]map[textID]string{"English": textEN, "German": textDE} {
		for id, text := range catalog {
			if strings.HasPrefix(string(id), "footer.") {
				continue
			}
			resolved := keyPlaceholders(text)
			for _, bad := range placeholderPattern.FindAllString(resolved, -1) {
				t.Errorf("%s text of %q holds the unresolved %s", lang, id, bad)
			}
		}
	}
}
