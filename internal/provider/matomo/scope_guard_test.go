package matomo

// Scope guard: the raw all-websites flag of the scope is private to targets.go. Any other file asks a predicate
// (allows, everySite, singles, binding) so an exclusion is never skipped by a module that reads the flag. The
// checker parses the non-test files of the package; a selector or composite-literal key named allSites outside
// targets.go is a finding.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

const guardedField = "allSites"

// scopeFieldReads returns the files other than targets.go that read or set the guarded field.
func scopeFieldReads(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var found []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "targets.go" {
			continue
		}
		source, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, source, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		reads := false
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.SelectorExpr:
				reads = reads || n.Sel.Name == guardedField
			case *ast.KeyValueExpr:
				if key, ok := n.Key.(*ast.Ident); ok {
					reads = reads || key.Name == guardedField
				}
			}
			return true
		})
		if reads {
			found = append(found, name)
		}
	}
	slices.Sort(found)
	return found, nil
}

func TestOnlyTargetsReadsTheAllWebsitesFlag(t *testing.T) {
	found, err := scopeFieldReads(os.DirFS("."))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("%v read scope.allSites; ask allows, everySite, singles, or binding instead", found)
	}
}

func TestScopeGuardFindsAReadOutsideTargets(t *testing.T) {
	fsys := fstest.MapFS{
		"targets.go":   {Data: []byte("package matomo\nfunc f(s scope) bool { return s.allSites }\n")},
		"read.go":      {Data: []byte("package matomo\nfunc g(s scope) bool { return s.allSites }\n")},
		"literal.go":   {Data: []byte("package matomo\nvar v = scope{allSites: true}\n")},
		"clean.go":     {Data: []byte("package matomo\nfunc h(s scope) bool { return s.everySite() }\n")},
		"read_test.go": {Data: []byte("package matomo\nfunc k(s scope) bool { return s.allSites }\n")},
	}
	found, err := scopeFieldReads(fsys)
	if err != nil || !slices.Equal(found, []string{"literal.go", "read.go"}) {
		t.Errorf("found = %v, err = %v", found, err)
	}
}
