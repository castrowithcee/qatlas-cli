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

// coreFor starts the application core over the discovery connections and the fake at base.
func coreFor(t *testing.T, base string) *application.Core {
	t.Helper()
	red := &redact.Redactor{}
	return application.New(registry(t), discoveryConfig(base), resolver(red, nil), red)
}

const (
	// refApp is the app of GitHub's examples, shortened to the members the projection reads.
	refApp  = `{"id":1,"slug":"octoapp","node_id":"MDExOkludGVncmF0aW9uMQ==","name":"Octocat App","html_url":"https://github.com/apps/octoapp"}`
	refBase = "https://api.github.com/repos/octo-org/example"
)

// The branch protection documented by GitHub's REST reference for "Get branch protection", as the short
// branch carries it.
var documentedProtection = `{"url":"` + refBase + `/branches/main/protection","enabled":true,"name":"main",` +
	`"protection_url":"` + refBase + `/branches/main/protection",` +
	`"required_status_checks":{"url":"` + refBase + `/branches/main/protection/required_status_checks",` +
	`"enforcement_level":"non_admins","strict":true,"contexts":["ci/test"],` +
	`"contexts_url":"` + refBase + `/branches/main/protection/required_status_checks/contexts",` +
	`"checks":[{"context":"ci/test","app_id":15368},{"context":"ci/lint","app_id":null}]},` +
	`"enforce_admins":{"url":"` + refBase + `/branches/main/protection/enforce_admins","enabled":true},` +
	`"required_pull_request_reviews":{"url":"` + refBase + `/branches/main/protection/required_pull_request_reviews",` +
	`"dismissal_restrictions":{"url":"` + refBase + `/dismissal_restrictions","users_url":"` + refBase + `/u",` +
	`"teams_url":"` + refBase + `/t","users":[` + documentedAccount + `],"teams":[` + pullTeam + `],"apps":[` + refApp + `]},` +
	`"bypass_pull_request_allowances":{"users":[],"teams":[],"apps":[` + refApp + `]},` +
	`"dismiss_stale_reviews":true,"require_code_owner_reviews":true,"required_approving_review_count":2,` +
	`"require_last_push_approval":true},` +
	`"restrictions":{"url":"` + refBase + `/restrictions","users_url":"` + refBase + `/restrictions/users",` +
	`"teams_url":"` + refBase + `/restrictions/teams","apps_url":"` + refBase + `/restrictions/apps",` +
	`"users":[` + documentedAccount + `],"teams":[],"apps":[]},` +
	`"required_linear_history":{"enabled":true},"allow_force_pushes":{"enabled":false},` +
	`"allow_deletions":{"enabled":false},"block_creations":{"enabled":true},` +
	`"required_conversation_resolution":{"enabled":true},"required_signatures":{"url":"` + refBase +
	`/branches/main/protection/required_signatures","enabled":true},"lock_branch":{"enabled":false},` +
	`"allow_fork_syncing":{"enabled":true}}`

var documentedBranches = `[{"name":"main","commit":{"sha":"c5b97d5a","url":"` + refBase + `/commits/c5b97d5a"},` +
	`"protected":true,"protection":` + documentedProtection + `,"protection_url":"` + refBase +
	`/branches/main/protection"},{"name":"topic","commit":{"sha":"d6c08e6b","url":"` + refBase +
	`/commits/d6c08e6b"},"protected":false}]`

