package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const testGHSA = "GHSA-c2f3-g4h5-jmpq"

// advisoryFake answers the global, repository, and organization advisory routes and the code quality finding
// route, and records the raw query of every request.
type advisoryFake struct {
	queries []string
}

func (a *advisoryFake) vulnerabilities(count int) string {
	entries := make([]string, count)
	for i := range entries {
		entries[i] = `{"package":{"ecosystem":"npm","name":"lodash"},"vulnerable_version_range":"<4.17.21",` +
			`"first_patched_version":"4.17.21","vulnerable_functions":["merge"]}`
	}
	return "[" + strings.Join(entries, ",") + "]"
}

func (a *advisoryFake) route(w http.ResponseWriter, req *http.Request) bool {
	path := strings.TrimPrefix(req.URL.Path, "/api/v3")
	var single string
	switch {
	case path == "/advisories" || strings.HasPrefix(path, "/advisories/"):
		single = `{"ghsa_id":"` + testGHSA + `","cve_id":"CVE-2026-1234","html_url":"https://x/g","type":"reviewed",` +
			`"severity":"high","summary":"Prototype pollution","description":"` + strings.Repeat("d", 4100) + `",` +
			`"references":["https://example.test/a"],"published_at":"2026-09-01T00:00:00Z",` +
			`"vulnerabilities":` + a.vulnerabilities(25) + `,"cvss":{"vector_string":"CVSS:3.1/AV:N","score":7.5},` +
			`"epss":{"percentage":1.5,"percentile":80.25},"cwes":[{"cwe_id":"CWE-1321","name":"x"}]}`
	case strings.HasSuffix(path, "/security-advisories"):
		single = `{"ghsa_id":"` + testGHSA + `","cve_id":null,"html_url":"https://x/r","state":"draft","severity":"low",` +
			`"summary":"Draft","description":"private details","author":{"login":"octocat"},"publisher":null,` +
			`"created_at":"2026-09-01T00:00:00Z","cwe_ids":["CWE-79"],"cvss":{"vector_string":null,"score":null},` +
			`"vulnerabilities":[{"package":{"ecosystem":"go","name":"example.test/m"},` +
			`"vulnerable_version_range":"<1.2","patched_versions":"1.2","vulnerable_functions":null}]}`
	case strings.Contains(path, "/code-quality/findings/"):
		single = `{"number":12,"state":"open","url":"https://api.github.com/x/12","created_at":"2026-09-01T00:00:00Z",` +
			`"rule":{"id":"go/useless-null-check","title":"Useless check","description":"desc","help":"help text",` +
			`"severity":"warning","category":"maintainability"},"location":{"path":"a.go","start_line":7,` +
			`"start_column":2,"end_line":8,"end_column":9},"message":{"text":"` + strings.Repeat("m", 600) +
			`","markdown":"md"}}`
	default:
		return false
	}
	a.queries = append(a.queries, path+"?"+req.URL.RawQuery)
	w.Header().Set("Content-Type", "application/json")
	if strings.HasSuffix(path, "/404") || strings.HasSuffix(path, "/advisories/GHSA-2222-2222-2222") {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
		return true
	}
	isList := path == "/advisories" || strings.HasSuffix(path, "/security-advisories")
	if !isList {
		fmt.Fprint(w, single)
		return true
	}
	if req.URL.Query().Get("after") == "" {
		w.Header().Set("Link", `<https://api.github.com/advisories?per_page=1&after=Y3Vyc29yOnYy>; rel="next"`)
	}
	fmt.Fprint(w, "["+single+"]")
	return true
}

func serveAdvisories(t *testing.T) (*fakeGitHub, *advisoryFake, *application.Core, *int) {
	t.Helper()
	a := &advisoryFake{}
	f := &fakeGitHub{failure: a.route}
	base := serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	return f, a, application.New(registry(t), discoveryConfig(base), resolver(red, &reads), red), &reads
}

