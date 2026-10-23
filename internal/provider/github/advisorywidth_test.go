package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// The advisories and the finding documented by GitHub's REST reference (global-advisory,
// repository-advisory, code-quality-finding), with the accounts shortened to documentedAccount.
const (
	documentedGlobalAdvisory = `{"id":1,"ghsa_id":"GHSA-abcd-1234-efgh","cve_id":"CVE-2050-00000",` +
		`"url":"https://api.github.com/advisories/GHSA-abcd-1234-efgh",` +
		`"html_url":"https://github.com/advisories/GHSA-abcd-1234-efgh",` +
		`"repository_advisory_url":"https://api.github.com/repos/project/a-package/security-advisories/GHSA-abcd-1234-efgh",` +
		`"summary":"Heartbleed security advisory","description":"This bug allows an attacker to read memory.",` +
		`"type":"reviewed","severity":"high","source_code_location":"https://github.com/project/a-package",` +
		`"identifiers":[{"type":"GHSA","value":"GHSA-abcd-1234-efgh"},{"type":"CVE","value":"CVE-2050-00000"}],` +
		`"references":["https://nvd.nist.gov/vuln/detail/CVE-2050-00000"],"published_at":"2023-03-23T02:30:56Z",` +
		`"updated_at":"2023-03-24T02:30:56Z","github_reviewed_at":"2023-03-23T02:30:56Z",` +
		`"nvd_published_at":"2023-03-25T02:30:56Z","withdrawn_at":null,` +
		`"vulnerabilities":[{"package":{"ecosystem":"npm","name":"a-package"},"first_patched_version":"1.0.3",` +
		`"vulnerable_version_range":"<=1.0.2","vulnerable_functions":["a_function"]}],` +
		`"cvss":{"vector_string":"CVSS:3.1/AV:N/AC:H/PR:H/UI:R/S:C/C:H/I:H/A:H","score":7.6},` +
		`"cvss_severities":{"cvss_v3":{"vector_string":"CVSS:3.1/AV:N/AC:H/PR:H/UI:R/S:C/C:H/I:H/A:H","score":7.60},` +
		`"cvss_v4":{"vector_string":"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N","score":9.3}},` +
		`"cwes":[{"cwe_id":"CWE-400","name":"Uncontrolled Resource Consumption"}],` +
		`"epss":{"percentage":0.00045,"percentile":"0.16001e0"},` +
		`"credits":[{"user":` + documentedAccount + `,"type":"analyst"}]}`

	documentedRepositoryAdvisory = `{"ghsa_id":"GHSA-abcd-1234-efgh","cve_id":"CVE-2050-00000",` +
		`"url":"https://api.github.com/repos/repo/a-package/security-advisories/GHSA-abcd-1234-efgh",` +
		`"html_url":"https://github.com/repo/a-package/security/advisories/GHSA-abcd-1234-efgh",` +
		`"summary":"A short summary of the advisory.","description":"A detailed description.","severity":"critical",` +
		`"author":` + documentedAccount + `,"publisher":` + documentedAccount + `,` +
		`"identifiers":[{"type":"GHSA","value":"GHSA-abcd-1234-efgh"},{"type":"CVE","value":"CVE-2050-00000"}],` +
		`"state":"published","created_at":"2020-01-01T00:00:00Z","updated_at":"2020-01-02T00:00:00Z",` +
		`"published_at":"2020-01-03T00:00:00Z","closed_at":null,"withdrawn_at":null,"submission":{"accepted":false},` +
		`"vulnerabilities":[{"package":{"ecosystem":"pip","name":"a-package"},"vulnerable_version_range":">= 1.0.0, < 1.0.1",` +
		`"patched_versions":"1.0.1","vulnerable_functions":["function1"]}],` +
		`"cvss":{"vector_string":"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H","score":9.8},` +
		`"cvss_severities":{"cvss_v3":{"vector_string":"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H","score":9.8},` +
		`"cvss_v4":{"vector_string":"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N","score":9.3}},` +
		`"cwes":[{"cwe_id":"CWE-123","name":"A CWE"}],"cwe_ids":["CWE-123"],` +
		`"credits":[{"login":"octocat","type":"analyst"}],` +
		`"credits_detailed":[{"user":` + documentedAccount + `,"type":"analyst","state":"accepted"}],` +
		`"collaborating_users":[` + documentedAccount + `],` +
		`"collaborating_teams":[{"name":"Justice League","id":1,"node_id":"MDQ6VGVhbTE=","slug":"justice-league",` +
		`"description":"A great team.","privacy":"closed","notification_setting":"notifications_enabled",` +
		`"url":"https://api.github.com/teams/1","html_url":"https://github.com/orgs/github/teams/justice-league",` +
		`"members_url":"https://api.github.com/teams/1/members{/member}",` +
		`"repositories_url":"https://api.github.com/teams/1/repos","permission":"admin","parent":null}],` +
		`"private_fork":{"id":217723378,"node_id":"MDEwOlJlcG9zaXRvcnkyMTc3MjMzNzg=",` +
		`"name":"octo-repo-ghsa-abcd-1234-efgh","full_name":"octo-org/octo-repo-ghsa-abcd-1234-efgh",` +
		`"owner":` + documentedAccount + `,"private":true,` +
		`"html_url":"https://github.com/octo-org/octo-repo-ghsa-abcd-1234-efgh","description":null,"fork":false,` +
		`"url":"https://api.github.com/repos/octo-org/octo-repo-ghsa-abcd-1234-efgh",` +
		`"forks_url":"https://api.github.com/repos/octo-org/octo-repo-ghsa-abcd-1234-efgh/forks",` +
		`"temp_clone_token":"clone-token-canary"}}`

	documentedFinding = `{"number":42,"state":"open","url":"https://api.github.com/repos/octocat/hello-world/code-quality/findings/42",` +
		`"rule":{"id":"java/useless-null-check","title":"Useless null check","description":"Checking.",` +
		`"help":"# Useless null check","severity":"warning","category":"maintainability"},` +
		`"location":{"path":"java/UselessNullCheck.java","start_line":9,"start_column":4,"end_line":9,"end_column":18},` +
		`"message":{"text":"This check is useless.","markdown":"This check is useless. [o](java/UselessNullCheck.java#L9C4-L9C18)"},` +
		`"created_at":"2026-01-23T12:34:56Z"}`
)