func TestBranchListReportsEveryDocumentedMember(t *testing.T) {
	f, core := cannedCore(t, documentedBranches, false, discoveryConfig)
	got, err := invoke(t, core, branchesList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if requests := f.recorded(); len(requests) != 1 {
		t.Errorf("requests = %d, want one", len(requests))
	}
	if values, _ := url.ParseQuery(lastRequest(t, f).query); values.Has("protected") {
		t.Errorf("plain request carries protected: %q", lastRequest(t, f).query)
	}
	branch := members(t, got, "branches", "#0")
	wantMembers(t, "branch", branch, map[string]string{
		"name": `"main"`, "protected": `true`, "sha": `"c5b97d5a"`,
		"commit": `{"sha":"c5b97d5a","links":{"url":"` + refBase + `/commits/c5b97d5a"}}`,
		"links":  `{"protection_url":"` + refBase + `/branches/main/protection"}`,
	})
	protection := members(t, got, "branches", "#0", "protection")
	wantMembers(t, "protection", protection, map[string]string{
		"name": `"main"`, "enabled": `true`,
		"links": `{"url":"` + refBase + `/branches/main/protection","protection_url":"` + refBase +
			`/branches/main/protection"}`,
		"required_status_checks": `{"enforcement_level":"non_admins","strict":true,"contexts":["ci/test"],` +
			`"checks":[{"context":"ci/test","app_id":15368},{"context":"ci/lint"}],` +
			`"links":{"url":"` + refBase + `/branches/main/protection/required_status_checks",` +
			`"contexts_url":"` + refBase + `/branches/main/protection/required_status_checks/contexts"}}`,
		"enforce_admins":                   `{"enabled":true,"links":{"url":"` + refBase + `/branches/main/protection/enforce_admins"}}`,
		"required_linear_history":          `{"enabled":true}`,
		"allow_force_pushes":               `{"enabled":false}`,
		"allow_deletions":                  `{"enabled":false}`,
		"block_creations":                  `{"enabled":true}`,
		"required_conversation_resolution": `{"enabled":true}`,
		"lock_branch":                      `{"enabled":false}`,
		"allow_fork_syncing":               `{"enabled":true}`,
		"required_signatures": `{"enabled":true,"links":{"url":"` + refBase +
			`/branches/main/protection/required_signatures"}}`,
	})
	reviews := members(t, got, "branches", "#0", "protection", "required_pull_request_reviews")
	wantMembers(t, "reviews", reviews, map[string]string{
		"dismiss_stale_reviews": `true`, "require_code_owner_reviews": `true`,
		"required_approving_review_count": `2`, "require_last_push_approval": `true`,
		"links": `{"url":"` + refBase + `/branches/main/protection/required_pull_request_reviews"}`,
	})
	dismissal := members(t, got, "branches", "#0", "protection", "required_pull_request_reviews", "dismissal_restrictions")
	wantMembers(t, "dismissal", dismissal, map[string]string{
		"users": `[` + accountWant + `]`,
		"links": `{"url":"` + refBase + `/dismissal_restrictions","users_url":"` + refBase + `/u","teams_url":"` + refBase + `/t"}`,
		"apps": `[{"id":1,"slug":"octoapp","node_id":"MDExOkludGVncmF0aW9uMQ==","name":"Octocat App",` +
			`"url":"https://github.com/apps/octoapp"}]`,
	})
	if teams := string(dismissal["teams"]); !strings.Contains(teams, `"slug":"justice-league"`) ||
		!strings.Contains(teams, `"id":1`) {
		t.Errorf("dismissal teams = %s", teams)
	}
	restrictions := members(t, got, "branches", "#0", "protection", "restrictions")
	wantMembers(t, "restrictions", restrictions, map[string]string{
		"users": `[` + accountWant + `]`,
		"links": `{"url":"` + refBase + `/restrictions","users_url":"` + refBase + `/restrictions/users",` +
			`"teams_url":"` + refBase + `/restrictions/teams","apps_url":"` + refBase + `/restrictions/apps"}`,
	})
	// A branch GitHub sends without protection answers what it always did, plus its commit.
	sameJSON(t, "second", members(t, got, "branches", "#1")["commit"], `{"sha":"d6c08e6b","links":{"url":"`+refBase+`/commits/d6c08e6b"}}`)
	if _, ok := members(t, got, "branches", "#1")["protection"]; ok {
		t.Errorf("an unprotected branch holds a protection")
	}
}

func TestBranchListWithoutNewMembersKeepsItsOldAnswer(t *testing.T) {
	_, core := cannedCore(t, `[{"name":"main","protected":true,"commit":{"sha":"abc"}}]`, false, discoveryConfig)
	got, err := invoke(t, core, branchesList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "list", got, `{"branches":[{"name":"main","protected":true,"sha":"abc","commit":{"sha":"abc"}}],`+
		`"has_more":false,"repository":"octo-org/example"}`)
}

func TestBranchListProtectedReachesTheRequestAndTheCursor(t *testing.T) {
	f, core := cannedCore(t, documentedBranches, true, discoveryConfig)
	plain, err := invoke(t, core, branchesList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"true", "false"} {
		first, err := invoke(t, core, branchesList.ID, "repo", `{"protected":`+value+`}`, false)
		if err != nil {
			t.Fatal(err)
		}
		if values, _ := url.ParseQuery(lastRequest(t, f).query); values.Get("protected") != value {
			t.Errorf("request = %q, want protected=%s", lastRequest(t, f).query, value)
		}
		var page struct {
			NextCursor string `json:"next_cursor"`
		}
		if err := json.Unmarshal(first, &page); err != nil || page.NextCursor == "" {
			t.Fatalf("first page = %s, %v", first, err)
		}
		if _, err := invoke(t, core, branchesList.ID, "repo",
			fmt.Sprintf(`{"protected":%s,"cursor":%q}`, value, page.NextCursor), false); err != nil {
			t.Errorf("continuation = %v", err)
		}
		other := "true"
		if value == "true" {
			other = "false"
		}
		before := len(f.recorded())
		for name, arguments := range map[string]string{
			"another value": fmt.Sprintf(`{"protected":%s,"cursor":%q}`, other, page.NextCursor),
			"no value":      fmt.Sprintf(`{"cursor":%q}`, page.NextCursor),
		} {
			if _, err := invoke(t, core, branchesList.ID, "repo", arguments, false); !isInvalidRequest(err) {
				t.Errorf("%s = %v, want a refused cursor", name, err)
			}
		}
		if len(f.recorded()) != before {
			t.Errorf("a refused cursor reached GitHub")
		}
	}
	// A cursor issued without the filter stays valid without it and is refused with one.
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(plain, &page); err != nil || page.NextCursor == "" {
		t.Fatalf("plain page = %s, %v", plain, err)
	}
	if _, err := invoke(t, core, branchesList.ID, "repo", fmt.Sprintf(`{"cursor":%q}`, page.NextCursor), false); err != nil {
		t.Errorf("plain continuation = %v", err)
	}
	if _, err := invoke(t, core, branchesList.ID, "repo",
		fmt.Sprintf(`{"protected":false,"cursor":%q}`, page.NextCursor), false); !isInvalidRequest(err) {
		t.Errorf("a filter on a plain cursor = %v, want a refused cursor", err)
	}
	if _, err := invoke(t, core, branchesList.ID, "repo", `{"protected":"yes"}`, false); err == nil {
		t.Errorf("a non-boolean filter was accepted")
	}
}

func TestTagListReportsEveryDocumentedMember(t *testing.T) {
	const body = `[{"name":"v0.1","commit":{"sha":"c5b97d5a","url":"` + refBase + `/commits/c5b97d5a"},` +
		`"zipball_url":"https://github.com/octo-org/example/zipball/v0.1",` +
		`"tarball_url":"https://github.com/octo-org/example/tarball/v0.1","node_id":"MDM6UmVmcmVmcy90YWdzL3YwLjE="}]`
	_, core := cannedCore(t, body, false, discoveryConfig)
	got, err := invoke(t, core, tagsList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	wantMembers(t, "tag", members(t, got, "tags", "#0"), map[string]string{
		"name": `"v0.1"`, "sha": `"c5b97d5a"`, "node_id": `"MDM6UmVmcmVmcy90YWdzL3YwLjE="`,
		"commit": `{"sha":"c5b97d5a","links":{"url":"` + refBase + `/commits/c5b97d5a"}}`,
		"links": `{"zipball_url":"https://github.com/octo-org/example/zipball/v0.1",` +
			`"tarball_url":"https://github.com/octo-org/example/tarball/v0.1"}`,
	})

	_, core = cannedCore(t, `[{"name":"v0.1","commit":{"sha":"abc"}}]`, false, discoveryConfig)
	got, err = invoke(t, core, tagsList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "old", got, `{"tags":[{"name":"v0.1","sha":"abc","commit":{"sha":"abc"}}],"has_more":false,"repository":"octo-org/example"}`)
}

// serveTagRoutes answers the Git ref and the Git tag route with fixed bodies and counts the requests.
func serveTagRoutes(t *testing.T, ref, tag string) (*fakeGitHub, string) {
	t.Helper()
	f := &fakeGitHub{}
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/git/refs/tags/"):
			fmt.Fprint(w, ref)
		case strings.Contains(r.URL.Path, "/git/tags/"):
			fmt.Fprint(w, tag)
		default:
			return false
		}
		return true
	}
	return f, serve(t, f)
}

