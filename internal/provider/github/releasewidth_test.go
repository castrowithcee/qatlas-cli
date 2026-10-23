package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// The release, release asset, ruleset, and custom property objects of GitHub's REST reference; the members the
// examples leave out (immutable, mentions_count, reactions, state, and the ruleset timestamps and
// permission) are filled with the documented shapes.
const (
	documentedReleaseAsset = `{"url":"https://api.github.com/repos/octocat/Hello-World/releases/assets/1",` +
		`"browser_download_url":"https://github.com/octocat/Hello-World/releases/download/v1.0.0/example.zip",` +
		`"id":1,"node_id":"MDEyOlJlbGVhc2VBc3NldDE=","name":"example.zip","label":"short description",` +
		`"state":"uploaded","content_type":"application/zip","size":1024,"digest":"sha256:abc",` +
		`"download_count":42,"created_at":"2013-02-27T19:35:32Z","updated_at":"2013-02-27T19:35:32Z",` +
		`"uploader":` + documentedAccount + `}`
	documentedRelease = `{"url":"https://api.github.com/repos/octocat/Hello-World/releases/1",` +
		`"html_url":"https://github.com/octocat/Hello-World/releases/v1.0.0",` +
		`"assets_url":"https://api.github.com/repos/octocat/Hello-World/releases/1/assets",` +
		`"upload_url":"https://uploads.github.com/repos/octocat/Hello-World/releases/1/assets{?name,label}",` +
		`"tarball_url":"https://api.github.com/repos/octocat/Hello-World/tarball/v1.0.0",` +
		`"zipball_url":"https://api.github.com/repos/octocat/Hello-World/zipball/v1.0.0",` +
		`"discussion_url":"https://github.com/octocat/Hello-World/discussions/90","id":1,` +
		`"node_id":"MDc6UmVsZWFzZTE=","tag_name":"v1.0.0","target_commitish":"master","name":"v1.0.0",` +
		`"body":"Description of the release","body_html":"<p>x</p>","body_text":"x","draft":false,` +
		`"prerelease":false,"immutable":true,"created_at":"2013-02-27T19:35:32Z",` +
		`"published_at":"2013-02-27T19:35:32Z","updated_at":"2013-03-01T10:00:00Z","author":` + documentedAccount + `,` +
		`"assets":[` + documentedReleaseAsset + `],"mentions_count":2,` +
		`"reactions":{"url":"https://api.github.com/repos/octocat/Hello-World/releases/1/reactions",` +
		`"total_count":5,"+1":3,"-1":0,"laugh":1,"confused":0,"heart":1,"hooray":0,"eyes":0,"rocket":0}}`

	assetWant = `{"id":1,"node_id":"MDEyOlJlbGVhc2VBc3NldDE=","name":"example.zip","label":"short description",` +
		`"state":"uploaded","size_bytes":1024,"content_type":"application/zip","digest":"sha256:abc",` +
		`"download_count":42,"created_at":"2013-02-27T19:35:32Z","updated_at":"2013-02-27T19:35:32Z",` +
		`"uploader":` + accountWant + `,"links":{"url":"https://api.github.com/repos/octocat/Hello-World/releases/assets/1",` +
		`"browser_download_url":"https://github.com/octocat/Hello-World/releases/download/v1.0.0/example.zip"}}`
	releaseWidthWant = `"node_id":"MDc6UmVsZWFzZTE=","updated_at":"2013-03-01T10:00:00Z","immutable":true,` +
		`"mentions_count":2,"reactions":{"total_count":5,"plus_one":3,"minus_one":0,"laugh":1,"confused":0,` +
		`"heart":1,"hooray":0,"eyes":0,"rocket":0,` +
		`"links":{"url":"https://api.github.com/repos/octocat/Hello-World/releases/1/reactions"}},` +
		`"author_detail":` + accountWant + `,"assets":[` + assetWant + `],` +
		`"links":{"url":"https://api.github.com/repos/octocat/Hello-World/releases/1",` +
		`"assets_url":"https://api.github.com/repos/octocat/Hello-World/releases/1/assets",` +
		`"upload_url":"https://uploads.github.com/repos/octocat/Hello-World/releases/1/assets{?name,label}",` +
		`"tarball_url":"https://api.github.com/repos/octocat/Hello-World/tarball/v1.0.0",` +
		`"zipball_url":"https://api.github.com/repos/octocat/Hello-World/zipball/v1.0.0",` +
		`"discussion_url":"https://github.com/octocat/Hello-World/discussions/90"}`
	releaseCompactWant = `"id":1,"tag":"v1.0.0","name":"v1.0.0","draft":false,"prerelease":false,"author":"octocat",` +
		`"created_at":"2013-02-27T19:35:32Z","published_at":"2013-02-27T19:35:32Z",` +
		`"url":"https://github.com/octocat/Hello-World/releases/v1.0.0"`
	releaseGetWant = `{` + releaseCompactWant + `,"body":"Description of the release","target_commitish":"master",` +
		`"assets_count":1,` + releaseWidthWant + `,"repository":"octo-org/example"}`
	releaseListWant = `{"releases":[{` + releaseCompactWant + `,"target_commitish":"master",` +
		`"body":"Description of the release","assets_count":1,` + releaseWidthWant + `}],"has_more":false,` +
		`"repository":"octo-org/example"}`

	documentedRuleset = `{"id":42,"name":"super cool ruleset","target":"branch","source_type":"Repository",` +
		`"source":"monalisa/my-repo","enforcement":"active",` +
		`"bypass_actors":[{"actor_id":234,"actor_type":"Team","bypass_mode":"always"}],` +
		`"current_user_can_bypass":"always","node_id":"RRS_lACkVXNlcgQB","created_at":"2023-07-15T08:43:03Z",` +
		`"updated_at":"2023-08-23T16:29:47Z","_links":{"self":{"href":"https://api.github.com/repos/monalisa/my-repo/rulesets/42"},` +
		`"html":{"href":"https://github.com/monalisa/my-repo/rules/42"}},` +
		`"conditions":{"ref_name":{"include":["refs/heads/main","refs/heads/master"],"exclude":["refs/heads/dev*"]}},` +
		`"rules":[{"type":"commit_author_email_pattern","parameters":{"operator":"contains","pattern":"github"}}]}`
	rulesetWidthWant = `"node_id":"RRS_lACkVXNlcgQB","source_type":"Repository",` +
		`"current_user_can_bypass":"always","created_at":"2023-07-15T08:43:03Z","updated_at":"2023-08-23T16:29:47Z",` +
		`"url":"https://github.com/monalisa/my-repo/rules/42",` +
		`"links":{"url":"https://api.github.com/repos/monalisa/my-repo/rulesets/42"}`
	rulesetBodyWant = `"conditions":{"ref_name":{"include":["refs/heads/main","refs/heads/master"],` +
		`"exclude":["refs/heads/dev*"]}},"rules":[{"type":"commit_author_email_pattern","parameters":` +
		`{"operator":"contains","pattern":"github"}}],"bypass_actors":[{"actor_id":234,"actor_type":"Team",` +
		`"bypass_mode":"always"}]`
	rulesetGetWant = `{"id":42,"name":"super cool ruleset","target":"branch","enforcement":"active",` +
		`"source":"monalisa/my-repo",` + rulesetWidthWant + `,` + rulesetBodyWant + `,"repository":"octo-org/example"}`
	rulesetListWant = `{"rulesets":[{"id":42,"name":"super cool ruleset","target":"branch","enforcement":"active",` +
		`"source":"octo-org/example","source_name":"monalisa/my-repo",` + rulesetWidthWant + `,` + rulesetBodyWant +
		`}],"repository":"octo-org/example","has_more":false}`

	documentedPropertySchema = `[{"property_name":"environment","url":"https://api.github.com/orgs/github/properties/schema/environment",` +
		`"source_type":"organization","value_type":"single_select","required":true,"default_value":"production",` +
		`"description":"Prod or dev environment","allowed_values":["production","development"],` +
		`"values_editable_by":"org_actors","require_explicit_values":true}]`
	propertySchemaWant = `{"schema":[{"property_name":"environment","source_type":"organization",` +
		`"value_type":"single_select","required":true,"default_value":"production",` +
		`"description":"Prod or dev environment","allowed_values":["production","development"],` +
		`"values_editable_by":"org_actors","require_explicit_values":true,` +
		`"links":{"url":"https://api.github.com/orgs/github/properties/schema/environment"}}],` +
		`"organization":"orgs/octo-org"}`
)

