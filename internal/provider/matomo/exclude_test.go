package matomo

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const excludeTools = "[matomo.sites.list, matomo.sites.get, matomo.users.get-access, matomo.multi-sites.get, " +
	"matomo.segments.get, matomo.segments.list, matomo.segments.add, matomo.segments.delete, matomo.segments.star]"

const excludeConnections = `
  x-read: {service: matomo, credential: token, effects: [read], exclude: [site/1]}
  x-tools: {service: matomo, credential: token, tools: ` + excludeTools + `, exclude: [site/1]}
  x-general: {service: matomo, credential: token, tools: ` + excludeTools + `, targets: [site], exclude: [site/3]}
  x-inst: {service: matomo, credential: token, tools: [matomo.users.get-access], targets: [site, instance], exclude: [site/1]}
  x-single: {service: matomo, credential: token, effects: [read], targets: [site/2, site/3], exclude: [site/9]}
`

func newExcludeEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	resetCatalogs()
	t.Cleanup(resetCatalogs)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(excludeConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

// excluding sets the exclusions of a connection after the environment was built.
func (e *environment) excluding(connection string, excludes ...string) {
	conn := e.cfg.Connections[connection]
	conn.Exclude = excludes
	e.cfg.Connections[connection] = conn
}

func TestExclusionFormsAreDeclaredAndParsedWithoutQuotingValues(t *testing.T) {
	metadata, ok := registry(t).ProviderMetadata(Provider)
	if !ok || !metadata.Target.Excludes() {
		t.Fatalf("metadata = %+v, want declared exclusions", metadata)
	}
	for _, kind := range metadata.Target.Kinds {
		if (len(kind.ExcludeForms) > 0) != (kind.Name == "site") {
			t.Errorf("kind %s: Excludable = %t", kind.Name, (len(kind.ExcludeForms) > 0))
		}
	}
	for _, tc := range []struct {
		name     string
		excludes []string
		valid    bool
	}{
		{"one website", []string{"site/5"}, true},
		{"two websites", []string{"site/5", "site/6"}, true},
		{"site", []string{"site"}, false},
		{"instance", []string{"instance"}, false},
		{"zero", []string{"site/0"}, false},
		{"padded ID", []string{"site/05"}, false},
		{"blank", []string{""}, false},
		{"padded", []string{" site/5"}, false},
		{"duplicate", []string{"site/5", "site/5"}, false},
		{"equal to a target", []string{"site/2"}, false},
		{"wildcard", []string{"site/*"}, false},
	} {
		_, err := parseScope([]string{"site/2", "instance"}, tc.excludes...)
		if (err == nil) != tc.valid {
			t.Errorf("%s: err = %v, want valid=%t", tc.name, err, tc.valid)
		}
		if err != nil && strings.Contains(err.Error(), "5") {
			t.Errorf("%s: error quotes the value: %v", tc.name, err)
		}
	}
	// What the core checks of the configuration, without a target the exclusion could equal.
	for _, tc := range []struct {
		excludes []string
		valid    bool
	}{
		{[]string{"site/5"}, true}, {[]string{"site/0"}, false}, {[]string{"instance"}, false},
		{[]string{"site/5", "site/5"}, false}, {[]string{"site"}, false}, {[]string{"site/7"}, true},
	} {
		if got := len(metadata.Target.CheckExcludes(tc.excludes, nil)) == 0; got != tc.valid {
			t.Errorf("CheckExcludes(%v) valid = %t, want %t", tc.excludes, got, tc.valid)
		}
	}
	// A bare site exclusion is refused where the connection opens.
	var calls []call
	env := newExcludeEnvironment(t, &calls, versionOnly("5.14.2").handler(t))
	env.excluding("x-read", "site")
	if _, err := env.invoke("matomo.sites.list", "x-read", `{}`); !isInvalidRequest(err) && classOf(err) == "" || *env.reads != 0 || len(calls) != 0 {
		t.Errorf("a site exclusion: err = %v, reads = %d, calls = %d", err, *env.reads, len(calls))
	}
	// An exclusion that matches no target is valid and changes nothing.
	one, err := parseScope([]string{"site/2", "site/3"}, "site/9")
	if err != nil || one.allows(9) || !one.allows(2) || one.everySite() || one.reachesSites() != true {
		t.Errorf("scope = %+v, %v", one, err)
	}
}

func TestExclusionsNeverChangeTheOfferedTools(t *testing.T) {
	var calls []call
	env := newExcludeEnvironment(t, &calls, versionOnly("5.14.2").handler(t))
	offered := func(connection string) []string {
		response, err := env.core.Tools(application.SearchRequest{Connection: connection})
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, tool := range response.Tools {
			ids = append(ids, tool.ID)
		}
		slices.Sort(ids)
		return ids
	}
	if got, want := offered("x-read"), offered("open"); !slices.Equal(got, want) {
		t.Errorf("x-read offers %v, open offers %v", got, want)
	}
	if got, want := offered("x-single"), offered("two"); len(got) != len(want) {
		t.Errorf("x-single offers %d tools, a binding without exclusions %d", len(got), len(want))
	}
	// The reach of a tool never sees an exclusion: the instance tools still need the instance target.
	for _, tool := range []string{"matomo.site-groups.list", "matomo.database-statistics.get"} {
		if reached, _ := reaches(tool, []string{"site"}); reached {
			t.Errorf("%s reached without the instance target", tool)
		}
		if reached, _ := reaches(tool, []string{"site", "instance"}); !reached {
			t.Errorf("%s not reached with the instance target", tool)
		}
	}
	if reached, _ := reaches("matomo.sites.list", nil); !reached {
		t.Error("an empty binding no longer reaches the websites")
	}
}

func TestExcludedWebsiteIsRefusedBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newExcludeEnvironment(t, &calls, versionOnly("5.14.2").handler(t))
	for _, tc := range []struct{ tool, connection, arguments string }{
		{"matomo.sites.get", "x-tools", `{"idSite":1}`},
		{"matomo.sites.get", "x-general", `{"idSite":3}`},
		{"matomo.users.get-access", "x-tools", `{"idSite":1}`},
		{"matomo.segments.list", "x-tools", `{"idSite":1}`},
		{"matomo.segments.add", "x-general", `{"idSite":3,"name":"a","definition":"countryCode==de"}`},
		{"matomo.multi-sites.get", "x-tools", `{"view":"site","idSite":1,"period":"day","date":"today"}`},
	} {
		_, err := env.change(tc.tool, tc.connection, tc.arguments)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "exclu") || strings.Contains(err.Error(), "site/") {
			t.Errorf("%s %s: err = %v", tc.tool, tc.arguments, err)
		}
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Fatalf("secret reads = %d, requests = %d, want none", *env.reads, len(calls))
	}
}

