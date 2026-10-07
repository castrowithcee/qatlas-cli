package provider_test

// Helper guard: the shared transport and error helpers live in internal/provider, and provider packages use
// them instead of keeping their own copies. The checker works on an fs.FS and parses non-test files only;
// build tags are ignored on purpose, so every platform checks the same file set. Copies that exist today are
// listed as exceptions. The list can only shrink: a new copy fails the test, and so does an exception that no
// longer matches anything.
//
// A provider package must not declare a top-level function, method or type with the name of a shared helper,
// build an http.Client literal, spell the "Retry-After" header, or name provider.CauseConnectionReset. The
// files directly in internal/provider, the ratelimit package, and test files are not checked.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

const guardHint = "use the shared helper from internal/provider instead of a local copy"

// guardedNames are the top-level declarations a provider package must not redefine.
var guardedNames = map[string]bool{
	"providerError":        true,
	"invalidResponse":      true,
	"invalidRequest":       true,
	"newHTTPClient":        true,
	"retryAfter":           true,
	"capHold":              true,
	"transportError":       true,
	"validToken":           true,
	"redirectRefusedError": true,
}

const (
	nameHTTPClient = "http.Client"
	nameRetryAfter = `"Retry-After"`
	nameConnReset  = "provider.CauseConnectionReset"
)

// guardFinding is one use of a guarded construct: the provider directory relative to internal/provider, the
// slash-separated file path, and the guarded name.
type guardFinding struct {
	Dir  string
	File string
	Name string
}

// guardException allows one name in one provider directory. Every exception states why.
type guardException struct {
	Dir    string
	Name   string
	Reason string
}

const (
	keepName    = "kept: the package-local error constructor keeps its own name; renaming it is deferred"
	keepResp    = "kept: the package-local invalid-response constructor keeps its own name; renaming it is deferred"
	keepReq     = "kept: the package-local invalid-request constructor keeps its own name; renaming it is deferred"
	noRedirect  = "differs: the client refuses every redirect with a provider-specific error"
	ncRedirect  = "differs: the client follows a redirect only to the same origin and path prefix"
	tgClient    = "differs: the client returns every redirect response unfollowed and sets no transport; a test builds it directly"
	driveClient = "differs: the download client follows at most one https redirect"
	driveRetry  = "differs: the hold is capped at a provider-specific limit"
	ghRetry     = "differs: the hold also reads X-RateLimit-Reset when the quota is spent"
	ghCap       = "differs: the cap uses the provider's own maximum hold"
	tdRetry     = "differs: the hold falls back to a retry time from the response body"
	tdCap       = "differs: the cap uses the provider's own maximum hold"
	exRetry     = "differs: the hold falls back to X-RateLimit-Reset, a Unix timestamp"
	bsTransport = "differs: the constructor takes a parameter for changes"
	stToken     = "differs: the token check takes a minimum length and a wider character range"
	tgToken     = "differs: the token check uses another character set"
)

