package cli

// Architecture guard: the layer rules of the module are enforced by reading its source, so a violation fails
// the test run instead of waiting for a review. The checker works on an fs.FS and looks at imports and string
// literals only; build tags are ignored on purpose, so every platform checks the same file set. Violations
// that exist today are listed as exceptions. The list can only shrink: a new violation fails the test, and so
// does an exception that no longer matches anything.
//
// Rules:
//  1. Surfaces (internal/tui, internal/web) reach secrets, the vault, approvals and the connection log only
//     through the core, never through provider packages, the capability layer or the CLI.
//  2. Code outside internal/provider names no provider in a string literal.
//  3. Provider packages depend on the provider contract and a few shared leaf packages, never on another
//     provider.
//  4. Surfaces do not open the stores themselves: the qualified identifiers that construct or hold a config,
//     vault or secret store handle are used only where listed, so store access runs through the management core
//     (internal/manage). Types and constants of those packages stay free.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

const (
	ruleSurfaceAccess = 1
	ruleProviderNames = 2
	ruleProviderDeps  = 3
	ruleStoreHandles  = 4

	registryFile = "internal/cli/registry.go"
	providerRoot = "internal/provider/"
)

// archFinding is one violation: the rule, the slash-separated file path, and what was found. For rules 1 and 3
// the item is the module-relative import path, for rule 2 the provider name, ID or tool ID prefix, for rule 4
// the qualified identifier such as "config.Store" (always with the package's own name, never an import alias).
type archFinding struct {
	Rule int
	File string
	Item string
}

// archException allows one finding. Every exception states why.
type archException struct {
	Rule   int
	File   string
	Item   string
	Reason string
}

// Storage-related packages that surfaces must not import beyond their exceptions.
var surfaceForbidden = map[string]bool{
	"internal/secret":       true,
	"internal/vault":        true,
	"internal/vaultproc":    true,
	"internal/approval":     true,
	"internal/vaultmigrate": true,
	"internal/connlog":      true,
	"internal/capability":   true,
	"internal/cli":          true,
}

// Qualified identifiers that surfaces must not use beyond their exceptions, by module-relative package path.
var storeHandles = map[string]map[string]bool{
	"internal/config": {"Store": true, "NewStore": true, "Save": true},
	"internal/vault":  {"New": true, "OpenWithKey": true},
	"internal/secret": {"New": true, "NewWith": true, "SystemStore": true, "NewFile": true},
}

// Packages a provider may import from the module.
var providerAllowed = map[string]bool{
	"internal/provider":           true,
	"internal/provider/ratelimit": true,
	"internal/capability":         true,
	"internal/config":             true,
	"internal/redact":             true,
	"internal/localfile":          true,
	"internal/secret":             true,
}

// Names that are also ordinary words only count as a whole literal, or as a tool ID prefix for IDs.
var (
	ambiguousIDs   = map[string]bool{"make": true, "github": true}
	ambiguousNames = map[string]bool{"Make": true}
)

const (
	hintCore     = "move the logic into the core (internal/manage for management flows) and let the surface call it"
	hintStore    = "route the store access through the management core (internal/manage)"
	hintProvider = "keep provider specifics in internal/provider and let core and surfaces read them from the registry"
)

const (
	debtSurface = "existing debt: the surface reaches the store directly instead of through the core"
	debtHandle  = "existing debt: the surface holds or opens a store handle instead of calling the management core"
	debtOutput  = "existing debt: provider code uses the shared output package instead of the provider contract"
	debtName    = "existing debt: help or error text names a provider instead of reading it from the registry"
	releaseHost = "permanent: the text names GitHub as the release host, not as a provider"
)