func TestAdvisoryToolsSatisfyTheirContract(t *testing.T) {
	_, a, core, _ := serveAdvisories(t)
	for _, tt := range []struct{ id, connection, arguments, want, scope string }{
		{globalAdvisoriesList.ID, "open", `{"ghsa_id":"` + testGHSA + `","type":"reviewed","cve_id":"CVE-2026-1234",` +
			`"ecosystem":"npm","severity":"high","cwes":["79","22"],"is_withdrawn":true,"affects":"lodash@4.0.0",` +
			`"published":"2026-01-01..2026-03-31","updated":">=2026-01-01","modified":"2026-02-01"}`,
			`"cvss_score":7.5`, ""},
		{globalAdvisoriesGet.ID, "open", `{"ghsa_id":"` + testGHSA + `"}`, `"references":["https://example.test/a"]`, ""},
		{repositoryAdvisoriesList.ID, "repo", `{"state":"draft","sort":"updated","direction":"asc"}`,
			`"description":"private details"`, `"repository":"octo-org/example"`},
		{organizationAdvisoriesList.ID, "org", `{"state":"triage","sort":"published","direction":"desc"}`,
			`"author":"octocat"`, `"owner":"orgs/octo-org"`},
		{codeQualityFindingsGet.ID, "repo", `{"finding_number":12}`, `"rule_help":"help text"`,
			`"repository":"octo-org/example"`},
	} {
		got, err := invoke(t, core, tt.id, tt.connection, tt.arguments, false)
		if err != nil || !strings.Contains(string(got), tt.want) || !strings.Contains(string(got), tt.scope) {
			t.Errorf("%s = %s, %v; want %s and %s", tt.id, got, err, tt.want, tt.scope)
		}
	}
	joined := strings.Join(a.queries, "\n")
	for _, want := range []string{"ghsa_id=" + testGHSA, "type=reviewed", "cve_id=CVE-2026-1234", "ecosystem=npm",
		"severity=high", "cwes=79%2C22", "is_withdrawn=true", "affects=lodash%404.0.0",
		"published=2026-01-01..2026-03-31", "updated=%3E%3D2026-01-01", "modified=2026-02-01", "per_page=30",
		"/orgs/octo-org/security-advisories?", "/repos/octo-org/example/security-advisories?", "state=draft",
		"state=triage", "sort=updated", "direction=asc", "/repos/octo-org/example/code-quality/findings/12"} {
		if !strings.Contains(joined, want) {
			t.Errorf("queries lack %q:\n%s", want, joined)
		}
	}
	// Long texts and long lists are cut and say so.
	got, _ := invoke(t, core, globalAdvisoriesGet.ID, "open", `{"ghsa_id":"`+testGHSA+`"}`, false)
	var advisory GlobalAdvisory
	if json.Unmarshal(got, &advisory) != nil || !advisory.Truncated || len(advisory.Vulnerabilities) != advisoryMaxVulnerable ||
		len([]rune(advisory.Description)) != advisoryDescriptionLimit {
		t.Errorf("global get = %s, want a visible cut", got)
	}
	got, _ = invoke(t, core, codeQualityFindingsGet.ID, "repo", `{"finding_number":12}`, false)
	if !strings.Contains(string(got), `"truncated":true`) {
		t.Errorf("finding = %s, want a visible cut", got)
	}
	// A missing advisory and a missing finding are not found.
	for _, tt := range []struct{ id, connection, arguments string }{
		{globalAdvisoriesGet.ID, "open", `{"ghsa_id":"GHSA-2222-2222-2222"}`},
		{codeQualityFindingsGet.ID, "repo", `{"finding_number":404}`},
	} {
		if _, err := invoke(t, core, tt.id, tt.connection, tt.arguments, false); err == nil ||
			!strings.Contains(err.Error(), "does not hold") {
			t.Errorf("%s missing = %v, want not found", tt.id, err)
		}
	}
}

func TestAdvisoryListsPageByCursor(t *testing.T) {
	_, a, core, _ := serveAdvisories(t)
	for _, tt := range []struct{ id, connection, arguments, filters string }{
		{globalAdvisoriesList.ID, "open", `{"severity":"high","limit":1}`, `"severity":"high",`},
		{repositoryAdvisoriesList.ID, "repo", `{"state":"draft","limit":1}`, `"state":"draft",`},
		{organizationAdvisoriesList.ID, "org", `{"limit":1}`, ``},
	} {
		first, err := invoke(t, core, tt.id, tt.connection, tt.arguments, false)
		var page struct {
			HasMore    bool   `json:"has_more"`
			NextCursor string `json:"next_cursor"`
		}
		if err != nil || json.Unmarshal(first, &page) != nil || !page.HasMore || page.NextCursor == "" {
			t.Fatalf("%s first = %s, %v", tt.id, first, err)
		}
		for name, cursor := range alteredCursors(t, page.NextCursor) {
			if _, err := invoke(t, core, tt.id, tt.connection, `{"cursor":"`+cursor+`"}`, false); !isInvalidRequest(err) {
				t.Errorf("%s with %s = %v, want invalid request", tt.id, name, err)
			}
		}
		if _, err := invoke(t, core, tt.id, tt.connection, `{"state":"closed","severity":"low","cursor":"`+page.NextCursor+`"}`, false); !isInvalidRequest(err) {
			t.Errorf("%s cursor with other filters = %v, want invalid request", tt.id, err)
		}
		before := len(a.queries)
		next, err := invoke(t, core, tt.id, tt.connection, `{`+tt.filters+`"cursor":"`+page.NextCursor+`"}`, false)
		if err != nil || strings.Contains(string(next), `"has_more":true`) {
			t.Errorf("%s continuation = %s, %v; want the last batch", tt.id, next, err)
		}
		last := a.queries[len(a.queries)-1]
		if len(a.queries) != before+1 || !strings.Contains(last, "after=Y3Vyc29yOnYy") || !strings.Contains(last, "per_page=1") {
			t.Errorf("%s continuation query = %q, want the after cursor and the first batch size", tt.id, last)
		}
	}
}

