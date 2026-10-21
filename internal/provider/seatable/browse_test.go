package seatable

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func browseResolved() *config.Resolved {
	return resolvedConnection("sales", "sales-reader", salesEnv, cloudOrigin, "")
}

func serveMetadata(t *testing.T, body string, calls *[]string) {
	t.Helper()
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		*calls = append(*calls, request.Method+" "+request.URL.Path)
		return jsonResponse(http.StatusOK, body), nil
	})
	stubLimiter(t, salesToken)
}

func targetsOf(page config.TargetPage) []string {
	var out []string
	for _, e := range page.Entries {
		out = append(out, e.Target)
	}
	return out
}

func TestBrowseLevels(t *testing.T) {
	var calls []string
	serveMetadata(t, metadataBody, &calls)
	red := &redact.Redactor{}
	page, err := listTargets(context.Background(), browseResolved(), resolver(red), red, config.TargetListRequest{Kind: "table"})
	if err != nil || !slices.Equal(targetsOf(page), []string{"id:0000", "id:0001"}) || page.Entries[0].Name != "Kunden" ||
		!page.Entries[0].HasChildren {
		t.Fatalf("tables = %+v, %v", page, err)
	}
	for _, parent := range []string{"id:0000", "Kunden"} {
		page, err = listTargets(context.Background(), browseResolved(), resolver(red), red,
			config.TargetListRequest{Kind: "view", Parent: parent})
		if err != nil || !slices.Equal(targetsOf(page), []string{"id:0000/id:0000", "id:0000/id:7Yk3"}) ||
			page.Entries[1].Name != "Aktive" {
			t.Fatalf("views of %s = %+v, %v", parent, page, err)
		}
	}
	for _, req := range []config.TargetListRequest{
		{Kind: "view"}, {Kind: "view", Parent: "id:0000/id:0000"}, {Kind: "workspace"}, {Kind: "table", Cursor: "2"},
	} {
		before := len(calls)
		var invalid *provider.InvalidRequestError
		if _, err := listTargets(context.Background(), browseResolved(), resolver(red), red, req); !asInvalid(err, &invalid) || len(calls) != before {
			t.Errorf("%+v: err = %v", req, err)
		}
	}
}

func asInvalid(err error, target **provider.InvalidRequestError) bool {
	e, ok := err.(*provider.InvalidRequestError)
	*target = e
	return ok
}

func TestBrowseEntriesAreValidTargets(t *testing.T) {
	var calls []string
	serveMetadata(t, `{"metadata":{"tables":[{"_id":"a b","name":"bad id","views":[]},
 {"_id":"T1","name":"x\u0000y","views":[{"_id":"V1","name":""}]}]}}`, &calls)
	red := &redact.Redactor{}
	meta, _ := registry(t).ProviderMetadata(Provider)
	page, err := listTargets(context.Background(), browseResolved(), resolver(red), red, config.TargetListRequest{Kind: "table"})
	if err != nil || !slices.Equal(targetsOf(page), []string{"id:T1"}) || page.Entries[0].Name != "x y" {
		t.Fatalf("tables = %+v, %v", page, err)
	}
	views, err := listTargets(context.Background(), browseResolved(), resolver(red), red,
		config.TargetListRequest{Kind: "view", Parent: "id:T1"})
	if err != nil || len(views.Entries) != 1 || views.Entries[0].Name != "V1" {
		t.Fatalf("views = %+v, %v", views, err)
	}
	for _, e := range append(page.Entries, views.Entries...) {
		if !meta.Target.ValidTarget(e.Kind, e.Target) {
			t.Errorf("%s is not a valid %s target", e.Target, e.Kind)
		}
	}
}

// Every form a connection takes today stays valid, in the kind that spells it and with the same reading.
func TestKindsCoverEveryTodayForm(t *testing.T) {
	meta, _ := registry(t).ProviderMetadata(Provider)
	if err := meta.Target.ValidateKindParents(); err != nil {
		t.Fatal(err)
	}
	for target, kind := range map[string]string{
		"Kunden": "table", "id:0000": "table", "*": "table", "Kunden Alt": "table",
		"Kunden/Aktive": "view", "id:0000/id:7Yk3": "view", "Kunden/id:7Yk3": "view", "id:0000/Aktive": "view",
	} {
		if !meta.Target.ValidTarget(kind, target) {
			t.Errorf("%s is not a valid %s target", target, kind)
		}
		other := map[string]string{"table": "view", "view": "table"}[kind]
		if target != "*" && meta.Target.ValidTarget(other, target) {
			t.Errorf("%s also fits %s", target, other)
		}
	}
	// The reading of a target is the parser's, not the kinds': a value with Kinds parses as before.
	for raw, want := range map[string]target{
		"Kunden":          {tableParam: "table_name", table: "Kunden"},
		"id:0000":         {tableParam: "table_id", table: "0000"},
		"Kunden/Aktive":   {tableParam: "table_name", table: "Kunden", viewParam: "view_name", view: "Aktive"},
		"id:0000/id:7Yk3": {tableParam: "table_id", table: "0000", viewParam: "view_id", view: "7Yk3"},
	} {
		if got, err := parseTarget(raw); err != nil || got != want {
			t.Errorf("parseTarget(%q) = %+v, %v", raw, got, err)
		}
	}
	if s, err := parseScope(&config.Resolved{Targets: []string{"*"}}); err != nil || !s.wildcard {
		t.Errorf("wildcard scope = %+v, %v", s, err)
	}
}