var guardExceptions = []guardException{
	{"baserow", "invalidRequest", keepReq},
	{"baserow", "invalidResponse", keepResp},
	{"baserow", "providerError", keepName},
	{"bookstack", "invalidRequest", keepReq},
	{"bookstack", "providerError", keepName},
	{"bookstack", "transportError", bsTransport},
	{"excalidrawplus", `"Retry-After"`, exRetry},
	{"excalidrawplus", "invalidRequest", keepReq},
	{"excalidrawplus", "invalidResponse", keepResp},
	{"excalidrawplus", "providerError", keepName},
	{"github", `"Retry-After"`, ghRetry},
	{"github", "capHold", ghCap},
	{"github", "invalidRequest", keepReq},
	{"github", "invalidResponse", keepResp},
	{"github", "providerError", keepName},
	{"github", "retryAfter", ghRetry},
	{"infomaniakchat", "invalidRequest", keepReq},
	{"infomaniakchat", "providerError", keepName},
	{"infomaniakdav", "http.Client", noRedirect},
	{"infomaniakdav", "invalidResponse", keepResp},
	{"infomaniakdav", "newHTTPClient", noRedirect},
	{"infomaniakdav", "providerError", keepName},
	{"infomaniakdrive", `"Retry-After"`, driveRetry},
	{"infomaniakdrive", "http.Client", driveClient},
	{"infomaniakdrive", "invalidRequest", keepReq},
	{"infomaniakdrive", "invalidResponse", keepResp},
	{"infomaniakdrive", "providerError", keepName},
	{"infomaniakdrive", "retryAfter", driveRetry},
	{"infomaniakmail", "invalidRequest", keepReq},
	{"infomaniakmail", "invalidResponse", keepResp},
	{"infomaniakmail", "providerError", keepName},
	{"lexware", "providerError", keepName},
	{"make", "invalidRequest", keepReq},
	{"make", "invalidResponse", keepResp},
	{"make", "providerError", keepName},
	{"n8n", "invalidRequest", keepReq},
	{"n8n", "invalidResponse", keepResp},
	{"n8n", "providerError", keepName},
	{"nextcloud", "http.Client", ncRedirect},
	{"nextcloud", "invalidResponse", keepResp},
	{"nextcloud", "newHTTPClient", ncRedirect},
	{"nextcloud", "providerError", keepName},
	{"penpot", "invalidRequest", keepReq},
	{"penpot", "invalidResponse", keepResp},
	{"penpot", "providerError", keepName},
	{"seatable", "providerError", keepName},
	{"seatable", "validToken", stToken},
	{"seatableaccount", "invalidRequest", keepReq},
	{"seatableaccount", "invalidResponse", keepResp},
	{"seatableaccount", "providerError", keepName},
	{"telegram", "http.Client", tgClient},
	{"telegram", "invalidResponse", keepResp},
	{"telegram", "newHTTPClient", tgClient},
	{"telegram", "providerError", keepName},
	{"telegram", "validToken", tgToken},
	{"todoist", `"Retry-After"`, tdRetry},
	{"todoist", "capHold", tdCap},
	{"todoist", "invalidRequest", keepReq},
	{"todoist", "invalidResponse", keepResp},
	{"todoist", "providerError", keepName},
	{"todoist", "retryAfter", tdRetry},
	{"twentycrm", "providerError", keepName},
}

func TestProviderHelperGuard(t *testing.T) {
	findings, err := guardFindings(os.DirFS(providerTree(t)))
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range guardProblems(findings, guardExceptions) {
		t.Error(problem)
	}
}

// providerTree returns the directory of this package, internal/provider.
func providerTree(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(dir)
}

// guardProblems turns findings into failure messages: every finding without an exception, and every
// exception without a finding or without a reason or listed twice.
func guardProblems(findings []guardFinding, exceptions []guardException) []string {
	type key struct{ dir, name string }
	found := map[key]bool{}
	for _, f := range findings {
		found[key{f.Dir, f.Name}] = true
	}
	excepted := map[key]bool{}
	var problems []string
	for _, e := range exceptions {
		k := key{e.Dir, e.Name}
		switch {
		case strings.TrimSpace(e.Reason) == "":
			problems = append(problems, fmt.Sprintf("%s: exception for %s has no reason", e.Dir, e.Name))
		case excepted[k]:
			problems = append(problems, fmt.Sprintf("%s: exception for %s is listed twice", e.Dir, e.Name))
		case !found[k]:
			problems = append(problems, fmt.Sprintf("%s: exception for %s is stale; remove it from the list", e.Dir, e.Name))
		}
		excepted[k] = true
	}
	for _, f := range findings {
		if excepted[key{f.Dir, f.Name}] {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s: %s is a copy of a shared helper; %s", f.File, f.Name, guardHint))
	}
	sort.Strings(problems)
	return problems
}

// guardFindings walks the provider packages below the root of fsys and returns every guarded construct, sorted.
func guardFindings(fsys fs.FS) ([]guardFinding, error) {
	var findings []guardFinding
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != "." && (skipGuardDir(d.Name()) || p == "ratelimit") {
				return fs.SkipDir
			}
			return nil
		}
		dir := path.Dir(p)
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || dir == "." {
			return nil
		}
		src, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), p, src, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", p, err)
		}
		add := func(name string) { findings = append(findings, guardFinding{dir, p, name}) }
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if guardedNames[decl.Name.Name] {
					add(decl.Name.Name)
				}
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok && guardedNames[ts.Name.Name] {
						add(ts.Name.Name)
					}
				}
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ImportSpec:
				return false
			case *ast.CompositeLit:
				if isSelector(n.Type, "http", "Client") {
					add(nameHTTPClient)
				}
			case *ast.BasicLit:
				if n.Kind == token.STRING {
					if v, err := strconv.Unquote(n.Value); err == nil && v == "Retry-After" {
						add(nameRetryAfter)
					}
				}
			case *ast.SelectorExpr:
				if isSelector(n, "provider", "CauseConnectionReset") {
					add(nameConnReset)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Name < b.Name
	})
	out := findings[:0]
	for i, f := range findings {
		if i == 0 || f != findings[i-1] {
			out = append(out, f)
		}
	}
	return out, nil
}

