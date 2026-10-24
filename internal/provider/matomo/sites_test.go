package matomo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// listing answers the list methods with the sites it holds, honouring limit like Matomo: the first rows by ID.
func listing(total int) answers {
	list := func(c call) *http.Response {
		rows := total
		if limit, err := strconv.Atoi(c.form.Get("limit")); err == nil && limit < rows {
			rows = limit
		}
		ids := make([]string, rows)
		for i := range ids {
			ids[i] = strconv.Itoa(i + 1)
		}
		return ok(siteList(ids...))
	}
	return with(versionOnly("5.14.2"), answers{
		"SitesManager.getSitesWithAtLeastViewAccess": list,
		"SitesManager.getPatternMatchSites":          list,
	})
}

type listed struct {
	Sites []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"sites"`
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor"`
}

func (l listed) ids() []int {
	ids := []int{}
	for _, site := range l.Sites {
		ids = append(ids, site.ID)
	}
	return ids
}

func listOf(t *testing.T, env *environment, operation, connection, arguments string) listed {
	t.Helper()
	result, err := env.invoke(operation, connection, arguments)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	var out listed
	if json.Unmarshal([]byte(result), &out) != nil {
		t.Fatalf("result = %s", result)
	}
	return out
}

func TestSitesListPagesWithOneExtraRow(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, listing(5).handler(t))
	first := listOf(t, env, "matomo.sites.list", "open", `{"limit":2}`)
	if !slices.Equal(first.ids(), []int{1, 2}) || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first = %+v", first)
	}
	// One more row than the batch is asked for, from the start of the list.
	if got := calls[len(calls)-1].form.Get("limit"); got != "3" {
		t.Errorf("limit asked = %s, want 3", got)
	}
	second := listOf(t, env, "matomo.sites.list", "open", `{"cursor":"`+first.NextCursor+`"}`)
	if !slices.Equal(second.ids(), []int{3, 4}) || !second.HasMore {
		t.Fatalf("second = %+v", second)
	}
	last := listOf(t, env, "matomo.sites.list", "open", `{"limit":2,"cursor":"`+second.NextCursor+`"}`)
	if !slices.Equal(last.ids(), []int{5}) || last.HasMore || last.NextCursor != "" {
		t.Fatalf("last = %+v", last)
	}
}

func TestSitesListEndsExactlyOnAFullLastPage(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, listing(4).handler(t))
	first := listOf(t, env, "matomo.sites.list", "open", `{"limit":2}`)
	last := listOf(t, env, "matomo.sites.list", "open", `{"cursor":"`+first.NextCursor+`"}`)
	if !slices.Equal(last.ids(), []int{3, 4}) || last.HasMore || last.NextCursor != "" {
		t.Fatalf("last = %+v, want a full page that ends the list", last)
	}
	exact := listOf(t, env, "matomo.sites.list", "open", `{"limit":4}`)
	if exact.HasMore || len(exact.Sites) != 4 {
		t.Fatalf("exact = %+v", exact)
	}
}

func TestSitesListSlicesWhenTheServerIgnoresLimit(t *testing.T) {
	var calls []call
	all := siteList("1", "2", "3", "4", "5")
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"SitesManager.getSitesWithAtLeastViewAccess": func(call) *http.Response { return ok(all) },
	}).handler(t))
	first := listOf(t, env, "matomo.sites.list", "open", `{"limit":2}`)
	if !slices.Equal(first.ids(), []int{1, 2}) || !first.HasMore {
		t.Fatalf("first = %+v", first)
	}
	second := listOf(t, env, "matomo.sites.list", "open", `{"cursor":"`+first.NextCursor+`"}`)
	if !slices.Equal(second.ids(), []int{3, 4}) || !second.HasMore {
		t.Fatalf("second = %+v", second)
	}
}

func TestSitesListCursorIsBound(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, listing(5).handler(t))
	first := listOf(t, env, "matomo.sites.list", "open", `{"limit":2}`)
	for name, arguments := range map[string]string{
		"altered":       `{"cursor":"` + first.NextCursor[:len(first.NextCursor)-2] + `AA"}`,
		"garbage":       `{"cursor":"AAAA"}`,
		"other pattern": `{"pattern":"x","cursor":"` + first.NextCursor + `"}`,
		"other limit":   `{"limit":3,"cursor":"` + first.NextCursor + `"}`,
	} {
		if _, err := env.invoke("matomo.sites.list", "open", arguments); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want invalid-request", name, err)
		}
	}
	// A cursor of one connection does not continue a list of another binding.
	if _, err := env.invoke("matomo.sites.list", "two", `{"cursor":"`+first.NextCursor+`"}`); !isInvalidRequest(err) {
		t.Errorf("cursor of another binding: err = %v", err)
	}
	if _, err := env.invoke("matomo.sites.list", "open", `{"limit":101}`); err == nil {
		t.Error("an oversized limit was accepted")
	}
}