const (
	documentedTagRef = `{"ref":"refs/tags/v1.0.0","node_id":"MDM6UmVmcmVmcy90YWdzL3YxLjAuMA==",` +
		`"url":"` + refBase + `/git/refs/tags/v1.0.0","object":{"type":"tag","sha":"940bd336248efae0f9ee5bc7b2d5c985887b16ac",` +
		`"url":"` + refBase + `/git/tags/940bd336248efae0f9ee5bc7b2d5c985887b16ac"}}`
	documentedTagObject = `{"node_id":"MDM6VGFnOTQwYmQzMzYyNDhlZmFlMGY5ZWU1YmM3YjJkNWM5ODU4ODdiMTZhYw==",` +
		`"tag":"v1.0.0","sha":"940bd336248efae0f9ee5bc7b2d5c985887b16ac","url":"` + refBase +
		`/git/tags/940bd336248efae0f9ee5bc7b2d5c985887b16ac","message":"initial version",` +
		`"tagger":{"name":"Monalisa Octocat","email":"octocat@github.com","date":"2014-11-07T22:01:45Z"},` +
		`"object":{"type":"commit","sha":"c3d0be41ecbe669545ee3e94d31ed9a4bc91ee3c","url":"` + refBase +
		`/git/commits/c3d0be41ecbe669545ee3e94d31ed9a4bc91ee3c"},` +
		`"verification":{"verified":false,"reason":"unsigned","signature":null,"payload":null,"verified_at":null}}`
)