func TestSitesListWidensTheWindowForExcludedWebsitesAndKeepsCursorsOnTheRows(t *testing.T) {
	var calls []call
	env := newExcludeEnvironment(t, &calls, listing(5).handler(t))
	first := listOf(t, env, "matomo.sites.list", "x-read", `{"limit":2}`)
	if !slices.Equal(first.ids(), []int{2, 3}) || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first = %+v", first)
	}
	if limit := lastCall(calls, "SitesManager.getSitesWithAtLeastViewAccess").form.Get("limit"); limit != "4" {
		t.Errorf("limit of the first window = %s, want 4", limit)
	}
	second := listOf(t, env, "matomo.sites.list", "x-read", `{"cursor":"`+first.NextCursor+`"}`)
	if !slices.Equal(second.ids(), []int{4, 5}) || second.HasMore {
		t.Errorf("second = %+v", second)
	}
	// A page that ends exactly on the last website reports no more.
	exact := listOf(t, env, "matomo.sites.list", "x-read", `{"limit":4}`)
	if !slices.Equal(exact.ids(), []int{2, 3, 4, 5}) || exact.HasMore {
		t.Errorf("exact = %+v", exact)
	}
	// The cursor does not survive a change of the exclusions.
	env.excluding("x-read", "site/1", "site/2")
	if _, err := env.invoke("matomo.sites.list", "x-read", `{"cursor":"`+first.NextCursor+`"}`); !isInvalidRequest(err) {
		t.Errorf("cursor after an exclusion changed: err = %v", err)
	}
}