func TestSitesListPatternUsesItsOwnMethod(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, listing(3).handler(t))
	if _, err := env.invoke("matomo.sites.list", "open", `{"pattern":"shop"}`); err != nil {
		t.Fatal(err)
	}
	last := calls[len(calls)-1]
	if last.api() != "SitesManager.getPatternMatchSites" || last.form.Get("pattern") != "shop" {
		t.Fatalf("request = %+v", last)
	}
}

func listingWithSites(total int) answers {
	return with(listing(total), answers{"SitesManager.getSiteFromId": func(c call) *http.Response {
		return ok("[" + siteRow(c.form.Get("idSite"), "site"+c.form.Get("idSite")) + "]")
	}})
}

func TestSitesListDropsRowsOutsideTheTargets(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, listingWithSites(5).handler(t))
	for connection, want := range map[string][]int{
		"open": {1, 2, 3, 4, 5}, "general": {1, 2, 3, 4, 5}, "one": {2}, "two": {1, 2}, "mixed": {2},
	} {
		got := listOf(t, env, "matomo.sites.list", connection, `{}`)
		if !slices.Equal(got.ids(), want) || got.HasMore {
			t.Errorf("connection %s: sites = %+v, want %v", connection, got, want)
		}
	}
	// With a pattern the rows are filtered on this side, so more rows than a batch are read.
	got := listOf(t, env, "matomo.sites.list", "two", `{"pattern":"site"}`)
	if !slices.Equal(got.ids(), []int{1, 2}) {
		t.Errorf("pattern: sites = %+v", got)
	}
	if limit := calls[len(calls)-1].form.Get("limit"); limit != strconv.Itoa(scanLimit) {
		t.Errorf("limit asked with a pattern = %s", limit)
	}
}

func TestSitesListOfSingleWebsitesPagesTheBoundIDsAndReadsOnlyTheBatch(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, listingWithSites(5).handler(t))
	cfg := env.cfg
	connection := cfg.Connections["two"]
	// Websites far beyond any scan limit stay reachable: the bound IDs are the list.
	connection.Targets = []string{"site/900000", "site/3", "site/2000", "site/7"}
	cfg.Connections["many"] = connection
	first := listOf(t, env, "matomo.sites.list", "many", `{"limit":3}`)
	if !slices.Equal(first.ids(), []int{3, 7, 2000}) || !first.HasMore {
		t.Fatalf("first = %+v", first)
	}
	reads := 0
	for _, c := range calls {
		if c.api() == "SitesManager.getSiteFromId" {
			reads++
		}
		if strings.HasPrefix(c.api(), "SitesManager.getSites") || c.api() == "SitesManager.getPatternMatchSites" {
			t.Errorf("a list method was called: %s", c.api())
		}
	}
	if reads != 3 {
		t.Errorf("website reads = %d, want the 3 of the batch", reads)
	}
	last := listOf(t, env, "matomo.sites.list", "many", `{"cursor":"`+first.NextCursor+`"}`)
	if !slices.Equal(last.ids(), []int{900000}) || last.HasMore {
		t.Fatalf("last = %+v", last)
	}
	exact := listOf(t, env, "matomo.sites.list", "many", `{"limit":4}`)
	if len(exact.Sites) != 4 || exact.HasMore {
		t.Fatalf("exact = %+v", exact)
	}
}

func TestSitesListWithPatternAtTheScanLimitIsRefused(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, listing(scanLimit+5).handler(t))
	_, err := env.invoke("matomo.sites.list", "two", `{"pattern":"site"}`)
	if !isInvalidRequest(err) || !strings.Contains(err.Error(), "narrow the pattern") {
		t.Fatalf("err = %v, want invalid-request asking to narrow the pattern", err)
	}
	// Every website in scope needs no scan, and one below the limit is complete.
	if _, err := env.invoke("matomo.sites.list", "open", `{"pattern":"site","limit":10}`); err != nil {
		t.Errorf("open: %v", err)
	}
	var calls2 []call
	env2 := newEnvironment(t, &calls2, listing(scanLimit-1).handler(t))
	if _, err := env2.invoke("matomo.sites.list", "two", `{"pattern":"site"}`); err != nil {
		t.Errorf("below the limit: %v", err)
	}
}

