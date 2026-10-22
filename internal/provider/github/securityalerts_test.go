package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const secretCanary = "ghp_CANARYsecretValue0123456789abcdef"

// alertFake answers the three alert routes of the bound repository. mode selects a failure shape for the
// secret scanning routes; every secret scanning answer carries the canary in every value-bearing field.
type alertFake struct {
	f    *fakeGitHub
	mode string
	// codeScanning replaces the code scanning alert the routes answer with.
	codeScanning string
	// dependabot replaces the Dependabot alert the routes answer with.
	dependabot string
	// queries records the raw query of every request by route.
	queries []string
}

func secretAlertJSON(number int) string {
	return fmt.Sprintf(`{"number":%d,"state":"open","created_at":"2026-09-01T00:00:00Z","html_url":"https://x/%d",`+
		`"secret_type":"github_personal_access_token","secret_type_display_name":"GitHub PAT","validity":"active",`+
		`"secret":%q,"secret_value":%q,"resolution_comment":%q,"first_location_detected":{"type":"commit",`+
		`"path":"src/a.go","start_line":3,"end_line":3,"blob_sha":"b","snippet":%q,"secret":%q},`+
		`"locations":[{"secret":%q}],"variants":[%q],"resolved_by":null}`,
		number, number, secretCanary, secretCanary, secretCanary, secretCanary, secretCanary, secretCanary, secretCanary)
}

func (a *alertFake) route(w http.ResponseWriter, req *http.Request) bool {
	rest, ok := strings.CutPrefix(req.URL.Path, actionsPrefix)
	if !ok {
		return false
	}
	kind, tail, _ := strings.Cut(rest, "/alerts")
	if kind != "code-scanning" && kind != "dependabot" && kind != "secret-scanning" {
		return false
	}
	a.queries = append(a.queries, kind+"?"+req.URL.RawQuery)
	w.Header().Set("Content-Type", "application/json")
	if kind == "secret-scanning" {
		switch a.mode {
		case "bad-json":
			fmt.Fprint(w, `{"secret":"`+secretCanary+`" broken`)
			return true
		case "status":
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"message":"boom `+secretCanary+`"}`)
			return true
		case "forbidden":
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"message":"no `+secretCanary+`"}`)
			return true
		case "huge":
			fmt.Fprint(w, `[{"number":1,"state":"open","secret":"`+secretCanary+strings.Repeat("x", maxResponseBytes)+`"}]`)
			return true
		}
	}
	number := strings.Trim(tail, "/")
	if number == "404" {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
		return true
	}
	var single, list string
	switch kind {
	case "code-scanning":
		single = a.codeScanning
		if single != "" {
			break
		}
		single = `{"number":42,"state":"open","html_url":"https://x/42","created_at":"2026-09-01T00:00:00Z",` +
			`"dismissed_by":{"login":"octocat"},"dismissed_comment":"` + strings.Repeat("c", 600) + `",` +
			`"rule":{"id":"go/sql-injection","name":"sql","severity":"error","security_severity_level":"high",` +
			`"description":"SQL injection","help":"help text","tags":["security"]},"tool":{"name":"CodeQL","version":"2.1"},` +
			`"most_recent_instance":{"ref":"refs/heads/main","commit_sha":"abc","message":{"text":"tainted"},` +
			`"location":{"path":"a.go","start_line":7,"end_line":8}}}`
	case "dependabot":
		single = a.dependabot
		if single != "" {
			break
		}
		single = `{"number":7,"state":"open","html_url":"https://x/7","dependency":{"package":{"ecosystem":"npm",` +
			`"name":"lodash"},"manifest_path":"package.json","scope":"runtime","relationship":"direct"},` +
			`"security_advisory":{"ghsa_id":"GHSA-xxxx","cve_id":"CVE-2026-1","summary":"Prototype pollution",` +
			`"description":"details","severity":"high","cvss":{"score":7.5}},"security_vulnerability":` +
			`{"vulnerable_version_range":"<4.17.21","first_patched_version":{"identifier":"4.17.21"}}}`
	default:
		single = secretAlertJSON(3)
	}
	list = "[" + single + "]"
	if number == "" {
		if a.f != nil && req.URL.Query().Get("page") == "1" {
			w.Header().Set("Link", `<https://api.github.com/x?page=2>; rel="next"`)
		}
		fmt.Fprint(w, list)
		return true
	}
	if _, err := strconv.Atoi(strings.TrimPrefix(number, "/")); err != nil {
		w.WriteHeader(http.StatusNotFound)
		return true
	}
	fmt.Fprint(w, single)
	return true
}