func TestUsersGetAccessCutsTheServerWindowBeforeFilteringIt(t *testing.T) {
	var calls []call
	rows := []string{"1", "2", "3", "4"}
	env := newExcludeEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"UsersManager.getSitesAccessForUser": func(c call) *http.Response {
			offset, _ := strconv.Atoi(c.form.Get("offset"))
			limit, _ := strconv.Atoi(c.form.Get("limit"))
			var out []string
			for _, id := range rows[min(offset, len(rows)):min(offset+limit, len(rows))] {
				out = append(out, `{"idsite":"`+id+`","site_name":"S`+id+`","role":"view","capabilities":[]}`)
			}
			return ok("[" + strings.Join(out, ",") + "]")
		},
	}).handler(t))
	type access struct {
		Sites []struct {
			ID int `json:"id"`
		} `json:"sites"`
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	}
	read := func(arguments string) (access, error) {
		var out access
		result, err := env.invoke("matomo.users.get-access", "x-tools", arguments)
		_ = json.Unmarshal([]byte(result), &out)
		return out, err
	}
	first, err := read(`{"login":"alice","limit":2}`)
	if err != nil || len(first.Sites) != 1 || first.Sites[0].ID != 2 || !first.HasMore {
		t.Fatalf("first = %+v, err = %v", first, err)
	}
	second, err := read(`{"login":"alice","limit":2,"cursor":"` + first.NextCursor + `"}`)
	if err != nil || len(second.Sites) != 2 || second.Sites[0].ID != 3 || second.HasMore {
		t.Fatalf("second = %+v, err = %v", second, err)
	}
	// A window made only of excluded websites is empty and still says that more follow.
	alone, err := read(`{"login":"alice","limit":1}`)
	if err != nil || len(alone.Sites) != 0 || !alone.HasMore || alone.NextCursor == "" {
		t.Fatalf("alone = %+v, err = %v", alone, err)
	}
	env.excluding("x-tools", "site/1", "site/2")
	if _, err := read(`{"login":"alice","limit":2,"cursor":"` + first.NextCursor + `"}`); !isInvalidRequest(err) {
		t.Errorf("cursor after an exclusion changed: err = %v", err)
	}
}

func TestUsersGetAccessByAccessDropsUsersLeftWithoutWebsites(t *testing.T) {
	var calls []call
	env := newExcludeEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"UsersManager.getUsersSitesFromAccess": func(call) *http.Response {
			return ok(`{"bob":["1"],"alice":["1","2"]}`)
		},
	}).handler(t))
	result, err := env.invoke("matomo.users.get-access", "x-inst", `{"access":"view"}`)
	if err != nil || strings.Contains(result, "bob") || !strings.Contains(result, `"login":"alice"`) ||
		!strings.Contains(result, `"sites":[2]`) {
		t.Errorf("result = %s, err = %v", result, err)
	}
}