func releaseCore(t *testing.T, routes map[string]string) (*fakeGitHub, *application.Core) {
	t.Helper()
	f, base := serveRoutes(t, routes)
	red := &redact.Redactor{}
	return f, application.New(registry(t), rulesetsConfig(base), resolver(red, nil), red)
}

// The release tools report every member GitHub documents for a release and a release asset.
func TestReleaseToolsReportEveryDocumentedMember(t *testing.T) {
	const prefix = "/api/v3/repos/octo-org/example/releases"
	_, core := releaseCore(t, map[string]string{prefix: "[" + documentedRelease + "]",
		prefix + "/1": documentedRelease, prefix + "/1/assets": "[" + documentedReleaseAsset + "]"})

	got, err := invoke(t, core, "github.releases.get", "repo", `{"id":1}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "releases.get", got, releaseGetWant)
	got, err = invoke(t, core, "github.releases.list", "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "releases.list", got, releaseListWant)
	got, err = invoke(t, core, "github.release-assets.list", "repo", `{"id":1}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "release-assets.list", got, `{"assets":[`+assetWant+`],"has_more":false,"repository":"octo-org/example"}`)
}

// A release answer without the new members keeps its earlier output, in the reads and in the changes.
func TestReleaseToolsWithoutNewMembersKeepTheirOldAnswer(t *testing.T) {
	const old = `{"id":3,"tag_name":"v3","target_commitish":"main","name":null,"body":"b","draft":true,` +
		`"prerelease":false,"author":{"login":"octocat"},"assets":[{"id":9,"name":"a","label":null,"size":5,` +
		`"content_type":"text/plain","digest":null,"download_count":1,"created_at":"c","updated_at":"u"}],` +
		`"created_at":"2026-01-01T00:00:00Z","published_at":null,"html_url":"https://github.com/octo-org/example/releases/tag/v3"}`
	// The author object behind the flat login joins it as author_detail.
	const compact = `"id":3,"tag":"v3","draft":true,"prerelease":false,"author":"octocat","author_detail":{"login":"octocat"},` +
		`"created_at":"2026-01-01T00:00:00Z","url":"https://github.com/octo-org/example/releases/tag/v3"`
	const asset = `{"id":9,"name":"a","size_bytes":5,"content_type":"text/plain","download_count":1,` +
		`"created_at":"c","updated_at":"u"}`
	const prefix = "/api/v3/repos/octo-org/example/releases"
	_, core := releaseCore(t, map[string]string{prefix: "[" + old + "]", prefix + "/3": old,
		prefix + "/3/assets": "[" + old[strings.Index(old, `{"id":9`):strings.Index(old, `],"created_at"`)] + "]"})

	got, err := invoke(t, core, "github.releases.get", "repo", `{"id":3}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "releases.get", got, `{`+compact+`,"body":"b","target_commitish":"main","assets_count":1,`+
		`"assets":[`+asset+`],"repository":"octo-org/example"}`)
	got, err = invoke(t, core, "github.releases.list", "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "releases.list", got, `{"releases":[{`+compact+`,"target_commitish":"main","body":"b",`+
		`"assets_count":1,"assets":[`+asset+`]}],"has_more":false,"repository":"octo-org/example"}`)

	// A list answer that carries no asset list and no body states neither.
	_, core = releaseCore(t, map[string]string{prefix: `[{"id":4,"tag_name":"v4","draft":false,"prerelease":false}]`})
	got, err = invoke(t, core, "github.releases.list", "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "minimal list", got, `{"releases":[{"id":4,"tag":"v4","draft":false,"prerelease":false}],`+
		`"has_more":false,"repository":"octo-org/example"}`)
}

// Text GitHub sent is cleaned, a long body is cut and marked, and a list of assets beyond the bound is cut.
func TestReleaseListBoundsForeignText(t *testing.T) {
	assets := make([]string, 0, 101)
	for i := 1; i <= 101; i++ {
		assets = append(assets, fmt.Sprintf(`{"id":%d,"name":"a%d","size":1,"download_count":0}`, i, i))
	}
	body, _ := json.Marshal(strings.Repeat("x", 4100))
	release := `{"id":1,"tag_name":"v1","draft":false,"prerelease":false,"body":` + string(body) +
		`,"assets":[` + strings.Join(assets, ",") + `]}`
	_, core := releaseCore(t, map[string]string{"/api/v3/repos/octo-org/example/releases": "[" + release + "]"})
	got, err := invoke(t, core, "github.releases.list", "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Releases []struct {
			Body            string           `json:"body"`
			BodyTruncated   bool             `json:"body_truncated"`
			AssetsCount     int              `json:"assets_count"`
			Assets          []map[string]any `json:"assets"`
			AssetsTruncated bool             `json:"assets_truncated"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(got, &list); err != nil || len(list.Releases) != 1 {
		t.Fatalf("list = %s, %v", got, err)
	}
	r := list.Releases[0]
	if len([]rune(r.Body)) != 4000 || !r.BodyTruncated || r.AssetsCount != 101 || len(r.Assets) != 100 || !r.AssetsTruncated {
		t.Errorf("body %d truncated %v, assets %d of %d truncated %v", len([]rune(r.Body)), r.BodyTruncated,
			len(r.Assets), r.AssetsCount, r.AssetsTruncated)
	}
}

// A ruleset read reports every member GitHub documents; the list adds the free-form members as GitHub lists
// them, and the earlier answer stays when GitHub sends none of the new members.
func TestRulesetToolsReportEveryDocumentedMember(t *testing.T) {
	const prefix = "/api/v3/repos/octo-org/example/rulesets"
	_, core := releaseCore(t, map[string]string{prefix: "[" + documentedRuleset + "]", prefix + "/42": documentedRuleset})

	got, err := invoke(t, core, "github.rulesets.get", "repo", `{"repository":"octo-org/example","id":42}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "rulesets.get", got, rulesetGetWant)
	got, err = invoke(t, core, "github.rulesets.list", "repo", `{"repository":"octo-org/example"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "rulesets.list", got, rulesetListWant)

	const old = `{"id":7,"name":"n","target":"branch","enforcement":"active"}`
	_, core = releaseCore(t, map[string]string{prefix: "[" + old + "]", prefix + "/7": old})
	got, err = invoke(t, core, "github.rulesets.list", "repo", `{"repository":"octo-org/example"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "old list", got, `{"rulesets":[{"id":7,"name":"n","target":"branch","enforcement":"active",`+
		`"source":"octo-org/example"}],"repository":"octo-org/example","has_more":false}`)
	// The application leaves the empty conditions, rules, and bypass actors out of every answer.
	got, err = invoke(t, core, "github.rulesets.get", "repo", `{"repository":"octo-org/example","id":7}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "old get", got, `{"id":7,"name":"n","target":"branch","enforcement":"active",`+
		`"repository":"octo-org/example"}`)
}

// A free-form ruleset member of a list entry beyond the bounds is left out and marked, never cut.
func TestRulesetListOmitsAnOversizedFreeFormMember(t *testing.T) {
	rules := make([]string, 0, 1100)
	for i := 0; i < 1100; i++ {
		rules = append(rules, `{"type":"deletion"}`)
	}
	deep := `{"a":{"b":{"c":{"d":{"e":{"f":{"g":{"h":{"i":1}}}}}}}}}`
	entry := `{"id":5,"name":"n","target":"branch","enforcement":"active","rules":[` + strings.Join(rules, ",") +
		`],"conditions":` + deep + `,"bypass_actors":[{"actor_id":1,"actor_type":"Team","bypass_mode":"always"}]}`
	_, core := releaseCore(t, map[string]string{"/api/v3/repos/octo-org/example/rulesets": "[" + entry + "]"})
	got, err := invoke(t, core, "github.rulesets.list", "repo", `{"repository":"octo-org/example"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "rulesets.list", got, `{"rulesets":[{"id":5,"name":"n","target":"branch","enforcement":"active",`+
		`"source":"octo-org/example","conditions_omitted":true,"rules_omitted":true,`+
		`"bypass_actors":[{"actor_id":1,"actor_type":"Team","bypass_mode":"always"}]}],`+
		`"repository":"octo-org/example","has_more":false}`)
}

// targets and includes_parents reach GitHub, targets stands in the cursor fingerprint, and every refused
// value fails before a credential is resolved.
func TestRulesetFiltersReachTheRequestAndTheCursor(t *testing.T) {
	const prefix = "/api/v3/repos/octo-org/example/rulesets"
	f := &fakeGitHub{}
	f.failure = func(w http.ResponseWriter, req *http.Request) bool {
		if !strings.HasPrefix(req.URL.Path, prefix) {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Path == prefix {
			w.Header().Set("Link", `<https://api.github.com/x?page=2>; rel="next"`)
			fmt.Fprint(w, "["+documentedRuleset+"]")
			return true
		}
		fmt.Fprint(w, documentedRuleset)
		return true
	}
	base := serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), rulesetsConfig(base), resolver(red, &reads), red)

	first, err := invoke(t, core, "github.rulesets.list", "repo",
		`{"repository":"octo-org/example","targets":["push","branch"],"limit":1}`, false)
	if err != nil {
		t.Fatal(err)
	}
	query, _ := url.ParseQuery(lastRequest(t, f).query)
	if query.Get("targets") != "branch,push" || query.Get("includes_parents") != "true" {
		t.Errorf("query = %v, want targets branch,push", query)
	}
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(first, &page); err != nil || page.NextCursor == "" {
		t.Fatalf("first page = %s, %v", first, err)
	}
	// The same filters in another order continue; other or no filters refuse the cursor.
	if _, err := invoke(t, core, "github.rulesets.list", "repo",
		`{"repository":"octo-org/example","targets":["branch","push"],"cursor":"`+page.NextCursor+`"}`, false); err != nil {
		t.Errorf("continuation with the same targets = %v", err)
	}
	for _, other := range []string{`"targets":["branch"],`, `"targets":["tag","push"],`, ``} {
		if _, err := invoke(t, core, "github.rulesets.list", "repo",
			`{"repository":"octo-org/example",`+other+`"cursor":"`+page.NextCursor+`"}`, false); !isInvalidRequest(err) {
			t.Errorf("continuation with %q = %v, want an invalid request", other, err)
		}
	}

	reads = 0
	for _, bad := range []string{`"targets":[]`, `"targets":["branch","branch"]`, `"targets":["repository"]`,
		`"targets":["branch","tag","push","tag"]`} {
		if _, err := invoke(t, core, "github.rulesets.list", "repo",
			`{"repository":"octo-org/example",`+bad+`}`, false); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want an invalid request", bad, err)
		}
	}
	if _, err := invoke(t, core, "github.rulesets.get", "org",
		`{"organization":"orgs/octo-org","id":42,"includes_parents":true}`, false); !isInvalidRequest(err) {
		t.Errorf("includes_parents for an organization = %v, want an invalid request", err)
	}
	if reads != 0 {
		t.Errorf("credential reads = %d, want none before the refusals", reads)
	}

	for _, tt := range []struct{ arguments, want string }{
		{`{"repository":"octo-org/example","id":42,"includes_parents":false}`, "includes_parents=false"},
		{`{"repository":"octo-org/example","id":42,"includes_parents":true}`, "includes_parents=true"},
		{`{"repository":"octo-org/example","id":42}`, ""},
	} {
		if _, err := invoke(t, core, "github.rulesets.get", "repo", tt.arguments, false); err != nil {
			t.Fatal(err)
		}
		if got := lastRequest(t, f).query; got != tt.want {
			t.Errorf("get %s: query = %q, want %q", tt.arguments, got, tt.want)
		}
	}
}

// The custom property definitions report their source, the explicit-value rule, and their REST address.
func TestCustomPropertySchemaReportsEveryDocumentedMember(t *testing.T) {
	_, core := releaseCore(t, map[string]string{"/api/v3/orgs/octo-org/properties/schema": documentedPropertySchema})
	got, err := invoke(t, core, "github.custom-properties.get", "org", `{"organization":"orgs/octo-org"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "custom-properties.get", got, propertySchemaWant)

	const old = `[{"property_name":"team","value_type":"string"}]`
	_, core = releaseCore(t, map[string]string{"/api/v3/orgs/octo-org/properties/schema": old})
	got, err = invoke(t, core, "github.custom-properties.get", "org", `{"organization":"orgs/octo-org"}`, false)
	sameJSON(t, "old schema", got, `{"schema":[{"property_name":"team","value_type":"string"}],"organization":"orgs/octo-org"}`)
	if err != nil {
		t.Fatal(err)
	}
}