func serveAlerts(t *testing.T, mode string) (*fakeGitHub, *alertFake, *application.Core, *int) {
	t.Helper()
	a := &alertFake{mode: mode}
	f := &fakeGitHub{}
	a.f = f
	f.failure = a.route
	base := serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	cfg := coreConfig(base)
	cfg.Connections["repo"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Effects: []config.Effect{config.EffectRead}}
	return f, a, application.New(registry(t), cfg, resolver(red, &reads), red), &reads
}

func TestSecurityAlertToolsSatisfyTheirContract(t *testing.T) {
	_, a, core, _ := serveAlerts(t, "")
	for _, tt := range []struct{ id, arguments, want string }{
		{codeScanningAlertsList.ID, `{"state":"open","severity":"high","tool_name":"CodeQL","ref":"refs/heads/main"}`, `"rule_id":"go/sql-injection"`},
		{codeScanningAlertsGet.ID, `{"alert_number":42}`, `"help":"help text"`},
		{dependabotAlertsList.ID, `{"state":"open","severity":"high"}`, `"package":"lodash"`},
		{dependabotAlertsGet.ID, `{"alert_number":7}`, `"first_patched_version":"4.17.21"`},
		{secretScanningAlertsList.ID, `{"state":"open","secret_type":"github_personal_access_token","resolution":"revoked"}`, `"secret_type":"github_personal_access_token"`},
		{secretScanningAlertsGet.ID, `{"alert_number":3}`, `"path":"src/a.go"`},
	} {
		got, err := invoke(t, core, tt.id, "repo", tt.arguments, false)
		if err != nil || !strings.Contains(string(got), tt.want) ||
			!strings.Contains(string(got), `"repository":"octo-org/example"`) {
			t.Errorf("%s = %s, %v; want %s", tt.id, got, err, tt.want)
		}
	}
	joined := strings.Join(a.queries, "\n")
	for _, want := range []string{"code-scanning?", "state=open", "severity=high", "tool_name=CodeQL",
		"ref=refs%2Fheads%2Fmain", "hide_secret=true", "secret_type=github_personal_access_token", "resolution=revoked"} {
		if !strings.Contains(joined, want) {
			t.Errorf("queries lack %q:\n%s", want, joined)
		}
	}
	// A dismissed comment is cut and says so.
	got, _ := invoke(t, core, codeScanningAlertsGet.ID, "repo", `{"alert_number":42}`, false)
	if !strings.Contains(string(got), `"truncated":true`) {
		t.Errorf("get = %s, want a visible cut", got)
	}
	// A missing alert is not found.
	if _, err := invoke(t, core, dependabotAlertsGet.ID, "repo", `{"alert_number":404}`, false); err == nil ||
		!strings.Contains(err.Error(), "does not hold") {
		t.Errorf("missing alert = %v, want not found", err)
	}
}

func TestSecurityAlertListsPageByCursor(t *testing.T) {
	_, _, core, _ := serveAlerts(t, "")
	first, err := invoke(t, core, dependabotAlertsList.ID, "repo", `{"limit":1}`, false)
	var page DependabotAlertList
	if err != nil || json.Unmarshal(first, &page) != nil || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("first = %s, %v", first, err)
	}
	if _, err := invoke(t, core, dependabotAlertsList.ID, "repo", `{"state":"fixed","cursor":"`+page.NextCursor+`"}`, false); !isInvalidRequest(err) {
		t.Errorf("cursor with other filters = %v, want invalid request", err)
	}
	if _, err := invoke(t, core, dependabotAlertsList.ID, "repo", `{"cursor":"`+page.NextCursor+`"}`, false); err != nil {
		t.Errorf("continuation = %v", err)
	}
}