func TestTagGetReportsEveryDocumentedMember(t *testing.T) {
	f, base := serveTagRoutes(t, documentedTagRef, documentedTagObject)
	core := coreFor(t, base)
	got, err := invoke(t, core, tagsGet.ID, "repo", `{"tag":"v1.0.0"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if requests := f.recorded(); len(requests) != 2 || !strings.HasSuffix(requests[0].path, "/git/refs/tags/v1.0.0") ||
		!strings.Contains(requests[1].path, "/git/tags/940bd336") {
		t.Errorf("requests = %+v, want the ref and then the tag object", requests)
	}
	object := members(t, got)
	wantMembers(t, "tag", object, map[string]string{
		"name": `"v1.0.0"`, "type": `"annotated"`, "sha": `"940bd336248efae0f9ee5bc7b2d5c985887b16ac"`,
		"target_sha": `"c3d0be41ecbe669545ee3e94d31ed9a4bc91ee3c"`, "target_type": `"commit"`,
		"tagger": `"Monalisa Octocat"`, "tagged_at": `"2014-11-07T22:01:45Z"`, "message": `"initial version"`,
		"node_id":       `"MDM6VGFnOTQwYmQzMzYyNDhlZmFlMGY5ZWU1YmM3YjJkNWM5ODU4ODdiMTZhYw=="`,
		"links":         `{"url":"` + refBase + `/git/tags/940bd336248efae0f9ee5bc7b2d5c985887b16ac"}`,
		"tagger_detail": `{"name":"Monalisa Octocat","email":"octocat@github.com","date":"2014-11-07T22:01:45Z"}`,
		"object_detail": `{"sha":"c3d0be41ecbe669545ee3e94d31ed9a4bc91ee3c","type":"commit","links":{"url":"` +
			refBase + `/git/commits/c3d0be41ecbe669545ee3e94d31ed9a4bc91ee3c"}}`,
		"verification": `{"verified":false,"reason":"unsigned"}`,
		"ref_detail": `{"ref":"refs/tags/v1.0.0","node_id":"MDM6UmVmcmVmcy90YWdzL3YxLjAuMA==",` +
			`"links":{"url":"` + refBase + `/git/refs/tags/v1.0.0"},` +
			`"object":{"sha":"940bd336248efae0f9ee5bc7b2d5c985887b16ac","type":"tag","links":{"url":"` + refBase +
			`/git/tags/940bd336248efae0f9ee5bc7b2d5c985887b16ac"}}}`,
	})
}

func TestTagGetVerificationIsCutAndMarked(t *testing.T) {
	long := strings.Repeat("p", projectionSignatureLimit+5)
	tag := strings.Replace(documentedTagObject, `"signature":null,"payload":null`, `"signature":"sig","payload":"`+long+`"`, 1)
	_, base := serveTagRoutes(t, documentedTagRef, tag)
	got, err := invoke(t, coreFor(t, base), tagsGet.ID, "repo", `{"tag":"v1.0.0"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	verification := members(t, got, "verification")
	var payload string
	if json.Unmarshal(verification["payload"], &payload) != nil || len(payload) != projectionSignatureLimit ||
		string(verification["payload_truncated"]) != "true" || string(verification["signature"]) != `"sig"` {
		t.Errorf("verification = %s", verification)
	}
}

func TestTagGetLightweightKeepsItsOldAnswerAndAddsTheRef(t *testing.T) {
	ref := `{"ref":"refs/tags/v0.1","node_id":"N1","url":"` + refBase + `/git/refs/tags/v0.1",` +
		`"object":{"type":"commit","sha":"c5b97d5a","url":"` + refBase + `/git/commits/c5b97d5a"}}`
	f, base := serveTagRoutes(t, ref, "")
	got, err := invoke(t, coreFor(t, base), tagsGet.ID, "repo", `{"tag":"v0.1"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if requests := f.recorded(); len(requests) != 1 {
		t.Errorf("requests = %d, want one", len(requests))
	}
	sameJSON(t, "lightweight", got, `{"name":"v0.1","type":"lightweight","sha":"c5b97d5a","repository":"octo-org/example",`+
		`"ref_detail":{"ref":"refs/tags/v0.1","node_id":"N1","links":{"url":"`+refBase+`/git/refs/tags/v0.1"},`+
		`"object":{"sha":"c5b97d5a","type":"commit","links":{"url":"`+refBase+`/git/commits/c5b97d5a"}}}}`)
}