var architectureExceptions = []archException{
	// Rule 1: surface access, internal/tui.
	{ruleSurfaceAccess, "internal/tui/approvals.go", "internal/approval", debtSurface},
	{ruleSurfaceAccess, "internal/tui/approvals.go", "internal/vault", debtSurface},
	{ruleSurfaceAccess, "internal/tui/logs.go", "internal/vault", debtSurface},
	{ruleSurfaceAccess, "internal/tui/logs.go", "internal/vaultproc", debtSurface},
	{ruleSurfaceAccess, "internal/tui/migrate.go", "internal/secret", debtSurface},
	{ruleSurfaceAccess, "internal/tui/migrate.go", "internal/vault", debtSurface},
	{ruleSurfaceAccess, "internal/tui/migrate.go", "internal/vaultmigrate", debtSurface},
	{ruleSurfaceAccess, "internal/tui/migrate.go", "internal/vaultproc", debtSurface},
	{ruleSurfaceAccess, "internal/tui/payload.go", "internal/vault", debtSurface},
	{ruleSurfaceAccess, "internal/tui/restart.go", "internal/vault", debtSurface},
	{ruleSurfaceAccess, "internal/tui/secrets.go", "internal/secret", debtSurface},
	{ruleSurfaceAccess, "internal/tui/secrets.go", "internal/vault", debtSurface},
	{ruleSurfaceAccess, "internal/tui/setup.go", "internal/secret", debtSurface},
	{ruleSurfaceAccess, "internal/tui/setup.go", "internal/vault", debtSurface},
	{ruleSurfaceAccess, "internal/tui/tokens.go", "internal/approval", debtSurface},
	{ruleSurfaceAccess, "internal/tui/tokens.go", "internal/vault", debtSurface},
	{ruleSurfaceAccess, "internal/tui/tui.go", "internal/approval", debtSurface},
	{ruleSurfaceAccess, "internal/tui/tui.go", "internal/secret", debtSurface},
	{ruleSurfaceAccess, "internal/tui/vaultheader.go", "internal/secret", debtSurface},
	{ruleSurfaceAccess, "internal/tui/vaultheader.go", "internal/vault", debtSurface},
	{ruleSurfaceAccess, "internal/tui/vaultheader.go", "internal/vaultproc", debtSurface},
	{ruleSurfaceAccess, "internal/tui/vaultsettings.go", "internal/vault", debtSurface},
	// Rule 1: surface access, internal/web.
	{ruleSurfaceAccess, "internal/web/connection.go", "internal/approval", debtSurface},
	{ruleSurfaceAccess, "internal/web/credential.go", "internal/vault", debtSurface},
	{ruleSurfaceAccess, "internal/web/server.go", "internal/secret", debtSurface},
	{ruleSurfaceAccess, "internal/web/server.go", "internal/vault", debtSurface},
	// Rule 4: store handles.
	{ruleStoreHandles, "internal/tui/logs.go", "vault.New", debtHandle},
	{ruleStoreHandles, "internal/tui/tui.go", "config.Store", debtHandle},
	{ruleStoreHandles, "internal/tui/vaultsettings.go", "config.Store", debtHandle},
	// Rule 2: provider names in string literals.
	{ruleProviderNames, "internal/cli/tui.go", "GitHub", debtName},
	{ruleProviderNames, "internal/cli/tui.go", "SeaTable", debtName},
	{ruleProviderNames, "internal/cli/tui.go", "Telegram", debtName},
	{ruleProviderNames, "internal/cli/update.go", "GitHub", releaseHost},
	{ruleProviderNames, "internal/helptopics/helptopics.go", "BookStack", debtName},
	{ruleProviderNames, "internal/helptopics/helptopics.go", "GitHub", debtName},
	{ruleProviderNames, "internal/helptopics/helptopics.go", "Nextcloud", debtName},
	{ruleProviderNames, "internal/helptopics/helptopics.go", "Telegram", debtName},
	{ruleProviderNames, "internal/helptopics/helptopics.go", "Todoist", debtName},
	{ruleProviderNames, "internal/helptopics/helptopics.go", "bookstack", debtName},
	{ruleProviderNames, "internal/helptopics/helptopics.go", "seatable", debtName},
	{ruleProviderNames, "internal/selfupdate/selfupdate.go", "GitHub", releaseHost},
	{ruleProviderNames, "internal/selfupdate/selfupdate.go", "github.", releaseHost},
	// Rule 3: provider dependencies.
	{ruleProviderDeps, "internal/provider/bookstack/attachments.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/attachments_test.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/attachmentswrite.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/attachmentswrite_test.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/auditlog.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/books.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/books_test.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/bookstack.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/bookstack_test.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/comments.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/comments_test.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/contentpermissions.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/contentpermissions_test.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/export.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/export_test.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/images.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/images_test.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/imageswrite.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/imports.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/imports_test.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/people.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/recyclebin.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/roleswrite.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/roleswrite_test.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/search.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/search_test.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/shelves.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/shelves_test.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/tags.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/tags_test.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/userswrite.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/userswrite_test.go", "internal/output", debtOutput},
	{ruleProviderDeps, "internal/provider/bookstack/writes.go", "internal/output", debtOutput},
}