func TestTargetBrowserListsWebsitesAsTargets(t *testing.T) {
	var calls []call
	long := strings.Repeat("n", 400)
	rows := `[` + siteRow("1", "alpha") + `,` + siteRow("2", "beta\\u0007x") + `,` + siteRow("3", long) + `]`
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"SitesManager.getSitesWithAtLeastViewAccess": func(c call) *http.Response { return ok(rows) },
		"SitesManager.getPatternMatchSites":          func(c call) *http.Response { return ok(`[` + siteRow("2", "beta") + `]`) },
	}).handler(t))
	// A connection bound to one website still lists all websites of the token.
	resolved := mustResolve(t, env, "one")
	page, err := listTargets(context.Background(), resolved, resolver(env.red, nil), env.red, config.TargetListRequest{Kind: "site"})
	if err != nil || len(page.Entries) != 3 || page.Next != "" || page.ServerSearch {
		t.Fatalf("page = %+v, %v", page, err)
	}
	if page.Entries[0].Target != "site/1" || page.Entries[0].Name != "alpha" || page.Entries[0].Kind != "site" ||
		strings.ContainsRune(page.Entries[1].Name, 7) || len(page.Entries[2].Name) > maxBrowseText {
		t.Fatalf("entries = %+v", page.Entries)
	}
	search, err := listTargets(context.Background(), resolved, resolver(env.red, nil), env.red,
		config.TargetListRequest{Kind: "site", Query: "beta"})
	if err != nil || len(search.Entries) != 1 || !search.ServerSearch || calls[len(calls)-1].form.Get("pattern") != "beta" {
		t.Fatalf("search = %+v, %v", search, err)
	}
	for _, request := range []config.TargetListRequest{{Kind: "instance"}, {Kind: "site", Parent: "site/1"},
		{Kind: "site", Cursor: "AAAA"}} {
		if _, err := listTargets(context.Background(), resolved, resolver(env.red, nil), env.red, request); err == nil {
			t.Errorf("request %+v was accepted", request)
		}
	}
	entries, err := resolveTargetURL(context.Background(), resolved, nil, nil, "https://analytics.example.test",
		"https://analytics.example.test/index.php?idSite=3")
	if err != nil || entries != nil {
		t.Errorf("resolveTargetURL = %v, %v, want nothing", entries, err)
	}
}

func TestTargetBrowserPagesAndStopsBeforeSecretOnBadBaseURL(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, listing(120).handler(t))
	resolved := mustResolve(t, env, "open")
	var seen []string
	cursor := ""
	for i := 0; i < 5; i++ {
		page, err := listTargets(context.Background(), resolved, resolver(env.red, nil), env.red,
			config.TargetListRequest{Kind: "site", Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page.Entries {
			seen = append(seen, e.Target)
		}
		if cursor = page.Next; cursor == "" {
			break
		}
	}
	if len(seen) != 120 {
		t.Fatalf("listed %d websites, want 120", len(seen))
	}
	resolved.BaseURL = "http://analytics.example.test"
	reads, before := 0, len(calls)
	if _, err := listTargets(context.Background(), resolved, resolver(env.red, &reads), env.red,
		config.TargetListRequest{Kind: "site"}); err == nil || reads != 0 || len(calls) != before {
		t.Errorf("bad base URL: err = %v, reads = %d", err, reads)
	}
}

func TestSitesGetReadsSiteAndAddresses(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"SitesManager.getSiteFromId": func(c call) *http.Response {
			if c.form.Get("idSite") != "2" {
				return failure(permissionMsg)
			}
			return ok("[" + siteRow("2", "shop") + "]")
		},
		"SitesManager.getSiteUrlsFromId": func(call) *http.Response {
			return ok(`["https://shop.example.test","https://www.shop.example.test"]`)
		},
	}).handler(t))
	// The one bound website is used when the argument is left out.
	for _, arguments := range []string{`{}`, `{"idSite":2}`} {
		result, err := env.invoke("matomo.sites.get", "one", arguments)
		if err != nil {
			t.Fatal(err)
		}
		var site Site
		if json.Unmarshal([]byte(result), &site) != nil || site.ID != 2 || site.Name != "shop" ||
			site.Timezone != "Europe/Berlin" || site.Currency != "EUR" || site.Ecommerce == nil || *site.Ecommerce ||
			site.SiteSearch == nil || !*site.SiteSearch || len(site.URLs) != 2 || site.CreatorLogin != "admin" {
			t.Fatalf("site = %s", result)
		}
	}
	// An object answer is read too.
	env2 := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"SitesManager.getSiteFromId":     func(call) *http.Response { return ok(siteRow("2", "shop")) },
		"SitesManager.getSiteUrlsFromId": func(call) *http.Response { return ok(`[]`) },
	}).handler(t))
	if _, err := env2.invoke("matomo.sites.get", "open", `{"idSite":2}`); err != nil {
		t.Fatal(err)
	}
	// An answer for another website is refused.
	env3 := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"SitesManager.getSiteFromId": func(call) *http.Response { return ok(siteRow("7", "other")) },
	}).handler(t))
	if _, err := env3.invoke("matomo.sites.get", "open", `{"idSite":2}`); classOf(err) != provider.ClassInvalidResponse {
		t.Fatalf("err = %v, want invalid-provider-response", err)
	}
}

