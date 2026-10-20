package manage

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const (
	canaryTarget = "spaces/CANARYTARGET"
	canaryName   = "CanaryName"
	canaryQuery  = "canaryquery"
)

var browseKinds = []config.TargetKind{
	{Name: "space", Forms: []string{"spaces/KEY"}},
	{Name: "page", Forms: []string{"spaces/KEY/pages/ID"}, Parent: "space"},
}

type browseCatalog struct{}

var browseProviders = []config.ProviderMetadata{
	{ID: "wiki", Name: "Wiki", Target: config.TargetMetadata{Kinds: browseKinds},
		SecretRoles: []config.SecretRole{{Name: "token"}}},
}

func (browseCatalog) ProviderMetadata(id string) (config.ProviderMetadata, bool) {
	for _, p := range browseProviders {
		if p.ID == id {
			return p, true
		}
	}
	return config.ProviderMetadata{}, false
}

func (browseCatalog) ProviderMetadataAll() []config.ProviderMetadata { return browseProviders }

// browseFixture is a service over a configuration with one wiki connection "docs", the wiki credentials
// "vault-cred" (vault) and "wiki-cred" (keyring), and a source whose calls are recorded. With readSecret
// set, a read resolves role token the way a provider does, through the resolver the core handed over or else
// own, the surface's resolver, and keeps what it got in seen.
type browseFixture struct {
	svc        *Service
	path       string
	calls      int
	list       func(config.TargetListRequest) (config.TargetPage, error)
	url        func(origin, rawURL string) ([]config.TargetEntry, error)
	own        *secret.Resolver
	readSecret bool
	resolved   *config.Resolved
	seen       string
}

// emptyStore is a system keyring that answers and holds nothing.
type emptyStore struct{}

func (emptyStore) Get(context.Context, string) (string, error) { return "", secret.ErrNoEntry }
func (emptyStore) Set(string, string) error                    { return secret.ErrUnavailable }
func (emptyStore) Delete(string) error                         { return secret.ErrNoEntry }

func (f *browseFixture) read(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver) error {
	f.calls++
	f.resolved = resolved
	if !f.readSecret {
		return nil
	}
	if secrets == nil {
		secrets = f.own
	}
	value, err := secrets.Resolve(secret.ForConnection(ctx, resolved), resolved.Credential, resolved.Secrets, "token")
	f.seen = value.Secret
	return err
}

func newBrowseFixture(t *testing.T, v *vault.Vault) *browseFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "qatlas", "config.yaml")
	store := config.NewStore(path, browseCatalog{})
	cfg := store.New()
	cfg.Services["wiki-svc"] = config.Service{Provider: "wiki", BaseURL: "https://wiki.example.test"}
	cfg.Credentials["wiki-cred"] = config.Credential{Type: config.CredentialTypeKeyring}
	cfg.Credentials["vault-cred"] = config.Credential{Provider: "wiki", Type: config.CredentialTypeVault}
	cfg.Connections["docs"] = config.Connection{Service: "wiki-svc", Credential: "wiki-cred"}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	secrets := newFakeSecrets()
	secrets.vault = v
	red := &redact.Redactor{}
	f := &browseFixture{path: path, own: secret.NewWith(nil, emptyStore{}, nil, red)}
	if v != nil {
		f.own.WithVault(v, nil)
	}
	f.svc = New(store, secrets, connlog.SurfaceTUI, red).WithVaultProcessSupport(false).
		WithTargetSource(TargetSource{
			List: func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
				req config.TargetListRequest) (config.TargetPage, error) {
				if err := f.read(ctx, resolved, secrets); err != nil {
					return config.TargetPage{}, err
				}
				return f.list(req)
			},
			ResolveURL: func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
				origin, rawURL string) ([]config.TargetEntry, error) {
				if err := f.read(ctx, resolved, secrets); err != nil {
					return nil, err
				}
				return f.url(origin, rawURL)
			},
		})
	return f
}