func TestAdvisoryArgumentsAndTargetsAreCheckedBeforeIO(t *testing.T) {
	f, _, core, reads := serveAdvisories(t)
	for _, tt := range []struct{ id, connection, arguments string }{
		// The GitHub-wide tools refuse a repository or a project target.
		{globalAdvisoriesList.ID, "starrepo", `{}`},
		{globalAdvisoriesList.ID, "repo", `{}`},
		{globalAdvisoriesList.ID, "planning", `{}`},
		{globalAdvisoriesGet.ID, "starrepo", `{"ghsa_id":"` + testGHSA + `"}`},
		{globalAdvisoriesGet.ID, "planning", `{"ghsa_id":"` + testGHSA + `"}`},
		// The organization tool needs an organization owner.
		{organizationAdvisoriesList.ID, "open", `{}`},
		{organizationAdvisoriesList.ID, "user", `{}`},
		{organizationAdvisoriesList.ID, "open", `{"owner":"users/octocat"}`},
		{organizationAdvisoriesList.ID, "org", `{"owner":"orgs/other"}`},
		{organizationAdvisoriesList.ID, "starrepo", `{}`},
		{organizationAdvisoriesList.ID, "repo", `{}`},
		{organizationAdvisoriesList.ID, "planning", `{}`},
		{repositoryAdvisoriesList.ID, "planning", `{}`},
		{codeQualityFindingsGet.ID, "planning", `{"finding_number":1}`},
		// Malformed filters.
		{globalAdvisoriesList.ID, "open", `{"ghsa_id":"GHSA-xxxx"}`},
		{globalAdvisoriesList.ID, "open", `{"type":"weird"}`},
		{globalAdvisoriesList.ID, "open", `{"cve_id":"CVE-1"}`},
		{globalAdvisoriesList.ID, "open", `{"ecosystem":"cobol"}`},
		{globalAdvisoriesList.ID, "open", `{"severity":"bad"}`},
		{globalAdvisoriesList.ID, "open", `{"cwes":["a"]}`},
		{globalAdvisoriesList.ID, "open", `{"affects":"a b"}`},
		{globalAdvisoriesList.ID, "open", `{"published":"x y"}`},
		{globalAdvisoriesGet.ID, "open", `{"ghsa_id":"nope"}`},
		{repositoryAdvisoriesList.ID, "repo", `{"state":"open"}`},
		{repositoryAdvisoriesList.ID, "repo", `{"sort":"name"}`},
		{repositoryAdvisoriesList.ID, "repo", `{"direction":"up"}`},
		{codeQualityFindingsGet.ID, "repo", `{"finding_number":0}`},
	} {
		*reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, tt.id, tt.connection, tt.arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s(%s) on %s = %v, want an invalid request", tt.id, tt.arguments, tt.connection, err)
		}
		if *reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s(%s) on %s reached the credential or GitHub", tt.id, tt.arguments, tt.connection)
		}
	}
	// A connection without targets, or with owner targets only, reaches the GitHub-wide tools.
	for _, connection := range []string{"open", "org", "user"} {
		if _, err := invoke(t, core, globalAdvisoriesList.ID, connection, `{}`, false); err != nil {
			t.Errorf("global list on %s = %v", connection, err)
		}
	}
}

func TestAdvisoryCursorIsBoundToItsTarget(t *testing.T) {
	_, _, core, _ := serveAdvisories(t)
	first, err := invoke(t, core, organizationAdvisoriesList.ID, "open", `{"owner":"orgs/octo-org","limit":1}`, false)
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	if err != nil || json.Unmarshal(first, &page) != nil || page.NextCursor == "" {
		t.Fatalf("first = %s, %v", first, err)
	}
	if _, err := invoke(t, core, organizationAdvisoriesList.ID, "open", `{"owner":"orgs/other","cursor":"`+page.NextCursor+`"}`, false); !isInvalidRequest(err) {
		t.Errorf("cursor of another organization = %v, want invalid request", err)
	}
}