func TestSitesGetBoundsProviderStrings(t *testing.T) {
	var calls []call
	long := strings.Repeat("x", 5000)
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"SitesManager.getSiteFromId": func(call) *http.Response {
			return ok(`[{"idsite":3,"name":"` + long + `","main_url":"` + long + `","excluded_ips":"` + long + `"}]`)
		},
		"SitesManager.getSiteUrlsFromId": func(call) *http.Response {
			urls := make([]string, 300)
			for i := range urls {
				urls[i] = "https://" + long
			}
			encoded, _ := json.Marshal(urls)
			return ok(string(encoded))
		},
	}).handler(t))
	result, err := env.invoke("matomo.sites.get", "open", `{"idSite":3}`)
	if err != nil {
		t.Fatal(err)
	}
	var site Site
	_ = json.Unmarshal([]byte(result), &site)
	if len(site.Name) > maxStringLength || len(site.MainURL) > maxUrlLength || len(site.ExcludedIPs) > maxSettingsText ||
		len(site.URLs) != maxSiteURLs || len(site.URLs[0]) > maxUrlLength {
		t.Fatalf("lengths: name %d main_url %d excluded_ips %d urls %d", len(site.Name), len(site.MainURL),
			len(site.ExcludedIPs), len(site.URLs))
	}
}

func TestForeignWebsiteIsRefusedBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, versionOnly("5.14.2").handler(t))
	for _, tc := range []struct{ connection, arguments string }{
		{"one", `{"idSite":99}`}, {"two", `{"idSite":3}`}, {"mixed", `{"idSite":4}`},
		{"one", `{"idSite":1}`}, // inside the account, outside the targets
		{"two", `{}`},           // two websites bound: the argument is required
		{"instance", `{"idSite":1}`},
	} {
		_, err := env.invoke("matomo.sites.get", tc.connection, tc.arguments)
		if err == nil || !isInvalidRequest(err) && !isUnsupported(err) {
			t.Errorf("%s %s: err = %v, want a refusal", tc.connection, tc.arguments, err)
			continue
		}
		for _, id := range []string{"99", "3", "4"} {
			if strings.Contains(err.Error(), id) && strings.Contains(tc.arguments, id) {
				t.Errorf("%s %s: refusal names the website: %v", tc.connection, tc.arguments, err)
			}
		}
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Fatalf("secret reads = %d, requests = %d, want none", *env.reads, len(calls))
	}
}

func TestInstanceOnlyBindingOffersNoWebsiteTool(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, versionOnly("5.14.2").handler(t))
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
	if got := offered("instance"); !slices.Equal(got, []string{"matomo.version.get"}) {
		t.Errorf("instance connection offers %v", got)
	}
	for _, connection := range []string{"open", "general", "one", "mixed"} {
		if got := offered(connection); len(got) != 22 {
			t.Errorf("connection %s offers %v", connection, got)
		}
	}
	if _, err := env.invoke("matomo.sites.list", "instance", `{}`); err == nil {
		t.Error("a website list ran on an instance-only binding")
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Errorf("secret reads = %d, requests = %d", *env.reads, len(calls))
	}
}