// isSelector reports whether e is the qualified identifier pkg.name.
func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

// skipGuardDir reports directories the go tool ignores, which hold no module code.
func skipGuardDir(name string) bool {
	return name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

func TestProviderHelperGuardRules(t *testing.T) {
	fsys := fstest.MapFS{
		"alpha/func.go":       {Data: []byte("package alpha\nfunc retryAfter() {}\n")},
		"alpha/method.go":     {Data: []byte("package alpha\ntype T struct{}\nfunc (T) capHold() {}\n")},
		"alpha/type.go":       {Data: []byte("package alpha\ntype validToken struct{}\n")},
		"beta/client.go":      {Data: []byte("package beta\nimport \"net/http\"\nvar c = &http.Client{}\nvar d = http.Client{}\n")},
		"beta/header.go":      {Data: []byte("package beta\nvar h = \"Retry-After\"\n")},
		"gamma/cause.go":      {Data: []byte("package gamma\nvar c = provider.CauseConnectionReset\n")},
		"alpha/x_test.go":     {Data: []byte("package alpha\nfunc providerError() {}\n")},
		"ratelimit/rate.go":   {Data: []byte("package ratelimit\nfunc retryAfter() {}\n")},
		"helpers.go":          {Data: []byte("package provider\nfunc retryAfter() {}\nvar h = \"Retry-After\"\n")},
		"alpha/testdata/a.go": {Data: []byte("package a\nfunc retryAfter() {}\n")},
	}
	findings, err := guardFindings(fsys)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range findings {
		got = append(got, f.File+" "+f.Name)
	}
	want := []string{
		"alpha/func.go retryAfter",
		"alpha/method.go capHold",
		"alpha/type.go validToken",
		"beta/client.go http.Client",
		"beta/header.go \"Retry-After\"",
		"gamma/cause.go provider.CauseConnectionReset",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("findings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	excepted := []guardException{{"alpha", "retryAfter", "x"}}
	problems := guardProblems(findings[:1], excepted)
	if len(problems) != 0 {
		t.Errorf("exception should allow the finding, got %v", problems)
	}
	if problems := guardProblems(findings[:1], nil); len(problems) != 1 ||
		!strings.Contains(problems[0], "alpha/func.go") || !strings.Contains(problems[0], "internal/provider") {
		t.Errorf("unexcepted finding: %v", problems)
	}
	stale := []guardException{{"delta", "retryAfter", "x"}}
	if problems := guardProblems(nil, stale); len(problems) != 1 || !strings.Contains(problems[0], "stale") {
		t.Errorf("stale exception: %v", problems)
	}
	noReason := []guardException{{"alpha", "retryAfter", " "}}
	if problems := guardProblems(findings[:1], noReason); len(problems) == 0 || !strings.Contains(problems[0], "no reason") {
		t.Errorf("missing reason: %v", problems)
	}
	twice := []guardException{{"alpha", "retryAfter", "x"}, {"alpha", "retryAfter", "x"}}
	if problems := guardProblems(findings[:1], twice); len(problems) != 1 || !strings.Contains(problems[0], "twice") {
		t.Errorf("duplicate exception: %v", problems)
	}
}