// widthFake answers the advisory routes with fixed bodies and records the raw query of each request.
type widthFake struct {
	global, repository, finding string
	queries                     []string
}

func (w *widthFake) route(rw http.ResponseWriter, req *http.Request) bool {
	path := strings.TrimPrefix(req.URL.Path, "/api/v3")
	var body string
	switch {
	case path == "/advisories":
		body = "[" + w.global + "]"
	case strings.HasPrefix(path, "/advisories/"):
		body = w.global
	case strings.HasSuffix(path, "/security-advisories"):
		body = "[" + w.repository + "]"
	case strings.Contains(path, "/code-quality/findings/"):
		body = w.finding
	default:
		return false
	}
	w.queries = append(w.queries, path+"?"+req.URL.RawQuery)
	rw.Header().Set("Content-Type", "application/json")
	if strings.HasSuffix(path, "/advisories") || strings.HasSuffix(path, "/security-advisories") {
		rw.Header().Set("Link", `<https://api.github.com/advisories?per_page=1&after=Y3Vyc29yOnYy>; rel="next"`)
	}
	fmt.Fprint(rw, body)
	return true
}

func serveWidth(t *testing.T, w *widthFake) (*application.Core, *int) {
	t.Helper()
	base := serve(t, &fakeGitHub{failure: w.route})
	reads := 0
	red := &redact.Redactor{}
	return application.New(registry(t), discoveryConfig(base), resolver(red, &reads), red), &reads
}

func asObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("not an object: %s", raw)
	}
	return out
}

func firstAdvisory(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	list, _ := asObject(t, raw)["advisories"].([]any)
	if len(list) != 1 {
		t.Fatalf("want one advisory in %s", raw)
	}
	return list[0].(map[string]any)
}

func wantMember(t *testing.T, got map[string]any, name string, want any) {
	t.Helper()
	if !reflect.DeepEqual(got[name], want) {
		t.Errorf("%s = %#v, want %#v", name, got[name], want)
	}
}

func TestGlobalAdvisoryWidthCarriesEveryDocumentedMember(t *testing.T) {
	core, _ := serveWidth(t, &widthFake{global: documentedGlobalAdvisory})
	for _, tt := range []struct {
		id, arguments string
		read          func([]byte) map[string]any
	}{
		{globalAdvisoriesGet.ID, `{"ghsa_id":"` + testGHSA + `"}`, func(b []byte) map[string]any { return asObject(t, b) }},
		{globalAdvisoriesList.ID, `{}`, func(b []byte) map[string]any { return firstAdvisory(t, b) }},
	} {
		raw, err := invoke(t, core, tt.id, "open", tt.arguments, false)
		if err != nil {
			t.Fatalf("%s: %v", tt.id, err)
		}
		got := tt.read(raw)
		wantMember(t, got, "url", "https://github.com/advisories/GHSA-abcd-1234-efgh")
		wantMember(t, got, "identifiers", []any{
			map[string]any{"type": "GHSA", "value": "GHSA-abcd-1234-efgh"},
			map[string]any{"type": "CVE", "value": "CVE-2050-00000"}})
		wantMember(t, got, "cwes", []any{"CWE-400"})
		wantMember(t, got, "cwes_details", []any{map[string]any{"cwe_id": "CWE-400", "name": "Uncontrolled Resource Consumption"}})
		wantMember(t, got, "links", map[string]any{"url": "https://api.github.com/advisories/GHSA-abcd-1234-efgh",
			"repository_advisory_url": "https://api.github.com/repos/project/a-package/security-advisories/GHSA-abcd-1234-efgh"})
		wantMember(t, got, "cvss_score", 7.6)
		wantMember(t, got, "epss_percentage", 0.00045)
		wantMember(t, got, "epss_percentile", 0.16001)
		credits, _ := got["credits"].([]any)
		if len(credits) != 1 {
			t.Fatalf("credits = %v", got["credits"])
		}
		credit := credits[0].(map[string]any)
		user := credit["user"].(map[string]any)
		wantMember(t, credit, "type", "analyst")
		wantMember(t, user, "login", "octocat")
		wantMember(t, user, "url", "https://github.com/octocat")
		wantMember(t, user["links"].(map[string]any), "url", "https://api.github.com/users/octocat")
		severities := got["cvss_severities"].(map[string]any)
		wantMember(t, severities["cvss_v3"].(map[string]any), "vector_string", "CVSS:3.1/AV:N/AC:H/PR:H/UI:R/S:C/C:H/I:H/A:H")
		wantMember(t, severities["cvss_v4"].(map[string]any), "vector_string",
			"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N")
		wantMember(t, severities["cvss_v4"].(map[string]any), "score", 9.3)
		// The scores keep the number as GitHub wrote it.
		if !strings.Contains(string(raw), `"score":7.60`) {
			t.Errorf("%s lacks the CVSS 3 score as sent: %s", tt.id, raw)
		}
	}
}