func TestSecurityAlertArgumentsAndTargetsAreCheckedBeforeIO(t *testing.T) {
	f, _, core, reads := serveAlerts(t, "")
	for _, tt := range []struct{ id, connection, arguments string }{
		{codeScanningAlertsGet.ID, "repo", `{"alert_number":0}`},
		{codeScanningAlertsList.ID, "repo", `{"state":"weird"}`},
		{codeScanningAlertsList.ID, "repo", `{"tool_name":"CodeQL","tool_guid":"abc"}`},
		{codeScanningAlertsList.ID, "repo", `{"tool_guid":"` + strings.Repeat("g", 257) + `"}`},
		{codeScanningAlertsList.ID, "repo", `{"tool_guid":"a\u0007b"}`},
		{codeScanningAlertsList.ID, "repo", `{"pr":0}`},
		{codeScanningAlertsList.ID, "repo", `{"pr":-3}`},
		{codeScanningAlertsList.ID, "repo", `{"assignees":["a","b","c","d","e","f","g","h","i","j","k"]}`},
		{codeScanningAlertsList.ID, "repo", `{"assignees":["*","octocat"]}`},
		{codeScanningAlertsList.ID, "repo", `{"assignees":["none","octocat"]}`},
		{codeScanningAlertsList.ID, "repo", `{"assignees":["bad login"]}`},
		{codeScanningAlertsList.ID, "repo", `{"assignees":[""]}`},
		{codeScanningAlertsList.ID, "repo", `{"sort":"name"}`},
		{codeScanningAlertsList.ID, "repo", `{"direction":"up"}`},
		{dependabotAlertsList.ID, "repo", `{"severity":"warning"}`},
		{secretScanningAlertsList.ID, "repo", `{"secret_type":"a b"}`},
		{secretScanningAlertsList.ID, "repo", `{"resolution":"x"}`},
		{secretScanningAlertsList.ID, "planning", `{}`},
		{secretScanningAlertsGet.ID, "planning", `{"alert_number":1}`},
		{codeScanningAlertsList.ID, "planning", `{}`},
		{dependabotAlertsGet.ID, "planning", `{"alert_number":1}`},
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
}

func TestSecretScanningNeverExposesTheSecret(t *testing.T) {
	for _, mode := range []string{"", "bad-json", "status", "forbidden", "huge"} {
		_, _, core, _ := serveAlerts(t, mode)
		for _, tt := range []struct{ id, arguments string }{
			{secretScanningAlertsList.ID, `{}`}, {secretScanningAlertsGet.ID, `{"alert_number":3}`},
		} {
			got, err := invoke(t, core, tt.id, "repo", tt.arguments, false)
			text := string(got)
			if err != nil {
				text += err.Error()
			}
			if strings.Contains(text, secretCanary) || strings.Contains(text, "CANARY") {
				t.Errorf("mode %q %s leaked the secret: %s", mode, tt.id, text)
			}
			if mode == "" && err != nil {
				t.Errorf("mode %q %s = %v", mode, tt.id, err)
			}
			if mode != "" && err == nil {
				t.Errorf("mode %q %s = %s, want an error", mode, tt.id, got)
			}
		}
	}
}

func TestSecurityProfileIsReadOnlyAndNotRecommended(t *testing.T) {
	reg := registry(t)
	if err := reg.ValidateProfiles(); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		switch profile.ID {
		case "security":
			if profile.Recommended || len(profile.Tools) != 11 {
				t.Errorf("security = %+v", profile)
			}
			if got := metadata.ProfileEffects(profile); len(got) != 1 || got[0] != config.EffectRead {
				t.Errorf("security permissions = %v", got)
			}
		case "read":
			if !profile.Recommended || len(profile.Tools) != 7 {
				t.Errorf("read profile changed: %+v", profile)
			}
		}
	}
}