func TestMultiSitesGetDropsSumsAndGroupsUnlessEveryWebsiteIsReached(t *testing.T) {
	const groups = `{"numSites":3,"totals":{"nb_visits":113},"lastDate":"2026-09-01","sites":[` +
		`{"label":"shops","isGroup":1,"nb_visits":109},{"label":"site1","idsite":1,"group":"shops","nb_visits":10},` +
		`{"label":"site3","idsite":3,"group":"","nb_visits":99},{"label":"site2","idsite":2,"group":"","nb_visits":4}]}`
	var calls []call
	env := newExcludeEnvironment(t, &calls, listedAnswers("MultiSites.getAllWithGroups", fixed(groups)).handler(t))
	result, err := env.invoke("matomo.multi-sites.get", "x-tools", `{"view":"groups","period":"month","date":"2026-09-01","limit":2}`)
	var out struct {
		NumSites *int           `json:"num_sites"`
		Totals   map[string]any `json:"totals"`
		Rows     []struct {
			Label string `json:"label"`
		} `json:"rows"`
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	}
	_ = json.Unmarshal([]byte(result), &out)
	// The window holds the group row and website 1; both are dropped, and the cursor follows the server rows.
	if err != nil || out.NumSites != nil || out.Totals != nil || len(out.Rows) != 0 || !out.HasMore || out.NextCursor == "" {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	env.excluding("x-tools", "site/1", "site/2")
	if _, err := env.invoke("matomo.multi-sites.get", "x-tools", `{"view":"groups","period":"month","date":"2026-09-01","limit":2,"cursor":"`+out.NextCursor+`"}`); !isInvalidRequest(err) {
		t.Errorf("cursor after an exclusion changed: err = %v", err)
	}
	// Without exclusions the sums stay.
	env.excluding("x-tools")
	result, err = env.invoke("matomo.multi-sites.get", "x-tools", `{"view":"groups","period":"month","date":"2026-09-01","limit":10}`)
	if err != nil || !strings.Contains(result, `"num_sites":3`) || !strings.Contains(result, `"totals"`) {
		t.Errorf("without exclusions: %s, err = %v", result, err)
	}
}

func TestSegmentForAllWebsitesIsReadableButNotWritableWithAnExclusion(t *testing.T) {
	var calls []call
	env := newExcludeEnvironment(t, &calls, segmentToolAnswers(answers{
		"SegmentEditor.getAll": func(call) *http.Response { return ok(storedSegments) },
		"SegmentEditor.add":    func(call) *http.Response { return ok(`{"value":12}`) },
		"SegmentEditor.delete": func(call) *http.Response { return ok(`true`) },
		"SegmentEditor.star":   func(call) *http.Response { return ok(`{"result":true,"starred":1}`) },
	}).handler(t))
	if result, err := env.invoke("matomo.segments.get", "x-tools", `{"idSegment":1}`); err != nil || !strings.Contains(result, `"enable_only_idsite":0`) {
		t.Fatalf("segment 0 read: %s, %v", result, err)
	}
	if result, err := env.invoke("matomo.segments.get", "x-tools", `{"idSegment":2}`); err == nil {
		t.Errorf("a segment of an excluded website was read: %s", result)
	}
	listed, err := env.invoke("matomo.segments.list", "x-tools", `{}`)
	if err != nil || strings.Contains(listed, "Site one") || !strings.Contains(listed, "All visits DE") {
		t.Errorf("list = %s, err = %v", listed, err)
	}
	before := len(calls)
	for _, args := range []string{`{"name":"a","definition":"countryCode==de"}`, `{"idSite":0,"name":"a","definition":"countryCode==de"}`} {
		if _, err := env.change("matomo.segments.add", "x-tools", args); !isInvalidRequest(err) {
			t.Errorf("add %s: err = %v", args, err)
		}
	}
	if len(calls) != before {
		t.Errorf("a refused segment add caused %d requests", len(calls)-before)
	}
	for _, tool := range []string{"matomo.segments.delete", "matomo.segments.star"} {
		if _, err := env.change(tool, "x-tools", `{"idSegment":1}`); err == nil {
			t.Errorf("%s changed a segment for all websites", tool)
		}
	}
	for _, call := range calls {
		if call.api() == "SegmentEditor.add" || call.api() == "SegmentEditor.delete" || call.api() == "SegmentEditor.star" {
			t.Errorf("a change reached Matomo: %s", call.api())
		}
	}
	// A website that is not excluded is still written.
	if _, err := env.change("matomo.segments.add", "x-tools", `{"idSite":2,"name":"a","definition":"countryCode==de"}`); err != nil {
		t.Errorf("add for a reachable website: %v", err)
	}
	// Every website without exclusions writes it.
	env.excluding("x-tools")
	if _, err := env.change("matomo.segments.add", "x-tools", `{"idSite":0,"name":"a","definition":"countryCode==de"}`); err != nil {
		t.Errorf("add for all websites: %v", err)
	}
}

// refusing runs a handler directly: it must refuse the excluded website before any secret is read.
func TestEveryToolGroupRefusesAnExcludedWebsiteBeforeTheSecret(t *testing.T) {
	var reads int
	red := &redact.Redactor{}
	secrets := resolver(red, &reads)
	for _, tc := range []struct {
		name      string
		handler   func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error)
		arguments string
	}{
		{"scheduled reports", invokeScheduledReportsList, `{"idSite":3}`},
		{"tracking failures", invokeTrackingFailuresList, `{"idSite":3}`},
		{"privacy find", invokeDataSubjectsFind, `{"idSite":3,"segment":"visitorId==0123456789abcdef"}`},
		{"privacy export", invokeDataSubjectsExport, `{"visits":[{"idSite":3,"idVisit":1}]}`},
		{"raw data", invokeRawDataAnonymize, `{"idSites":[2,3],"date":"2026-01-01","anonymizeIp":true}`},
		{"site management", invokeSitesUpdateCreatedTime, `{"idSites":[2,3],"minDate":"2024-01-15"}`},
		{"site deletion", invokeSitesDelete, `{"idSite":3}`},
		{"tracking", invokeTrackingRecordPageView, `{"idSite":3,"url":"https://example.org/"}`},
		{"instance settings", invokeGlobalSettingsGet, `{"idSite":3,"section":"excluded-query-parameters"}`},
		{"access", invokeUsersGetAccess, `{"idSite":3}`},
		{"latest visit", invokeVisitsGetLatestTime, `{"idSites":[2,3],"period":"day","date":"today"}`},
	} {
		resolved := &config.Resolved{Targets: []string{"site", "instance"}, Exclude: []string{"site/3"}}
		_, err := tc.handler(context.Background(), resolved, secrets, red, json.RawMessage(tc.arguments))
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "exclu") || strings.Contains(err.Error(), "site/3") {
			t.Errorf("%s: err = %v, want an invalid request that names nothing", tc.name, err)
		}
	}
	if reads != 0 {
		t.Errorf("secret reads = %d", reads)
	}
}