func TestRepositoryAdvisoryWidthCarriesEveryDocumentedMember(t *testing.T) {
	core, _ := serveWidth(t, &widthFake{repository: documentedRepositoryAdvisory})
	for _, tt := range []struct{ id, connection, arguments string }{
		{repositoryAdvisoriesList.ID, "repo", `{}`},
		{organizationAdvisoriesList.ID, "org", `{}`},
	} {
		raw, err := invoke(t, core, tt.id, tt.connection, tt.arguments, false)
		if err != nil {
			t.Fatalf("%s: %v", tt.id, err)
		}
		got := firstAdvisory(t, raw)
		wantMember(t, got, "url", "https://github.com/repo/a-package/security/advisories/GHSA-abcd-1234-efgh")
		wantMember(t, got, "links", map[string]any{"url": "https://api.github.com/repos/repo/a-package/security-advisories/GHSA-abcd-1234-efgh"})
		wantMember(t, got, "author", "octocat")
		wantMember(t, got, "publisher", "octocat")
		wantMember(t, got["author_detail"].(map[string]any), "login", "octocat")
		wantMember(t, got["publisher_detail"].(map[string]any), "url", "https://github.com/octocat")
		wantMember(t, got, "submission", map[string]any{"accepted": false})
		wantMember(t, got, "cwes", []any{"CWE-123"})
		wantMember(t, got, "cwe_ids", []any{"CWE-123"})
		wantMember(t, got, "cwes_details", []any{map[string]any{"cwe_id": "CWE-123", "name": "A CWE"}})
		wantMember(t, got, "credits", []any{map[string]any{"login": "octocat", "type": "analyst"}})
		wantMember(t, got, "identifiers", []any{
			map[string]any{"type": "GHSA", "value": "GHSA-abcd-1234-efgh"},
			map[string]any{"type": "CVE", "value": "CVE-2050-00000"}})
		detailed := got["credits_detailed"].([]any)[0].(map[string]any)
		wantMember(t, detailed, "type", "analyst")
		wantMember(t, detailed, "state", "accepted")
		wantMember(t, detailed["user"].(map[string]any), "login", "octocat")
		wantMember(t, got["collaborating_users"].([]any)[0].(map[string]any), "login", "octocat")
		team := got["collaborating_teams"].([]any)[0].(map[string]any)
		wantMember(t, team, "id", float64(1))
		wantMember(t, team, "node_id", "MDQ6VGVhbTE=")
		wantMember(t, team, "name", "Justice League")
		wantMember(t, team, "slug", "justice-league")
		wantMember(t, team, "description", "A great team.")
		wantMember(t, team, "privacy", "closed")
		wantMember(t, team, "notification_setting", "notifications_enabled")
		wantMember(t, team, "permission", "admin")
		wantMember(t, team, "url", "https://github.com/orgs/github/teams/justice-league")
		wantMember(t, team["links"].(map[string]any), "url", "https://api.github.com/teams/1")
		severities := got["cvss_severities"].(map[string]any)
		wantMember(t, severities["cvss_v3"].(map[string]any), "score", 9.8)
		wantMember(t, severities["cvss_v4"].(map[string]any), "score", 9.3)
		wantMember(t, severities["cvss_v4"].(map[string]any), "vector_string",
			"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N")
		if strings.Contains(string(raw), "clone-token-canary") || strings.Contains(string(raw), "temp_clone_token") {
			t.Errorf("%s shows the clone token: %s", tt.id, raw)
		}
	}
}