// codeScanningDocumentedAlert is the example alert of the GitHub API documentation, with the members that
// example leaves out filled in from the schema.
const codeScanningDocumentedAlert = `{"number":42,"created_at":"2020-06-19T11:21:34Z","updated_at":"2020-06-20T11:21:34Z",
"url":"https://api.github.com/repos/octo-org/example/code-scanning/alerts/42",
"html_url":"https://github.com/octo-org/example/security/code-scanning/42","state":"dismissed","fixed_at":null,
"dismissed_by":{"login":"octocat","id":54933897,"node_id":"MDQ6VXNlcjE=","type":"User","site_admin":false,
"url":"https://api.github.com/users/octocat","html_url":"https://github.com/octocat","followers_url":"https://api.github.com/users/octocat/followers"},
"dismissed_at":"2020-02-14T12:29:18Z","dismissed_reason":"false positive",
"dismissed_comment":"This alert is not actually correct.",
"rule":{"id":"js/zipslip","severity":"error","security_severity_level":"high",
"description":"Arbitrary file write during zip extraction","name":"js/zipslip",
"full_description":"Extracting files from a malicious zip archive can overwrite files.",
"tags":["security","external/cwe/cwe-022"],"help":"# Zip Slip\nHelp text","help_uri":"https://codeql.github.com/"},
"tool":{"name":"CodeQL","guid":null,"version":"2.4.0"},
"most_recent_instance":{"ref":"refs/heads/main","analysis_key":".github/workflows/codeql-analysis.yml:CodeQL-Build",
"category":".github/workflows/codeql-analysis.yml:CodeQL-Build","environment":"{}","state":"dismissed",
"commit_sha":"39406e42cb832f683daa691dd652a8dc36ee8930","message":{"text":"This path depends on a user-provided value.",
"markdown":"This [path](1) depends on a user-provided value."},
"location":{"path":"spec-main/api-session-spec.ts","start_line":917,"end_line":918,"start_column":7,"end_column":18},
"html_url":"https://github.com/octo-org/example/blob/39406e4/spec-main/api-session-spec.ts#L917",
"classifications":["test"]},
"instances_url":"https://api.github.com/repos/octo-org/example/code-scanning/alerts/42/instances",
"dismissal_approved_by":{"login":"hubot","id":2,"type":"User"},
"assignees":[{"login":"monalisa","id":3,"type":"User"}],
"linked_pull_requests":[{"url":"https://api.github.com/repos/octo-org/example/pulls/7","id":99,"node_id":"PR_1",
"html_url":"https://github.com/octo-org/example/pull/7","diff_url":"https://github.com/octo-org/example/pull/7.diff",
"issue_url":"https://api.github.com/repos/octo-org/example/issues/7","number":7,"state":"open","locked":false,
"title":"Fix zip slip","user":{"login":"octocat","id":1},"body":"Fixes it","labels":[{"id":5,"node_id":"L_1",
"url":"https://api.github.com/repos/octo-org/example/labels/bug","name":"bug","description":"Bug","color":"f29513","default":true}],
"milestone":{"url":"https://api.github.com/repos/octo-org/example/milestones/1","id":8,"number":1,"state":"open","title":"v1",
"open_issues":2,"closed_issues":3},"created_at":"2020-06-21T00:00:00Z","merge_commit_sha":null,
"requested_reviewers":[{"login":"hubot","id":2}],
"requested_teams":[{"id":11,"name":"core","slug":"core","permission":"pull","permissions":{"pull":true,"admin":false},
"url":"https://api.github.com/teams/11","html_url":"https://github.com/orgs/octo-org/teams/core",
"parent":{"id":10,"name":"all","slug":"all"}}],
"head":{"label":"fork:fix","ref":"fix","sha":"abc","user":{"login":"forker","id":4},
"repo":{"id":500,"node_id":"R_500","name":"example","full_name":"forker/example","private":false,"description":"secret fork text"}},
"base":{"label":"octo-org:main","ref":"main","sha":"def","user":{"login":"octo-org","id":9},
"repo":{"id":501,"node_id":"R_501","name":"example","full_name":"octo-org/example","private":false,"description":"base repo"}},
"_links":{"self":{"href":"https://api.github.com/repos/octo-org/example/pulls/7"},"html":{"href":"https://github.com/octo-org/example/pull/7"}},
"author_association":"MEMBER","auto_merge":{"enabled_by":{"login":"octocat","id":1},"merge_method":"squash",
"commit_title":"Fix","commit_message":"Fix it"},"stack":{"base":{"ref":"main","sha":"def"},"size":2,"position":1,"id":3,"number":7},
"draft":false}]}`

func decodeAlert(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return out
}

func at(t *testing.T, value any, path ...any) any {
	t.Helper()
	for _, step := range path {
		switch key := step.(type) {
		case string:
			object, ok := value.(map[string]any)
			if !ok {
				t.Fatalf("no object at %v in %v", step, value)
			}
			value = object[key]
		case int:
			list, ok := value.([]any)
			if !ok || key >= len(list) {
				t.Fatalf("no entry at %v in %v", step, value)
			}
			value = list[key]
		}
	}
	return value
}