func spaceEntry(key string) config.TargetEntry {
	return config.TargetEntry{Target: "spaces/" + key, Name: "Space " + key, Kind: "space", HasChildren: true}
}

func TestListTargetsLevelsFollowParent(t *testing.T) {
	f := newBrowseFixture(t, nil)
	f.list = func(req config.TargetListRequest) (config.TargetPage, error) {
		if req.Kind == "space" {
			return config.TargetPage{Entries: []config.TargetEntry{spaceEntry("A")}}, nil
		}
		return config.TargetPage{Entries: []config.TargetEntry{
			{Target: req.Parent + "/pages/7", Name: "Page", Kind: "page", HasChildren: true},
		}}, nil
	}
	ctx := context.Background()
	top, _, err := f.svc.ListTargets(ctx, AdminProof{}, "docs", config.TargetListRequest{Kind: "space"})
	if err != nil || len(top.Entries) != 1 || !top.Entries[0].HasChildren {
		t.Fatalf("top level = %+v, %v", top, err)
	}
	sub, _, err := f.svc.ListTargets(ctx, AdminProof{}, "docs",
		config.TargetListRequest{Kind: "page", Parent: top.Entries[0].Target})
	if err != nil || len(sub.Entries) != 1 || sub.Entries[0].Target != "spaces/A/pages/7" {
		t.Fatalf("child level = %+v, %v", sub, err)
	}
	// A page without a child kind never claims children.
	if sub.Entries[0].HasChildren {
		t.Error("HasChildren kept for a kind without a child kind")
	}
	for name, req := range map[string]config.TargetListRequest{
		"unknown kind":         {Kind: "nope"},
		"child without parent": {Kind: "page"},
		"top with parent":      {Kind: "space", Parent: "spaces/A"},
		"malformed parent":     {Kind: "page", Parent: "spaces/*"},
	} {
		calls := f.calls
		_, _, err := f.svc.ListTargets(ctx, AdminProof{}, "docs", req)
		var choice *TargetChoiceError
		if !errors.As(err, &choice) || choice.Code != "invalid-request" || f.calls != calls {
			t.Errorf("%s: err = %v, calls %d -> %d, want invalid-request without a read", name, err, calls, f.calls)
		}
	}
}

func TestListTargetsFollowsCursorAcrossPages(t *testing.T) {
	f := newBrowseFixture(t, nil)
	pages := map[string]config.TargetPage{
		"":   {Entries: []config.TargetEntry{spaceEntry("A"), spaceEntry("B")}, Next: "c2", ServerSearch: true},
		"c2": {Entries: []config.TargetEntry{spaceEntry("C")}, Next: "c3"},
		"c3": {Entries: []config.TargetEntry{spaceEntry("D")}},
	}
	f.list = func(req config.TargetListRequest) (config.TargetPage, error) { return pages[req.Cursor], nil }
	var got []string
	req := config.TargetListRequest{Kind: "space"}
	for range 5 {
		page, _, err := f.svc.ListTargets(context.Background(), AdminProof{}, "docs", req)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page.Entries {
			got = append(got, e.Target)
		}
		if page.Next == "" {
			break
		}
		req.Cursor = page.Next
	}
	if strings.Join(got, ",") != "spaces/A,spaces/B,spaces/C,spaces/D" {
		t.Errorf("entries over all pages = %v", got)
	}
}

func TestListTargetsDropsInvalidEntries(t *testing.T) {
	f := newBrowseFixture(t, nil)
	f.list = func(config.TargetListRequest) (config.TargetPage, error) {
		return config.TargetPage{Entries: []config.TargetEntry{
			spaceEntry("OK"),
			{Target: "spaces/*", Name: "wildcard", Kind: "space"},
			{Target: "repos/x", Name: "wrong form", Kind: "space"},
			{Target: "spaces/K2/pages/1", Name: "wrong kind", Kind: "page"},
			{Target: "spaces/K3", Name: "", Kind: "space"},
			{Target: "spaces/K4", Name: "bad\nname", Kind: "space"},
			{Target: "spaces/K5", Name: strings.Repeat("x", 300), Kind: "space"},
			{Target: "spaces/K6", Name: "unknown kind", Kind: "other"},
			{Target: "", Name: "empty", Kind: "space"},
		}}, nil
	}
	page, _, err := f.svc.ListTargets(context.Background(), AdminProof{}, "docs", config.TargetListRequest{Kind: "space"})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Target != "spaces/OK" {
		t.Fatalf("entries = %+v, %v, want only spaces/OK", page.Entries, err)
	}
}