func TestPrivateForkOutsideTheTargetsKeepsOnlyIdentifiers(t *testing.T) {
	core, _ := serveWidth(t, &widthFake{repository: documentedRepositoryAdvisory})
	// The connection names one repository; the fork is another one.
	raw, err := invoke(t, core, repositoryAdvisoriesList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	fork := firstAdvisory(t, raw)["private_fork"].(map[string]any)
	if !reflect.DeepEqual(fork, map[string]any{"id": float64(217723378), "node_id": "MDEwOlJlcG9zaXRvcnkyMTc3MjMzNzg=",
		"full_name": "octo-org/octo-repo-ghsa-abcd-1234-efgh"}) {
		t.Errorf("fork outside the targets = %v, want identifiers only", fork)
	}
	// An organization target admits the fork, which then shows in full.
	raw, err = invoke(t, core, organizationAdvisoriesList.ID, "org", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	fork = firstAdvisory(t, raw)["private_fork"].(map[string]any)
	wantMember(t, fork, "name", "octo-repo-ghsa-abcd-1234-efgh")
	wantMember(t, fork, "private", true)
	wantMember(t, fork, "url", "https://github.com/octo-org/octo-repo-ghsa-abcd-1234-efgh")
	wantMember(t, fork["links"].(map[string]any), "forks_url",
		"https://api.github.com/repos/octo-org/octo-repo-ghsa-abcd-1234-efgh/forks")
}

func TestFindingMessageDetail(t *testing.T) {
	core, _ := serveWidth(t, &widthFake{finding: documentedFinding})
	raw, err := invoke(t, core, codeQualityFindingsGet.ID, "repo", `{"finding_number":42}`, false)
	if err != nil {
		t.Fatal(err)
	}
	got := asObject(t, raw)
	wantMember(t, got, "message", "This check is useless.")
	wantMember(t, got, "message_detail", map[string]any{"text": "This check is useless.",
		"markdown": "This check is useless. [o](java/UselessNullCheck.java#L9C4-L9C18)"})
	long := strings.Replace(documentedFinding, `"markdown":"This check is useless. [o](java/UselessNullCheck.java#L9C4-L9C18)"`,
		`"markdown":"`+strings.Repeat("m", findingMarkdownLimit+1)+`"`, 1)
	core, _ = serveWidth(t, &widthFake{finding: long})
	raw, _ = invoke(t, core, codeQualityFindingsGet.ID, "repo", `{"finding_number":42}`, false)
	detail := asObject(t, raw)["message_detail"].(map[string]any)
	if len([]rune(detail["markdown"].(string))) != findingMarkdownLimit || detail["markdown_truncated"] != true {
		t.Errorf("long markdown = %d runes, truncated %v", len(detail["markdown"].(string)), detail["markdown_truncated"])
	}
}

// An answer without the new members yields the output the tools gave before.
func TestAdvisoryAnswersWithoutTheNewMembersStayAsBefore(t *testing.T) {
	core, _ := serveWidth(t, &widthFake{
		global: `{"ghsa_id":"` + testGHSA + `","html_url":"https://x/g","summary":"s","cvss":{"vector_string":"v","score":5.5},` +
			`"epss":{"percentage":1.5,"percentile":80.25},"cwes":[{"cwe_id":"CWE-1"}],"identifiers":[],"credits":[],` +
			`"cvss_severities":{"cvss_v3":null,"cvss_v4":null}}`,
		repository: `{"ghsa_id":"` + testGHSA + `","html_url":"https://x/r","state":"draft","author":{"login":"octocat"},` +
			`"publisher":null,"cwe_ids":["CWE-79"],"cvss":{"vector_string":null,"score":null},"submission":null,` +
			`"private_fork":null,"collaborating_users":[],"collaborating_teams":[]}`,
		finding: `{"number":12,"state":"open","url":"u","rule":{"id":"r"},"location":{"path":"a.go"},"message":{}}`,
	})
	for _, tt := range []struct{ id, connection, arguments, want string }{
		{globalAdvisoriesGet.ID, "open", `{"ghsa_id":"` + testGHSA + `"}`,
			`{"ghsa_id":"` + testGHSA + `","url":"https://x/g","summary":"s","cvss_score":5.5,"cvss_vector":"v",` +
				`"epss_percentage":1.5,"epss_percentile":80.25,"cwes":["CWE-1"],"cwes_details":[{"cwe_id":"CWE-1"}]}`},
		{repositoryAdvisoriesList.ID, "repo", `{}`, `{"ghsa_id":"` + testGHSA +
			`","url":"https://x/r","state":"draft","author":"octocat","cwes":["CWE-79"],"cwe_ids":["CWE-79"],` +
			`"author_detail":{"login":"octocat"}}`},
		{codeQualityFindingsGet.ID, "repo", `{"finding_number":12}`,
			`{"number":12,"state":"open","url":"u","rule_id":"r","path":"a.go","repository":"octo-org/example"}`},
	} {
		raw, err := invoke(t, core, tt.id, tt.connection, tt.arguments, false)
		got := asObject(t, raw)
		if strings.HasSuffix(tt.id, ".list") {
			got = firstAdvisory(t, raw)
		}
		if err != nil || !reflect.DeepEqual(got, asObject(t, []byte(tt.want))) {
			t.Errorf("%s = %s, %v; want %s", tt.id, raw, err, tt.want)
		}
	}
}

func TestAdvisoryListsAreCappedAndSayso(t *testing.T) {
	var credits, ids []string
	for i := 0; i < projectionListLimit+1; i++ {
		credits = append(credits, `{"login":"u","type":"finder"}`)
		ids = append(ids, `{"type":"CVE","value":"CVE-2026-0001"}`)
	}
	repository := `{"ghsa_id":"` + testGHSA + `","state":"draft","credits":[` + strings.Join(credits, ",") +
		`],"identifiers":[` + strings.Join(ids, ",") + `]}`
	core, _ := serveWidth(t, &widthFake{repository: repository})
	raw, err := invoke(t, core, repositoryAdvisoriesList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	got := firstAdvisory(t, raw)
	if len(got["credits"].([]any)) != projectionListLimit || got["credits_truncated"] != true ||
		len(got["identifiers"].([]any)) != projectionListLimit || got["identifiers_truncated"] != true {
		t.Errorf("long lists were not capped with their markers: %s", raw)
	}
}

func TestGlobalAdvisoryEPSSFiltersSortAndDirection(t *testing.T) {
	w := &widthFake{global: documentedGlobalAdvisory}
	core, _ := serveWidth(t, w)
	for _, tt := range []struct{ arguments, want string }{
		{`{"epss_percentage_min":0.5}`, "epss_percentage=%3E%3D0.5"},
		{`{"epss_percentage_max":0.25}`, "epss_percentage=%3C%3D0.25"},
		{`{"epss_percentage_min":0.1,"epss_percentage_max":0.9}`, "epss_percentage=0.1..0.9"},
		{`{"epss_percentage_min":0.3,"epss_percentage_max":0.3}`, "epss_percentage=0.3"},
		{`{"epss_percentile_min":0,"epss_percentile_max":1}`, "epss_percentile=0..1"},
		{`{"epss_percentile_min":0.95}`, "epss_percentile=%3E%3D0.95"},
		{`{"sort":"epss_percentile","direction":"asc"}`, "direction=asc&"},
		{`{"sort":"epss_percentage"}`, "sort=epss_percentage"},
	} {
		w.queries = nil
		if _, err := invoke(t, core, globalAdvisoriesList.ID, "open", tt.arguments, false); err != nil {
			t.Fatalf("%s: %v", tt.arguments, err)
		}
		if len(w.queries) != 1 || !strings.Contains(w.queries[0], tt.want) {
			t.Errorf("%s sent %v, want %s", tt.arguments, w.queries, tt.want)
		}
	}
	// Without the new arguments the request is the one it was.
	w.queries = nil
	invoke(t, core, globalAdvisoriesList.ID, "open", `{"severity":"high"}`, false)
	if len(w.queries) != 1 || strings.Contains(w.queries[0], "epss") || strings.Contains(w.queries[0], "sort") ||
		strings.Contains(w.queries[0], "direction") {
		t.Errorf("plain request = %v, want no new parameter", w.queries)
	}
}

func TestGlobalAdvisoryFiltersAreRefusedBeforeAnyCredential(t *testing.T) {
	w := &widthFake{global: documentedGlobalAdvisory}
	core, reads := serveWidth(t, w)
	for _, arguments := range []string{
		`{"epss_percentage_min":-0.1}`, `{"epss_percentage_min":1.1}`, `{"epss_percentage_max":2}`,
		`{"epss_percentile_min":-1}`, `{"epss_percentile_max":1.0001}`,
		`{"epss_percentage_min":0.8,"epss_percentage_max":0.2}`,
		`{"epss_percentile_min":0.5,"epss_percentile_max":0.4}`,
		`{"sort":"created"}`, `{"sort":"name"}`, `{"direction":"up"}`,
	} {
		*reads = 0
		if _, err := invoke(t, core, globalAdvisoriesList.ID, "open", arguments, false); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want an invalid request", arguments, err)
		}
		if *reads != 0 || len(w.queries) != 0 {
			t.Errorf("%s reached the credential or GitHub", arguments)
		}
	}
}

func TestGlobalAdvisoryCursorBindsTheNewFilters(t *testing.T) {
	// Without the new arguments the binding is the one of the compact list, so earlier cursors stay valid.
	old := advisoryArguments{Severity: "high"}
	if err := checkGlobalAdvisoriesList(&old, target{}); err != nil {
		t.Fatal(err)
	}
	legacy := fingerprint("advisories", "global", "", "", "", "", "high", "", false, "", "", "", "")
	if !reflect.DeepEqual(old.binding, legacy) {
		t.Errorf("binding changed without the new arguments")
	}
	w := &widthFake{global: documentedGlobalAdvisory}
	core, _ := serveWidth(t, w)
	for _, arguments := range []string{`{"epss_percentage_min":0.5,"limit":1}`, `{"epss_percentile_max":0.5,"limit":1}`,
		`{"sort":"updated","limit":1}`, `{"direction":"asc","limit":1}`} {
		first, err := invoke(t, core, globalAdvisoriesList.ID, "open", arguments, false)
		var page struct {
			NextCursor string `json:"next_cursor"`
		}
		if err != nil || json.Unmarshal(first, &page) != nil || page.NextCursor == "" {
			t.Fatalf("%s first = %s, %v", arguments, first, err)
		}
		cursor := `"cursor":"` + page.NextCursor + `"`
		cont := strings.Replace(arguments, `"limit":1`, cursor, 1)
		if _, err := invoke(t, core, globalAdvisoriesList.ID, "open", cont, false); err != nil {
			t.Errorf("%s continuation = %v", arguments, err)
		}
		for _, other := range []string{`{` + cursor + `}`, `{"epss_percentage_min":0.6,` + cursor + `}`,
			`{"epss_percentile_max":0.6,` + cursor + `}`, `{"sort":"published","direction":"desc",` + cursor + `}`,
			`{"sort":"epss_percentage",` + cursor + `}`, `{"direction":"desc",` + cursor + `}`} {
			if other == cont {
				continue
			}
			if _, err := invoke(t, core, globalAdvisoriesList.ID, "open", other, false); !isInvalidRequest(err) {
				t.Errorf("%s cursor with %s = %v, want invalid request", arguments, other, err)
			}
		}
	}
	// A continuation still sends the filters it was bound to.
	w.queries = nil
	first, _ := invoke(t, core, globalAdvisoriesList.ID, "open", `{"epss_percentage_min":0.5,"limit":1}`, false)
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	json.Unmarshal(first, &page)
	invoke(t, core, globalAdvisoriesList.ID, "open", `{"epss_percentage_min":0.5,"cursor":"`+page.NextCursor+`"}`, false)
	last, _ := url.QueryUnescape(w.queries[len(w.queries)-1])
	if !strings.Contains(last, "epss_percentage=>=0.5") || !strings.Contains(last, "after=Y3Vyc29yOnYy") {
		t.Errorf("continuation query = %q", last)
	}
}