func TestArchitecture(t *testing.T) {
	root := moduleRoot(t)
	findings, err := architectureFindings(os.DirFS(root), defaultRegistry().ProviderMetadataAll())
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range architectureProblems(findings, architectureExceptions) {
		t.Error(problem)
	}
}

// moduleRoot finds the directory with go.mod by walking up from the test's working directory.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}

// architectureProblems turns findings into failure messages: every finding without an exception, and every
// exception without a finding or listed twice.
func architectureProblems(findings []archFinding, exceptions []archException) []string {
	type key struct {
		rule       int
		file, item string
	}
	found := map[key]bool{}
	for _, f := range findings {
		found[key{f.Rule, f.File, f.Item}] = true
	}
	excepted := map[key]bool{}
	var problems []string
	for _, e := range exceptions {
		k := key{e.Rule, e.File, e.Item}
		switch {
		case strings.TrimSpace(e.Reason) == "":
			problems = append(problems, fmt.Sprintf("%s: rule %d exception for %q has no reason", e.File, e.Rule, e.Item))
		case excepted[k]:
			problems = append(problems, fmt.Sprintf("%s: rule %d exception for %q is listed twice", e.File, e.Rule, e.Item))
		case !found[k]:
			problems = append(problems, fmt.Sprintf("%s: rule %d exception for %q is stale; remove it from the list",
				e.File, e.Rule, e.Item))
		}
		excepted[k] = true
	}
	for _, f := range findings {
		if excepted[key{f.Rule, f.File, f.Item}] {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s: %s", f.File, describeFinding(f)))
	}
	sort.Strings(problems)
	return problems
}

func describeFinding(f archFinding) string {
	switch f.Rule {
	case ruleSurfaceAccess:
		return fmt.Sprintf("rule 1 (surface storage access): imports %s; %s", f.Item, hintCore)
	case ruleProviderNames:
		return fmt.Sprintf("rule 2 (provider neutrality): string literal names %q; %s", f.Item, hintProvider)
	case ruleStoreHandles:
		return fmt.Sprintf("rule 4 (surface store handles): uses %s; %s", f.Item, hintStore)
	default:
		return fmt.Sprintf("rule 3 (provider dependencies): imports %s; %s", f.Item, hintProvider)
	}
}

// architectureFindings walks the module in fsys and returns every violation of the three rules, sorted.
func architectureFindings(fsys fs.FS, providers []config.ProviderMetadata) ([]archFinding, error) {
	module, err := modulePath(fsys)
	if err != nil {
		return nil, err
	}
	providerDirs, err := registeredProviderDirs(fsys, module)
	if err != nil {
		return nil, err
	}
	matcher := newNameMatcher(providers)

	var findings []archFinding
	err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != "." && skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		isTest := strings.HasSuffix(p, "_test.go")
		inProviderTree := strings.HasPrefix(p, providerRoot)
		providerDir := ownerProviderDir(p, providerDirs)
		surface := !isTest && (underDir(p, "internal/tui") || underDir(p, "internal/web"))
		names := !isTest && !inProviderTree
		if !surface && !names && providerDir == "" {
			return nil
		}

		src, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		mode := parser.ImportsOnly | parser.SkipObjectResolution
		if names {
			mode = parser.SkipObjectResolution
		}
		file, err := parser.ParseFile(token.NewFileSet(), p, src, mode)
		if err != nil {
			return fmt.Errorf("parse %s: %w", p, err)
		}
		for _, imp := range file.Imports {
			target, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return fmt.Errorf("%s: bad import path %s", p, imp.Path.Value)
			}
			rel, inModule := strings.CutPrefix(target, module+"/")
			if !inModule {
				continue
			}
			if surface && (surfaceForbidden[rel] || strings.HasPrefix(rel, providerRoot)) {
				findings = append(findings, archFinding{ruleSurfaceAccess, p, rel})
			}
			if providerDir != "" && !providerAllowed[rel] && !(isTest && rel == "internal/application") {
				findings = append(findings, archFinding{ruleProviderDeps, p, rel})
			}
		}
		if surface {
			findings = append(findings, storeHandleFindings(file, p, module)...)
		}
		if names {
			ast.Inspect(file, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.ImportSpec:
					return false
				case *ast.BasicLit:
					if n.Kind != token.STRING {
						return true
					}
					if value, err := strconv.Unquote(n.Value); err == nil {
						for _, item := range matcher.match(value) {
							findings = append(findings, archFinding{ruleProviderNames, p, item})
						}
					}
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return dedupeFindings(findings), nil
}

// storeHandleFindings returns the protected qualified identifiers a file uses. A selector counts when its
// package name resolves, through the file's imports and their aliases, to one of the protected packages; the
// finding names the package's own name.
func storeHandleFindings(file *ast.File, p, module string) []archFinding {
	type pkg struct{ rel, name string }
	byLocal := map[string]pkg{}
	for _, imp := range file.Imports {
		target, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		rel, ok := strings.CutPrefix(target, module+"/")
		if !ok || storeHandles[rel] == nil {
			continue
		}
		local := path.Base(rel)
		if imp.Name != nil {
			local = imp.Name.Name
		}
		byLocal[local] = pkg{rel, path.Base(rel)}
	}
	if len(byLocal) == 0 {
		return nil
	}
	var findings []archFinding
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok {
			if target, ok := byLocal[id.Name]; ok && storeHandles[target.rel][sel.Sel.Name] {
				findings = append(findings, archFinding{ruleStoreHandles, p, target.name + "." + sel.Sel.Name})
			}
		}
		return true
	})
	return findings
}