func TestCodeScanningAlertGetReportsEveryDocumentedMember(t *testing.T) {
	_, a, core, _ := serveAlerts(t, "")
	a.codeScanning = codeScanningDocumentedAlert
	got, err := invoke(t, core, codeScanningAlertsGet.ID, "repo", `{"alert_number":42}`, false)
	if err != nil {
		t.Fatal(err)
	}
	alert := decodeAlert(t, got)
	for path, want := range map[string]any{
		"url":                                                       "https://github.com/octo-org/example/security/code-scanning/42",
		"links.url":                                                 "https://api.github.com/repos/octo-org/example/code-scanning/alerts/42",
		"links.instances_url":                                       "https://api.github.com/repos/octo-org/example/code-scanning/alerts/42/instances",
		"dismissed_by":                                              "octocat",
		"dismissed_by_detail.login":                                 "octocat",
		"dismissed_by_detail.id":                                    float64(54933897),
		"dismissed_by_detail.url":                                   "https://github.com/octocat",
		"dismissal_approved_by.login":                               "hubot",
		"assignees.0.login":                                         "monalisa",
		"rule_id":                                                   "js/zipslip",
		"rule_detail.id":                                            "js/zipslip",
		"rule_detail.name":                                          "js/zipslip",
		"rule_detail.severity":                                      "error",
		"rule_detail.security_severity_level":                       "high",
		"rule_detail.description":                                   "Arbitrary file write during zip extraction",
		"rule_detail.full_description":                              "Extracting files from a malicious zip archive can overwrite files.",
		"rule_detail.tags.1":                                        "external/cwe/cwe-022",
		"rule_detail.help":                                          "# Zip Slip\nHelp text",
		"rule_detail.help_uri":                                      "https://codeql.github.com/",
		"tool_detail.name":                                          "CodeQL",
		"tool_detail.version":                                       "2.4.0",
		"most_recent_instance.ref":                                  "refs/heads/main",
		"most_recent_instance.analysis_key":                         ".github/workflows/codeql-analysis.yml:CodeQL-Build",
		"most_recent_instance.environment":                          "{}",
		"most_recent_instance.category":                             ".github/workflows/codeql-analysis.yml:CodeQL-Build",
		"most_recent_instance.state":                                "dismissed",
		"most_recent_instance.commit_sha":                           "39406e42cb832f683daa691dd652a8dc36ee8930",
		"most_recent_instance.message.text":                         "This path depends on a user-provided value.",
		"most_recent_instance.message.markdown":                     "This [path](1) depends on a user-provided value.",
		"most_recent_instance.location.path":                        "spec-main/api-session-spec.ts",
		"most_recent_instance.location.start_line":                  float64(917),
		"most_recent_instance.location.end_line":                    float64(918),
		"most_recent_instance.location.start_column":                float64(7),
		"most_recent_instance.location.end_column":                  float64(18),
		"most_recent_instance.url":                                  "https://github.com/octo-org/example/blob/39406e4/spec-main/api-session-spec.ts#L917",
		"most_recent_instance.classifications.0":                    "test",
		"linked_pull_requests.0.number":                             float64(7),
		"linked_pull_requests.0.title":                              "Fix zip slip",
		"linked_pull_requests.0.url":                                "https://github.com/octo-org/example/pull/7",
		"linked_pull_requests.0.links.url":                          "https://api.github.com/repos/octo-org/example/pulls/7",
		"linked_pull_requests.0.links.diff_url":                     "https://github.com/octo-org/example/pull/7.diff",
		"linked_pull_requests.0.labels.0.name":                      "bug",
		"linked_pull_requests.0.milestone.title":                    "v1",
		"linked_pull_requests.0.requested_reviewers.0.login":        "hubot",
		"linked_pull_requests.0.requested_teams.0.slug":             "core",
		"linked_pull_requests.0.requested_teams.0.permissions.pull": true,
		"linked_pull_requests.0.requested_teams.0.parent.slug":      "all",
		"linked_pull_requests.0.base.repo.description":              "base repo",
		"linked_pull_requests.0.head.repo.full_name":                "forker/example",
		"linked_pull_requests.0.link_relations.self":                "https://api.github.com/repos/octo-org/example/pulls/7",
		"linked_pull_requests.0.auto_merge.merge_method":            "squash",
		"linked_pull_requests.0.stack.base.sha":                     "def",
		"linked_pull_requests.0.stack.position":                     float64(1),
		"linked_pull_requests.0.author_association":                 "MEMBER",
	} {
		var steps []any
		for _, part := range strings.Split(path, ".") {
			if n, err := strconv.Atoi(part); err == nil {
				steps = append(steps, n)
			} else {
				steps = append(steps, part)
			}
		}
		if got := at(t, alert, steps...); got != want {
			t.Errorf("%s = %v, want %v", path, got, want)
		}
	}
	// A fork outside the targets keeps only its identifiers.
	head := at(t, alert, "linked_pull_requests", 0, "head", "repo").(map[string]any)
	if _, leaked := head["description"]; leaked || head["full_name"] != "forker/example" {
		t.Errorf("head repo = %v, want identifiers only", head)
	}
	if _, ok := alert["truncated"]; ok {
		t.Errorf("truncated set without a cut: %s", got)
	}
}