func TestTargetForms(t *testing.T) {
	for raw, valid := range map[string]bool{
		"site": true, "site/1": true, "site/123456789": true, "instance": true,
		"": false, "site/": false, "site/0": false, "site/01": false, "site/-1": false, "site/x": false,
		"site/1/2": false, "site/1 2": false, "sites/1": false, "site/1234567890": false, "Instance": false,
		"*": false, "instance/1": false,
	} {
		if err := validateTarget(raw); (err == nil) != valid {
			t.Errorf("validateTarget(%q) = %v, want valid=%t", raw, err, valid)
		}
	}
	for values, valid := range map[string]bool{
		"site": true, "site/1,site/2": true, "instance": true, "site,instance": true, "site/1,instance": true,
		"site,site/1": false, "site/1,site": false, "site/1,site/1": false, "site,site": false,
		"instance,instance": false, "site/1,bad": false,
	} {
		if err := validateSet(strings.Split(values, ",")); (err == nil) != valid {
			t.Errorf("validateSet(%q) = %v, want valid=%t", values, err, valid)
		}
	}
	if _, err := parseScope([]string{"site/" + strings.Repeat("7", 12)}); err == nil || strings.Contains(err.Error(), "777") {
		t.Errorf("a target error quotes the value: %v", err)
	}
}

func TestEmptyTargetsReachWebsitesButNoInstance(t *testing.T) {
	bound, err := parseScope(nil)
	if err != nil || !bound.allSites || bound.instance || !bound.allows(7) {
		t.Fatalf("scope = %+v, %v", bound, err)
	}
	mixed, _ := parseScope([]string{"site/2", "instance"})
	if !mixed.instance || mixed.allows(1) || !mixed.allows(2) {
		t.Fatalf("scope = %+v", mixed)
	}
}

func TestConnectionTest(t *testing.T) {
	site := func(c call) *http.Response {
		if c.form.Get("idSite") == "7" {
			return failure(permissionMsg)
		}
		return ok("[" + siteRow(c.form.Get("idSite"), "s") + "]")
	}
	run := func(t *testing.T, connection string, handler answers) (provider.Class, []call) {
		t.Helper()
		var calls []call
		env := newEnvironment(t, &calls, handler.handler(t))
		resolved := mustResolve(t, env, connection)
		class, err := TestConnection(context.Background(), resolved, resolver(env.red, nil), env.red)
		if err != nil {
			t.Fatal(err)
		}
		return class, calls
	}
	good := with(versionOnly("5.14.2"), answers{"SitesManager.getSiteFromId": site})
	if class, calls := run(t, "two", good); class != provider.ClassOK || len(calls) != 3 {
		t.Errorf("two: class = %q, calls = %d, want ok after the version and each website", class, len(calls))
	}
	if class, calls := run(t, "open", good); class != provider.ClassOK || len(calls) != 1 {
		t.Errorf("open: class = %q, calls = %d", class, len(calls))
	}
	if class, calls := run(t, "instance", good); class != provider.ClassOK || len(calls) != 1 {
		t.Errorf("instance: class = %q, calls = %d", class, len(calls))
	}
	// A website the token does not see fails the test.
	var calls []call
	env := newEnvironment(t, &calls, good.handler(t))
	resolved := mustResolve(t, env, "one")
	resolved.Targets = []string{"site/7"}
	if class, err := TestConnection(context.Background(), resolved, resolver(env.red, nil), env.red); err != nil ||
		class != provider.ClassPermission {
		t.Errorf("missing target: class = %q, err = %v", class, err)
	}
	// An unusable target fails before the secret and any request.
	calls = nil
	reads := 0
	resolved.Targets = []string{"site", "site/1"}
	if class, err := TestConnection(context.Background(), resolved, resolver(env.red, &reads), env.red); err != nil ||
		class != provider.ClassProviderError || reads != 0 || len(calls) != 0 {
		t.Errorf("bad targets: class = %q, err = %v, reads = %d, calls = %d", class, err, reads, len(calls))
	}
}

// userURL builds an address with user info for the refusal tests.
func userURL(user *url.Userinfo) string {
	return (&url.URL{Scheme: "https", User: user, Host: "analytics.example.test"}).String()
}