func TestReaches(t *testing.T) {
	meta, _ := registry(t).ProviderMetadata(Provider)
	for _, c := range []struct {
		tool    string
		targets []string
		reached bool
		hint    string
	}{
		{baseOperations.ID, []string{"*"}, true, ""},
		{baseOperations.ID, []string{"Kunden"}, false, "needs *"},
		{tablesCreate.ID, []string{"id:0000", "id:0001"}, false, "needs *"},
		{tablesDuplicate.ID, []string{"*"}, true, ""},
		{tablesRename.ID, []string{"Kunden"}, true, ""},
		{tablesRename.ID, []string{"Kunden/Aktive"}, false, "needs TABLE or *"},
		{tablesRename.ID, []string{"Kunden/Aktive", "id:0001"}, true, ""},
		{columnCreate.ID, []string{"id:0000/id:7Yk3"}, false, "needs TABLE or *"},
		{viewsCreate.ID, []string{"Kunden/Aktive"}, false, "needs TABLE or *"},
		{optionsAdd.ID, []string{"*"}, true, ""},
		{rowsList.ID, []string{"Kunden/Aktive"}, true, ""},
		{viewsList.ID, []string{"Kunden/Aktive"}, true, ""},
		{collaboratorsList.ID, []string{"Kunden"}, true, ""},
		{snapshotsCreate.ID, []string{"Kunden/Aktive"}, true, ""},
	} {
		reached, hint := meta.Target.Reach(c.tool, c.targets)
		if reached != c.reached || hint != c.hint {
			t.Errorf("%s %v = %v %q, want %v %q", c.tool, c.targets, reached, hint, c.reached, c.hint)
		}
	}
}

func TestResolveTargetURL(t *testing.T) {
	var calls []string
	serveMetadata(t, metadataBody, &calls)
	red := &redact.Redactor{}
	const base = "https://cloud.seatable.io/workspace/7/dtable/Sales/"
	for _, c := range []struct {
		url  string
		want []string
	}{
		{base + "?tid=0000&vid=7Yk3", []string{"id:0000", "id:0000/id:7Yk3"}},
		{base + "?tid=0000", []string{"id:0000"}},
		{base + "?tid=0001&vid=0000", []string{"id:0001", "id:0001/id:0000"}},
		{base + "?tid=0000&vid=zzzz", []string{"id:0000"}},
		{base + "?tid=9999", nil},
		{base, nil},
		{"https://cloud.seatable.io/workspace/8/dtable/Sales/?tid=0000", nil},
		{"https://cloud.seatable.io/workspace/7/dtable/Other/?tid=0000", nil},
		{"https://evil.example/workspace/7/dtable/Sales/?tid=0000", nil},
		{"http://cloud.seatable.io/workspace/7/dtable/Sales/?tid=0000", nil},
		{"https://cloud.seatable.io/workspace/7/dtable/Sales/?tid=a%2Fb", nil},
		{"not a url", nil},
	} {
		entries, err := resolveTargetURL(context.Background(), browseResolved(), resolver(red), red, cloudOrigin, c.url)
		if err != nil || !slices.Equal(targetsOf(config.TargetPage{Entries: entries}), c.want) {
			t.Errorf("%s = %v, %v, want %v", c.url, targetsOf(config.TargetPage{Entries: entries}), err, c.want)
		}
	}
	// A foreign host never reaches the provider.
	calls = nil
	_, _ = resolveTargetURL(context.Background(), browseResolved(), resolver(red), red, cloudOrigin,
		"https://evil.example/workspace/7/dtable/Sales/?tid=0000")
	if len(calls) != 0 {
		t.Errorf("calls = %v", calls)
	}
}

func TestFixesTargetWithMixedTableAndViewTargets(t *testing.T) {
	meta, _ := registry(t).ProviderMetadata(Provider)
	for _, c := range []struct {
		targets []string
		want    bool
	}{
		{[]string{"A"}, true},
		{[]string{"A/V"}, true},
		{[]string{"A", "A/V"}, true},
		{[]string{"A", "B/V"}, false},
		{[]string{"A/V", "B/W"}, false},
		{[]string{"*"}, false},
	} {
		if got := meta.Target.FixesTarget("table", c.targets); got != c.want {
			t.Errorf("FixesTarget(table, %v) = %v, want %v", c.targets, got, c.want)
		}
	}
}