func TestCodeScanningAlertListReportsTheDocumentedMembersWithoutLinkedPullRequests(t *testing.T) {
	_, a, core, _ := serveAlerts(t, "")
	a.codeScanning = codeScanningDocumentedAlert
	got, err := invoke(t, core, codeScanningAlertsList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	entry := at(t, decodeAlert(t, got), "alerts", 0).(map[string]any)
	if entry["rule_detail"] == nil || entry["tool_detail"] == nil || entry["most_recent_instance"] == nil ||
		entry["dismissed_by_detail"] == nil || entry["assignees"] == nil || entry["links"] == nil {
		t.Errorf("list entry lacks a detail member: %v", entry)
	}
	if _, ok := entry["linked_pull_requests"]; ok {
		t.Errorf("list entry carries linked_pull_requests")
	}
}

func TestCodeScanningAlertWithoutTheNewMembersIsUnchanged(t *testing.T) {
	_, a, core, _ := serveAlerts(t, "")
	a.codeScanning = `{"number":5,"state":"open","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z",` +
		`"html_url":"https://x/5","dismissed_reason":"won't fix","dismissed_comment":"ok"}`
	want := `{"created_at":"2026-01-01T00:00:00Z","dismissed_comment":"ok","dismissed_reason":"won't fix",` +
		`"number":5,"repository":"octo-org/example","state":"open","updated_at":"2026-01-02T00:00:00Z",` +
		`"url":"https://x/5"}`
	got, err := invoke(t, core, codeScanningAlertsGet.ID, "repo", `{"alert_number":5}`, false)
	if err != nil || string(got) != want {
		t.Errorf("get = %s, %v; want %s", got, err, want)
	}
}

func TestCodeScanningFlatMembersKeepTheirHandling(t *testing.T) {
	_, a, core, _ := serveAlerts(t, "")
	a.codeScanning = `{"number":5,"state":"open","dismissed_comment":"c\u0007` + strings.Repeat("c", 600) + `",` +
		`"dismissed_by":{"login":"oct\u0007o"},"rule":{"id":"r\u0007","name":"n","description":"d\u0007` +
		strings.Repeat("d", 600) + `","help":"h\u0007` + strings.Repeat("h", 5000) + `","tags":["t\u0007"]},` +
		`"most_recent_instance":{"message":{"text":"m\u0007` + strings.Repeat("m", 600) + `"}}}`
	got, err := invoke(t, core, codeScanningAlertsGet.ID, "repo", `{"alert_number":5}`, false)
	if err != nil {
		t.Fatal(err)
	}
	alert := decodeAlert(t, got)
	// The flat members keep control characters (JSON-escaped) and the cut at 500 or 4000 characters.
	for member, want := range map[string]string{"dismissed_by": "oct\x07o", "rule_id": "r\x07",
		"dismissed_comment": "c\x07" + strings.Repeat("c", 498), "rule_description": "d\x07" + strings.Repeat("d", 498),
		"message": "m\x07" + strings.Repeat("m", 498), "help": "h\x07" + strings.Repeat("h", 3998)} {
		if alert[member] != want {
			t.Errorf("%s = %q, want %q", member, alert[member], want)
		}
	}
	if alert["tags"].([]any)[0] != "t\x07" || alert["truncated"] != true {
		t.Errorf("tags or truncated changed: %v", alert)
	}
	// The new members are cleaned, cut, and marked.
	rule := alert["rule_detail"].(map[string]any)
	if rule["id"] != "r " || rule["description"] != "d "+strings.Repeat("d", 498) ||
		rule["description_truncated"] != true || rule["help"] != "h "+strings.Repeat("h", 3998) ||
		rule["help_truncated"] != true {
		t.Errorf("rule_detail = %v", rule)
	}
	if at(t, alert, "most_recent_instance", "message", "text_truncated") != true ||
		at(t, alert, "dismissed_by_detail", "login") != "oct o" {
		t.Errorf("detail members not cleaned: %s", got)
	}
	// A list cuts the rule help shorter.
	got, err = invoke(t, core, codeScanningAlertsList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	entry := at(t, decodeAlert(t, got), "alerts", 0)
	if help := at(t, entry, "rule_detail", "help").(string); len([]rune(help)) != 500 ||
		at(t, entry, "rule_detail", "help_truncated") != true {
		t.Errorf("list help = %d runes", len([]rune(help)))
	}
	if _, ok := entry.(map[string]any)["help"]; ok {
		t.Errorf("list carries the flat help")
	}
}

func TestCodeScanningFiltersReachTheQueryAndBindTheCursor(t *testing.T) {
	_, a, core, _ := serveAlerts(t, "")
	filters := `{"tool_guid":"guid-1","pr":12,"assignees":["octocat","hubot"],"sort":"updated","direction":"asc","limit":1}`
	first, err := invoke(t, core, codeScanningAlertsList.ID, "repo", filters, false)
	var page CodeScanningAlertList
	if err != nil || json.Unmarshal(first, &page) != nil || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("first = %s, %v", first, err)
	}
	query := a.queries[len(a.queries)-1]
	for _, want := range []string{"tool_guid=guid-1", "pr=12", "assignees=octocat%2Chubot", "sort=updated",
		"direction=asc"} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q lacks %q", query, want)
		}
	}
	cursor := `"cursor":"` + page.NextCursor + `"`
	for _, other := range []string{
		`"tool_guid":"guid-2","pr":12,"assignees":["octocat","hubot"],"sort":"updated","direction":"asc"`,
		`"tool_guid":"guid-1","pr":13,"assignees":["octocat","hubot"],"sort":"updated","direction":"asc"`,
		`"tool_guid":"guid-1","pr":12,"assignees":["octocat"],"sort":"updated","direction":"asc"`,
		`"tool_guid":"guid-1","pr":12,"assignees":["octocat","hubot"],"sort":"created","direction":"asc"`,
		`"tool_guid":"guid-1","pr":12,"assignees":["octocat","hubot"],"sort":"updated","direction":"desc"`,
		`"state":"open"`,
	} {
		if _, err := invoke(t, core, codeScanningAlertsList.ID, "repo", `{`+other+`,`+cursor+`}`,
			false); !isInvalidRequest(err) {
			t.Errorf("cursor with %s = %v, want invalid request", other, err)
		}
	}
	if _, err := invoke(t, core, codeScanningAlertsList.ID, "repo",
		`{"tool_guid":"guid-1","pr":12,"assignees":["octocat","hubot"],"sort":"updated","direction":"asc",`+cursor+`}`,
		false); err != nil {
		t.Errorf("continuation = %v", err)
	}
	// Without the new filters the request and the fingerprint stay as they were.
	_, plainFake, plain, _ := serveAlerts(t, "")
	firstPlain, _ := invoke(t, plain, codeScanningAlertsList.ID, "repo", `{"state":"open","limit":1}`, false)
	var plainPage CodeScanningAlertList
	if json.Unmarshal(firstPlain, &plainPage) != nil || plainPage.NextCursor == "" {
		t.Fatalf("plain first = %s", firstPlain)
	}
	if _, err := invoke(t, plain, codeScanningAlertsList.ID, "repo",
		`{"state":"open","cursor":"`+plainPage.NextCursor+`"}`, false); err != nil {
		t.Errorf("plain continuation = %v", err)
	}
	for _, absent := range []string{"pr=", "sort=", "direction=", "assignees=", "tool_guid="} {
		if strings.Contains(strings.Join(plainFake.queries, "\n"), absent) {
			t.Errorf("a request without the new filters sends %s", absent)
		}
	}
	bound, _ := parseTarget(repoTarget)
	args := alertArguments{State: "open", Limit: 1}
	if err := listAlertsCheck("code-scanning", codeScanningStates, codeScanningSeverities)(&args, bound); err != nil {
		t.Fatal(err)
	}
	old := fingerprint("security-alerts", "code-scanning", bound.String(), "open", "", "", "", "", "")
	if string(args.binding) != string(old) {
		t.Errorf("fingerprint without the new filters changed")
	}
}
