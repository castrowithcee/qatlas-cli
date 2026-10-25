package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	goast "go/ast"
	goconstant "go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/validator/rules"
)

// The GraphQL documents of this provider are validated against a snapshot of GitHub's public schema
// (https://docs.github.com/public/fpt/schema.docs.graphql), because GitHub rejects a document that breaks
// the schema as a whole. Two sets are checked: the documents that are constants of the package, and the
// documents the other tests of the package send, which covers those composed at run time. Refresh the
// snapshot by downloading that file again.
const schemaSnapshot = "testdata/github.schema.graphql"

var (
	sentMu        sync.Mutex
	sentDocuments = map[string]bool{}
)

type recordingTransport struct{ next http.RoundTripper }

func (r recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil && strings.HasSuffix(req.URL.Path, "/graphql") {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		var payload struct {
			Query string `json:"query"`
		}
		if json.Unmarshal(body, &payload) == nil && payload.Query != "" {
			sentMu.Lock()
			sentDocuments[payload.Query] = true
			sentMu.Unlock()
		}
	}
	return r.next.RoundTrip(req)
}

// recordDocuments makes a test transport remember every GraphQL document it carries.
func recordDocuments(next http.RoundTripper) http.RoundTripper { return recordingTransport{next} }

func loadSchema() (*ast.Schema, error) {
	raw, err := os.ReadFile(schemaSnapshot)
	if err != nil {
		return nil, err
	}
	return gqlparser.LoadSchema(&ast.Source{Name: schemaSnapshot, Input: string(raw)})
}

// schemaRules are the validation rules without OverlappingFieldsCanBeMerged. Its strict reading rejects
// fields of the same name with different nullability in sibling fragments on different types, which GitHub
// accepts; the account selection relies on that.
func schemaRules() *rules.Rules {
	r := rules.NewDefaultRules()
	r.RemoveRule("OverlappingFieldsCanBeMerged")
	return r
}

// schemaErrors validates the documents and names each invalid one with its errors.
func schemaErrors(schema *ast.Schema, documents []string) []string {
	var failures []string
	for _, document := range documents {
		if _, errs := gqlparser.LoadQueryWithRules(schema, document, schemaRules()); len(errs) > 0 {
			failures = append(failures, fmt.Sprintf("%s\n    %v", document, errs))
		}
	}
	return failures
}

// TestMain checks the documents the tests sent once all of them ran.
func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 {
		schema, err := loadSchema()
		if err != nil {
			fmt.Fprintln(os.Stderr, "schema:", err)
			os.Exit(1)
		}
		sentMu.Lock()
		documents := make([]string, 0, len(sentDocuments))
		for document := range sentDocuments {
			documents = append(documents, document)
		}
		sentMu.Unlock()
		sort.Strings(documents)
		for _, failure := range schemaErrors(schema, documents) {
			fmt.Fprintln(os.Stderr, "document sent by a test breaks the GitHub schema:", failure)
			code = 1
		}
	}
	os.Exit(code)
}

// constantDocuments evaluates the string constants and variables of the package that are complete GraphQL
// operations. Fragments of a document that only compose into one are skipped by the balance check.
func constantDocuments(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, ".", func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("ParseDir() = %v", err)
	}
	var files []*goast.File
	for _, pkg := range packages {
		for _, file := range pkg.Files {
			files = append(files, file)
		}
	}
	info := &types.Info{Types: map[goast.Expr]types.TypeAndValue{}}
	config := types.Config{IgnoreFuncBodies: true, Error: func(error) {}, Importer: emptyImporter{}}
	_, _ = config.Check("github", fset, files, info)
	seen := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*goast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*goast.ValueSpec)
				if !ok {
					continue
				}
				for _, expr := range value.Values {
					known := info.Types[expr].Value
					if known == nil || known.Kind() != goconstant.String {
						continue
					}
					text := strings.TrimSpace(goconstant.StringVal(known))
					if (strings.HasPrefix(text, "query") || strings.HasPrefix(text, "mutation")) && balanced(text) {
						seen[text] = true
					}
				}
			}
		}
	}
	documents := make([]string, 0, len(seen))
	for text := range seen {
		documents = append(documents, text)
	}
	sort.Strings(documents)
	return documents
}

func balanced(text string) bool {
	depth := 0
	for _, r := range text {
		switch r {
		case '{', '(':
			depth++
		case '}', ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0 && strings.Contains(text, "{")
}

type emptyImporter struct{}

func (emptyImporter) Import(path string) (*types.Package, error) {
	pkg := types.NewPackage(path, path[strings.LastIndex(path, "/")+1:])
	pkg.MarkComplete()
	return pkg, nil
}

func TestConstantDocumentsMatchSchema(t *testing.T) {
	schema, err := loadSchema()
	if err != nil {
		t.Fatalf("loadSchema() = %v", err)
	}
	documents := constantDocuments(t)
	if len(documents) < 20 {
		t.Fatalf("found %d constant documents; the collection is broken", len(documents))
	}
	for _, failure := range schemaErrors(schema, documents) {
		t.Errorf("document breaks the GitHub schema: %s", failure)
	}
}

// TestItemDocumentsMatchSchema covers the project item documents, which are composed from methods.
func TestItemDocumentsMatchSchema(t *testing.T) {
	schema, err := loadSchema()
	if err != nil {
		t.Fatalf("loadSchema() = %v", err)
	}
	c := &Client{target: target{scope: "orgs"}}
	for _, failure := range schemaErrors(schema, []string{itemsQuery, c.itemQuery(), c.projectQuery()}) {
		t.Errorf("document breaks the GitHub schema: %s", failure)
	}
}