func TestListTargetsBoundsThePage(t *testing.T) {
	f := newBrowseFixture(t, nil)
	f.list = func(config.TargetListRequest) (config.TargetPage, error) {
		page := config.TargetPage{Next: strings.Repeat("c", maxCursorBytes+1)}
		return page, nil
	}
	_, _, err := f.svc.ListTargets(context.Background(), AdminProof{}, "docs", config.TargetListRequest{Kind: "space"})
	var choice *TargetChoiceError
	if !errors.As(err, &choice) || choice.Code != "invalid-provider-response" {
		t.Errorf("oversized cursor: err = %v, want invalid-provider-response", err)
	}
	f.list = func(config.TargetListRequest) (config.TargetPage, error) {
		var page config.TargetPage
		for i := range maxPageEntries + 20 {
			page.Entries = append(page.Entries, spaceEntry("S"+strings.Repeat("a", i%5)+string(rune('A'+i%26))))
		}
		return page, nil
	}
	page, _, err := f.svc.ListTargets(context.Background(), AdminProof{}, "docs", config.TargetListRequest{Kind: "space"})
	if err != nil || len(page.Entries) != maxPageEntries {
		t.Errorf("entries = %d, %v, want %d", len(page.Entries), err, maxPageEntries)
	}
}

func TestResolveTargetURL(t *testing.T) {
	f := newBrowseFixture(t, nil)
	var gotOrigin, gotURL string
	f.url = func(origin, rawURL string) ([]config.TargetEntry, error) {
		gotOrigin, gotURL = origin, rawURL
		return []config.TargetEntry{spaceEntry("A"), {Target: "bad", Name: "x", Kind: "space"}}, nil
	}
	entries, _, err := f.svc.ResolveTargetURL(context.Background(), AdminProof{}, "docs",
		"https://web.example.test/spaces/A?x=1")
	if err != nil || len(entries) != 1 || entries[0].Target != "spaces/A" {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	if gotOrigin != "https://wiki.example.test" || !strings.HasSuffix(gotURL, "/spaces/A?x=1") {
		t.Errorf("provider got origin %q url %q", gotOrigin, gotURL)
	}
	calls := f.calls
	for _, raw := range []string{
		"ftp://wiki.example.test/x",
		(&url.URL{Scheme: "https", User: url.UserPassword("user", "pw"), Host: "wiki.example.test", Path: "/x"}).String(), "/spaces/A", "https://wiki.example.test/\n",
	} {
		_, _, err := f.svc.ResolveTargetURL(context.Background(), AdminProof{}, "docs", raw)
		var choice *TargetChoiceError
		if !errors.As(err, &choice) || choice.Code != "invalid-request" {
			t.Errorf("%q: err = %v, want invalid-request", raw, err)
		}
	}
	if f.calls != calls {
		t.Error("a malformed URL reached the provider")
	}
}

// The vault of this test lives in a fresh temporary directory; the source is a fake, so nothing real is read
// even when the protection does not hold.
func TestTargetChoiceNeedsAdminProofForEncryptedVault(t *testing.T) {
	unlocked, locked := adminVault(t)
	for name, v := range map[string]*vault.Vault{"unlocked": unlocked, "locked": locked} {
		f := newBrowseFixture(t, v)
		f.list = func(config.TargetListRequest) (config.TargetPage, error) {
			return config.TargetPage{Entries: []config.TargetEntry{spaceEntry("A")}}, nil
		}
		f.url = func(string, string) ([]config.TargetEntry, error) { return nil, nil }
		_, _, err := f.svc.ListTargets(context.Background(), AdminProof{}, "docs", config.TargetListRequest{Kind: "space"})
		var choice *TargetChoiceError
		if !errors.As(err, &choice) || choice.Code != "admin-required" {
			t.Errorf("%s list: err = %v, want admin-required", name, err)
		}
		_, _, err = f.svc.ResolveTargetURL(context.Background(), AdminProof{}, "docs", "https://wiki.example.test/x")
		if !errors.As(err, &choice) || choice.Code != "admin-required" {
			t.Errorf("%s resolve: err = %v, want admin-required", name, err)
		}
		if f.calls != 0 {
			t.Errorf("%s: the provider was read %d times without proof", name, f.calls)
		}
		if _, err := os.Stat(filepath.Join(vault.New(filepath.Dir(f.path)).Dir(), "logs")); err == nil {
			t.Errorf("%s: a refused choice was logged", name)
		}

		session := NewAdminSession(true, nil)
		if _, ok := session.Authorize(0); ok {
			t.Fatal("an inactive session granted proof")
		}
		session.Grant(time.Minute)
		proof, ok := session.Authorize(time.Minute)
		if !ok {
			t.Fatal("an active session refused proof")
		}
		if _, _, err := f.svc.ListTargets(context.Background(), proof, "docs", config.TargetListRequest{Kind: "space"}); err != nil {
			t.Errorf("%s with proof: %v", name, err)
		}
	}
}

func TestTargetChoiceNeedsNoProofWithoutEncryptedVault(t *testing.T) {
	plain := vault.New(t.TempDir())
	if err := plain.Set("c", "token", "synthetic", nil); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]*vault.Vault{"none": nil, "unencrypted": plain} {
		f := newBrowseFixture(t, v)
		f.list = func(config.TargetListRequest) (config.TargetPage, error) { return config.TargetPage{}, nil }
		if _, _, err := f.svc.ListTargets(context.Background(), AdminProof{}, "docs", config.TargetListRequest{Kind: "space"}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestTargetChoiceClassifiesProviderErrors(t *testing.T) {
	canary := "CANARY-PROVIDER-TEXT"
	for name, tc := range map[string]struct {
		err  error
		code string
	}{
		"auth":        {&provider.Error{Class: provider.ClassAuth, Op: "x", Message: canary}, "auth"},
		"permission":  {&provider.Error{Class: provider.ClassPermission, Message: canary}, "permission"},
		"rate":        {&provider.Error{Class: provider.ClassRateLimited, Message: canary}, "rate-limited"},
		"wrapped":     {errors.Join(errors.New(canary), &provider.Error{Class: provider.ClassTimeout}), "timeout"},
		"deadline":    {context.DeadlineExceeded, "timeout"},
		"unsupported": {config.ErrTargetBrowseUnsupported, "unsupported-capability"},
		"raw":         {errors.New(canary), "runtime"},
	} {
		f := newBrowseFixture(t, nil)
		f.list = func(config.TargetListRequest) (config.TargetPage, error) { return config.TargetPage{}, tc.err }
		_, _, err := f.svc.ListTargets(context.Background(), AdminProof{}, "docs", config.TargetListRequest{Kind: "space"})
		var choice *TargetChoiceError
		if !errors.As(err, &choice) || choice.Code != tc.code {
			t.Errorf("%s: err = %v, want code %s", name, err, tc.code)
			continue
		}
		if strings.Contains(err.Error(), canary) {
			t.Errorf("%s: error leaks provider text: %q", name, err)
		}
	}
}

func TestTargetChoiceLogsWithoutTargetsNamesOrQuery(t *testing.T) {
	f := newBrowseFixture(t, nil)
	f.list = func(config.TargetListRequest) (config.TargetPage, error) {
		return config.TargetPage{Entries: []config.TargetEntry{
			{Target: canaryTarget, Name: canaryName, Kind: "space", Path: canaryName},
		}}, nil
	}
	f.url = func(string, string) ([]config.TargetEntry, error) { return nil, nil }
	ctx := context.Background()
	if _, warn, err := f.svc.ListTargets(ctx, AdminProof{}, "docs",
		config.TargetListRequest{Kind: "space", Query: canaryQuery, Cursor: "cursor-canary"}); err != nil || warn != "" {
		t.Fatalf("ListTargets() = %q, %v", warn, err)
	}
	if _, warn, err := f.svc.ResolveTargetURL(ctx, AdminProof{}, "docs",
		"https://wiki.example.test/spaces/CANARYTARGET?q="+canaryQuery); err != nil || warn != "" {
		t.Fatalf("ResolveTargetURL() = %q, %v", warn, err)
	}
	var logs strings.Builder
	dir := filepath.Join(vault.New(filepath.Dir(f.path)).Dir(), "logs")
	files, err := os.ReadDir(dir)
	if err != nil || len(files) == 0 {
		t.Fatalf("no log written: %v", err)
	}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(dir, file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		logs.Write(data)
	}
	text := logs.String()
	for _, want := range []string{`"path":"tui"`, `"connection":"docs"`, `"operation":"config.target.list:space"`,
		`"operation":"config.target.resolve"`, `"effect":"read"`, `"result":"success"`} {
		if !strings.Contains(text, want) {
			t.Errorf("log lacks %s:\n%s", want, text)
		}
	}
	for _, leak := range []string{"CANARYTARGET", canaryName, canaryQuery, "cursor-canary", "wiki.example.test"} {
		if strings.Contains(text, leak) {
			t.Errorf("log holds %q:\n%s", leak, text)
		}
	}
}

func TestServiceWithoutTargetSourceRefusesBeforeAnything(t *testing.T) {
	f := newBrowseFixture(t, nil)
	svc := New(f.svc.store, f.svc.secrets, connlog.SurfaceCLI, nil).WithVaultProcessSupport(false)
	_, _, err := svc.ListTargets(context.Background(), AdminProof{}, "no-such-connection", config.TargetListRequest{Kind: "space"})
	var choice *TargetChoiceError
	if !errors.As(err, &choice) || choice.Code != "unsupported-capability" {
		t.Errorf("ListTargets() = %v, want unsupported-capability", err)
	}
	_, _, err = svc.ResolveTargetURL(context.Background(), AdminProof{}, "docs", "https://wiki.example.test/x")
	if !errors.As(err, &choice) || choice.Code != "unsupported-capability" {
		t.Errorf("ResolveTargetURL() = %v, want unsupported-capability", err)
	}
	if _, err := os.Stat(filepath.Join(vault.New(filepath.Dir(f.path)).Dir(), "logs")); err == nil {
		t.Error("a refused choice was logged")
	}
}

func TestKeepValidTakesWildcardOnlyWhereAFormSpellsIt(t *testing.T) {
	metadata := config.TargetMetadata{Kinds: []config.TargetKind{
		{Name: "owner", Forms: []string{"users/LOGIN"}},
		{Name: "repository", Forms: []string{"repos/OWNER/REPO", "repos/OWNER/*"}, Parent: "owner"},
	}}
	kept := keepValid(metadata, []config.TargetEntry{
		{Target: "repos/o/*", Name: "all", Kind: "repository"},
		{Target: "repos/*/r", Name: "placeholder", Kind: "repository"},
		{Target: "users/*", Name: "owner wildcard", Kind: "owner"},
		{Target: "repos/o/r", Name: "one", Kind: "repository"},
	}, "", maxPageEntries)
	var got []string
	for _, e := range kept {
		got = append(got, e.Target)
	}
	if strings.Join(got, ",") != "repos/o/*,repos/o/r" {
		t.Errorf("kept = %v, want repos/o/*,repos/o/r", got)
	}
	if err := checkListRequest(metadata, config.TargetListRequest{Kind: "repository", Parent: "users/*"}); err == nil {
		t.Error("a wildcard parent was accepted")
	}
}