func dedupeFindings(findings []archFinding) []archFinding {
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Item < b.Item
	})
	return compactFindings(findings)
}

func compactFindings(findings []archFinding) []archFinding {
	out := findings[:0]
	for i, f := range findings {
		if i == 0 || f != findings[i-1] {
			out = append(out, f)
		}
	}
	return out
}

// skipDir reports directories the go tool ignores, which hold no module code.
func skipDir(name string) bool {
	return name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

func underDir(p, dir string) bool { return strings.HasPrefix(p, dir+"/") }

// ownerProviderDir returns the registered provider package directory that holds p, or "".
func ownerProviderDir(p string, dirs []string) string {
	for _, dir := range dirs {
		if underDir(p, dir) {
			return dir
		}
	}
	return ""
}

// modulePath reads the module path from go.mod at the root of fsys.
func modulePath(fsys fs.FS) (string, error) {
	data, err := fs.ReadFile(fsys, "go.mod")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`), nil
		}
	}
	return "", fmt.Errorf("go.mod declares no module")
}

// registeredProviderDirs returns the directories of every provider package the registry file imports.
func registeredProviderDirs(fsys fs.FS, module string) ([]string, error) {
	src, err := fs.ReadFile(fsys, registryFile)
	if err != nil {
		return nil, err
	}
	file, err := parser.ParseFile(token.NewFileSet(), registryFile, src, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, imp := range file.Imports {
		target, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, err
		}
		if rel, ok := strings.CutPrefix(target, module+"/"); ok && strings.HasPrefix(rel, providerRoot) &&
			path.Dir(rel) == strings.TrimSuffix(providerRoot, "/") {
			dirs = append(dirs, rel)
		}
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("%s imports no provider package", registryFile)
	}
	return dirs, nil
}

// nameMatcher finds provider names, IDs and tool ID prefixes in a string literal. Names and IDs match as whole
// tokens: the characters around them are no letters or digits. Case is significant.
type nameMatcher struct {
	names []matchName
	ids   []matchID
}

type matchName struct {
	name string
	re   *regexp.Regexp
}

type matchID struct {
	id        string
	token     *regexp.Regexp
	toolID    *regexp.Regexp
	ambiguous bool
}

func newNameMatcher(providers []config.ProviderMetadata) nameMatcher {
	var m nameMatcher
	for _, p := range providers {
		m.names = append(m.names, matchName{p.Name, regexp.MustCompile(
			`(?:^|[^A-Za-z0-9])` + regexp.QuoteMeta(p.Name) + `(?:$|[^A-Za-z0-9])`)})
		m.ids = append(m.ids, matchID{
			id:    p.ID,
			token: regexp.MustCompile(`(?:^|[^A-Za-z0-9])` + regexp.QuoteMeta(p.ID) + `(?:$|[^A-Za-z0-9])`),
			// A tool ID prefix starts the literal or follows a separator that is no part of a path or host.
			toolID:    regexp.MustCompile(`(?:^|[^A-Za-z0-9._/@-])` + regexp.QuoteMeta(p.ID) + `\.[a-z]`),
			ambiguous: ambiguousIDs[p.ID],
		})
	}
	return m
}

// match returns the distinct findings of one literal value: a provider name, an ID, or an "<id>." prefix.
func (m nameMatcher) match(value string) []string {
	var items []string
	for _, n := range m.names {
		if ambiguousNames[n.name] {
			if value == n.name {
				items = append(items, n.name)
			}
			continue
		}
		if n.re.MatchString(value) {
			items = append(items, n.name)
		}
	}
	for _, id := range m.ids {
		switch {
		case value == id.id:
			items = append(items, id.id)
		case !id.ambiguous && id.token.MatchString(value):
			items = append(items, id.id)
		case id.toolID.MatchString(value):
			items = append(items, id.id+".")
		}
	}
	return items
}

// archFS builds a synthetic module with one registered provider ("alpha") that is clean in every rule.
func archFS(extra map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{
		"go.mod":     {Data: []byte("module example.com/m\n\ngo 1.25\n")},
		registryFile: {Data: []byte("package cli\n\nimport _ \"example.com/m/internal/provider/alpha\"\n")},
		"internal/provider/alpha/alpha.go": {Data: []byte(
			"package alpha\n\nimport _ \"example.com/m/internal/provider\"\n\nconst Name = \"Alpha\"\n")},
		"internal/tui/tui.go":   {Data: []byte("package tui\n\nimport _ \"example.com/m/internal/config\"\n")},
		"internal/core/core.go": {Data: []byte("package core\n\nconst Text = \"nothing to see\"\n")},
	}
	for name, src := range extra {
		fsys[name] = &fstest.MapFile{Data: []byte(src)}
	}
	return fsys
}

func archTestProviders() []config.ProviderMetadata {
	return []config.ProviderMetadata{{ID: "alpha", Name: "Alpha"}, {ID: "make", Name: "Make"}}
}

func TestArchitectureChecksDetectViolations(t *testing.T) {
	findings, err := architectureFindings(archFS(nil), archTestProviders())
	if err != nil || len(findings) != 0 {
		t.Fatalf("clean module: findings = %v, err = %v, want none", findings, err)
	}

	cases := []struct {
		name  string
		files map[string]string
		want  archFinding
	}{
		{"a surface that imports the vault", map[string]string{
			"internal/web/web.go": "package web\n\nimport _ \"example.com/m/internal/vault\"\n",
		}, archFinding{ruleSurfaceAccess, "internal/web/web.go", "internal/vault"}},
		{"a surface that imports a provider package", map[string]string{
			"internal/tui/x.go": "package tui\n\nimport _ \"example.com/m/internal/provider/alpha\"\n",
		}, archFinding{ruleSurfaceAccess, "internal/tui/x.go", "internal/provider/alpha"}},
		{"a surface that imports the capability layer", map[string]string{
			"internal/tui/x.go": "package tui\n\nimport _ \"example.com/m/internal/capability\"\n",
		}, archFinding{ruleSurfaceAccess, "internal/tui/x.go", "internal/capability"}},
		{"a surface that opens a config store", map[string]string{
			"internal/web/x.go": "package web\n\nimport \"example.com/m/internal/config\"\n\nvar _ = config.NewStore\n",
		}, archFinding{ruleStoreHandles, "internal/web/x.go", "config.NewStore"}},
		{"a surface that holds a store through an import alias", map[string]string{
			"internal/tui/x.go": "package tui\n\nimport cfg \"example.com/m/internal/config\"\n\ntype T struct{ s *cfg.Store }\n" +
				"\nfunc f() { _ = cfg.Config{}; _ = cfg.Store{} }\n",
		}, archFinding{ruleStoreHandles, "internal/tui/x.go", "config.Store"}},
		{"a provider name in a string", map[string]string{
			"internal/core/x.go": "package core\n\nconst Hint = \"Use Alpha here\"\n",
		}, archFinding{ruleProviderNames, "internal/core/x.go", "Alpha"}},
		{"a provider ID as a token", map[string]string{
			"internal/core/x.go": "package core\n\nconst Hint = \"the alpha provider\"\n",
		}, archFinding{ruleProviderNames, "internal/core/x.go", "alpha"}},
		{"a tool ID prefix of an ambiguous ID", map[string]string{
			"internal/core/x.go": "package core\n\nconst ID = \"make.scenarios.list\"\n",
		}, archFinding{ruleProviderNames, "internal/core/x.go", "make."}},
		{"an ambiguous ID as the exact literal", map[string]string{
			"internal/core/x.go": "package core\n\nconst ID = \"make\"\n",
		}, archFinding{ruleProviderNames, "internal/core/x.go", "make"}},
		{"a provider that imports a core package", map[string]string{
			"internal/provider/alpha/x.go": "package alpha\n\nimport _ \"example.com/m/internal/application\"\n",
		}, archFinding{ruleProviderDeps, "internal/provider/alpha/x.go", "internal/application"}},
		{"a provider test that imports another provider", map[string]string{
			"internal/provider/alpha/x_test.go": "package alpha\n\nimport _ \"example.com/m/internal/provider/beta\"\n",
		}, archFinding{ruleProviderDeps, "internal/provider/alpha/x_test.go", "internal/provider/beta"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			findings, err := architectureFindings(archFS(tt.files), archTestProviders())
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) != 1 || findings[0] != tt.want {
				t.Fatalf("findings = %v, want [%v]", findings, tt.want)
			}
			problems := architectureProblems(findings, nil)
			if len(problems) != 1 || !strings.Contains(problems[0], tt.want.File) ||
				!strings.Contains(problems[0], fmt.Sprintf("rule %d", tt.want.Rule)) {
				t.Fatalf("problems = %q, want one naming file and rule", problems)
			}
			excepted := []archException{{tt.want.Rule, tt.want.File, tt.want.Item, "test"}}
			if problems := architectureProblems(findings, excepted); len(problems) != 0 {
				t.Errorf("excepted finding still reported: %q", problems)
			}
		})
	}

	allowed := map[string]string{
		"internal/core/ok.go":                "package core\n\nconst A = \"make sure it works\"\nconst B = \"alphabet\"\n",
		"internal/core/ok_test.go":           "package core\n\nconst A = \"Alpha\"\n",
		"internal/core/testdata/data.go":     "package data\n\nconst A = \"Alpha\"\n",
		"internal/provider/alpha/ok.go":      "package alpha\n\nconst A = \"Alpha alpha.items.list\"\n",
		"internal/provider/alpha/ok_test.go": "package alpha\n\nimport _ \"example.com/m/internal/application\"\n",
		"internal/web/free.go":               "package web\n\nimport \"example.com/m/internal/config\"\n\nvar _ config.Config\n",
		"internal/web/ok_test.go":            "package web\n\nimport _ \"example.com/m/internal/vault\"\n",
	}
	if findings, err := architectureFindings(archFS(allowed), archTestProviders()); err != nil || len(findings) != 0 {
		t.Errorf("allowed code: findings = %v, err = %v, want none", findings, err)
	}

	// Only the handle identifiers are protected, and a vault or secret handle also trips rule 1 by its import.
	both := map[string]string{
		"internal/web/y.go": "package web\n\nimport (\n\t\"example.com/m/internal/secret\"\n\t\"example.com/m/internal/vault\"\n)\n\n" +
			"var _ secret.Source\nvar _ = vault.StateReady\nvar _ = secret.NewFile\n",
	}
	wantBoth := []archFinding{
		{ruleSurfaceAccess, "internal/web/y.go", "internal/secret"},
		{ruleSurfaceAccess, "internal/web/y.go", "internal/vault"},
		{ruleStoreHandles, "internal/web/y.go", "secret.NewFile"},
	}
	if got, err := architectureFindings(archFS(both), archTestProviders()); err != nil || !slices.Equal(got, wantBoth) {
		t.Errorf("vault and secret use: findings = %v, err = %v, want %v", got, err, wantBoth)
	}

	for rule := ruleSurfaceAccess; rule <= ruleStoreHandles; rule++ {
		stale := []archException{{rule, "internal/x.go", "item", "reason"}}
		problems := architectureProblems(nil, stale)
		if len(problems) != 1 || !strings.Contains(problems[0], "stale") {
			t.Errorf("rule %d: stale exception problems = %q, want one stale report", rule, problems)
		}
	}
	finding := []archFinding{{ruleProviderNames, "internal/x.go", "Alpha"}}
	for name, exceptions := range map[string][]archException{
		"no reason": {{ruleProviderNames, "internal/x.go", "Alpha", " "}},
		"duplicate": {{ruleProviderNames, "internal/x.go", "Alpha", "r"}, {ruleProviderNames, "internal/x.go", "Alpha", "r"}},
	} {
		if problems := architectureProblems(finding, exceptions); len(problems) == 0 {
			t.Errorf("exceptions with %s are accepted", name)
		}
	}
}