func TestBaseURLMustBeABareHTTPSAddress(t *testing.T) {
	for raw, want := range map[string]string{
		"https://analytics.example.test":         "https://analytics.example.test",
		"https://analytics.example.test/":        "https://analytics.example.test",
		"https://analytics.example.test/matomo/": "https://analytics.example.test/matomo",
		"https://analytics.example.test:8443/m":  "https://analytics.example.test:8443/m",
		"https://acme.matomo.cloud":              "https://acme.matomo.cloud",
		"http://analytics.example.test":          "",
		userURL(url.UserPassword("u", "p")):      "",
		userURL(url.User("u")):                   "",
		"https://analytics.example.test?x=1":     "",
		"https://analytics.example.test/m?x=1":   "",
		"https://analytics.example.test?":        "",
		"https://analytics.example.test#f":       "",
		"https://analytics.example.test/#":       "",
		"":                                       "",
		"analytics.example.test":                 "",
		"https:///matomo":                        "",
		"https://analytics.example.test/a b":     "",
		"https://analytics.example.test\\evil":   "",
	} {
		got, err := originOf(raw)
		if (err == nil) != (want != "") || got != want {
			t.Errorf("originOf(%q) = %q, %v, want %q", raw, got, err, want)
		}
		if err != nil && strings.Contains(err.Error(), "evil") {
			t.Errorf("the error quotes the value: %v", err)
		}
	}
}

func TestUnusableBaseURLStopsBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, versionOnly("5.14.2").handler(t))
	for _, raw := range []string{"http://analytics.example.test", userURL(url.UserPassword("user", "pw")),
		"https://analytics.example.test?token_auth=x", "https://analytics.example.test#f", ""} {
		resolved := mustResolve(t, env, "open")
		resolved.BaseURL = raw
		reads := 0
		_, err := Open(context.Background(), resolved, resolver(env.red, &reads), env.red)
		if classOf(err) != provider.ClassProviderError || reads != 0 || len(calls) != 0 ||
			strings.Contains(err.Error(), "pw") || strings.Contains(err.Error(), "token_auth") {
			t.Errorf("base URL %q: err = %v, reads = %d, calls = %d", raw, err, reads, len(calls))
		}
	}
}

func TestConfigurationRequiresTheBaseURL(t *testing.T) {
	reg := registry(t)
	document := func(service string) string {
		return `version: 1
services:
  analytics:
    provider: matomo
` + service + `credentials:
  reader:
    provider: matomo
    type: env
    values:
      token: QATLAS_MATOMO_TOKEN
connections:
  stats: {service: analytics, credential: reader}
`
	}
	for _, service := range []string{"    base_url: https://analytics.example.test/matomo\n",
		"    base_url: https://acme.matomo.cloud\n"} {
		if _, err := config.Decode(strings.NewReader(document(service)), reg); err != nil {
			t.Errorf("service %q was refused: %v", service, err)
		}
	}
	for _, service := range []string{"", "    base_url: http://analytics.example.test\n",
		"    base_url: https://analytics.example.test?x=1\n"} {
		_, err := config.Decode(strings.NewReader(document(service)), reg)
		if err == nil || !strings.Contains(err.Error(), "services.analytics.base_url") {
			t.Errorf("service %q: err = %v, want a base_url problem", service, err)
		}
	}
}

func TestRedirectIsRefused(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(call) (*http.Response, error) {
		response := respond(http.StatusFound, "")
		response.Header.Set("Location", "https://evil.example.test/index.php")
		return response, nil
	})
	_, err := env.invoke("matomo.version.get", "open", `{}`)
	if classOf(err) != provider.ClassProviderError || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %d", err, len(calls))
	}
}

func TestPagingHelper(t *testing.T) {
	binding := provider.CursorBinding("x")
	p, err := startPage(binding, 3, "")
	if err != nil || p.want() != 4 {
		t.Fatalf("page = %+v, %v", p, err)
	}
	rows := []int{1, 2, 3, 4}
	batch, next := slice(p, rows)
	if !slices.Equal(batch, []int{1, 2, 3}) || next == "" {
		t.Fatalf("batch = %v, next = %q", batch, next)
	}
	following, err := startPage(binding, 0, next)
	if err != nil || following.offset != 3 || following.limit != 3 || following.want() != 7 {
		t.Fatalf("following = %+v, %v", following, err)
	}
	// A method with filter_offset answers from the offset on.
	batch, next = sliceWindow(following, []int{4, 5})
	if !slices.Equal(batch, []int{4, 5}) || next != "" {
		t.Fatalf("window = %v, %q", batch, next)
	}
	params := map[string][]string{}
	following.serverWindow(params)
	if params["filter_offset"][0] != "3" || params["filter_limit"][0] != "4" {
		t.Fatalf("server window = %v", params)
	}
	if _, err := startPage(provider.CursorBinding("y"), 0, next); err == nil && next != "" {
		t.Error("a cursor of another binding was accepted")
	}
}
