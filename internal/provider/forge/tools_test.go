package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func TestRegisterPublishesBothProviders(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		metadata, ok := reg.ProviderMetadata(p.id)
		if !ok || metadata.Name != p.name || len(metadata.SecretRoles) != 1 || metadata.SecretRoles[0].Name != roleToken ||
			metadata.DefaultBaseURL != "" || metadata.ValidateBaseURL == nil || metadata.Target.Required ||
			!metadata.Target.Multiple || len(metadata.Target.Kinds) != 2 {
			t.Fatalf("%s metadata = %+v", p.id, metadata)
		}
		var ids []string
		for _, d := range reg.Provider(p.id) {
			ids = append(ids, d.ID)
			changes := d.Risk.Effect != capability.EffectRead
			if changes != (d.Risk.Confirmation == capability.ConfirmationRequired) ||
				!strings.HasPrefix(d.Risk.DataSensitivity, p.id+"-") || !d.Risk.OpenWorld {
				t.Errorf("%s risk = %+v", d.ID, d.Risk)
			}
		}
		want := []string{p.id + ".accounts.me", p.id + ".issue-deadlines.set", p.id + ".issue-timeline.list",
			p.id + ".issues.close", p.id + ".issues.create", p.id + ".issues.delete", p.id + ".issues.get",
			p.id + ".issues.list", p.id + ".issues.reopen", p.id + ".issues.search", p.id + ".issues.update",
			p.id + ".markdown.render", p.id + ".markup.render",
			p.id + ".repositories.get", p.id + ".repositories.list", p.id + ".settings.get",
			p.id + ".signing-keys.get", p.id + ".version.get"}
		if p == forgejo {
			want = slices.Insert(want, 13, p.id+".nodeinfo.get")
		}
		want = append(want, p.id+".commit-pull-requests.get", p.id+".pull-request-commits.list",
			p.id+".pull-request-diffs.get", p.id+".pull-request-files.list", p.id+".pull-requests.check-merged",
			p.id+".pull-requests.get", p.id+".pull-requests.get-by-branches", p.id+".pull-requests.list",
			p.id+".pull-requests.list-pinned")
		slices.Sort(want)
		if !slices.Equal(ids, want) {
			t.Errorf("%s tools = %v, want %v", p.id, ids, want)
		}
		profile, ok := metadata.RecommendedProfile()
		if !ok || !slices.Equal(profile.Tools, []string{p.id + ".accounts.me", p.id + ".repositories.list",
			p.id + ".repositories.get", p.id + ".settings.get", p.id + ".version.get"}) {
			t.Errorf("%s profile = %+v", p.id, profile)
		}
	}
	list := func(p *product) string {
		d, _, _ := reg.Lookup(p.toolID(repositoriesList))
		return string(d.InputSchema)
	}
	if !strings.Contains(list(forgejo), `"order_by"`) || strings.Contains(list(gitea), `"order_by"`) {
		t.Error("order_by is documented for Forgejo only")
	}
	get := func(p *product) string {
		d, _, _ := reg.Lookup(p.toolID(repositoriesGet))
		return string(d.OutputSchema)
	}
	for _, name := range []string{`"wiki_branch"`, `"has_wiki_contents"`, `"pronouns"`} {
		if !strings.Contains(get(forgejo), name) || strings.Contains(get(gitea), name) {
			t.Errorf("%s is a Forgejo member only", name)
		}
	}
	for _, name := range []string{`"licenses"`, `"branch_count"`, `"has_code"`, `"type":{"type":"string"}`} {
		if strings.Contains(get(forgejo), name) || !strings.Contains(get(gitea), name) {
			t.Errorf("%s is a Gitea member only", name)
		}
	}
	for _, name := range []string{`"last_login"`, `"login_name"`, `"source_id"`, `"prohibit_login"`} {
		if strings.Contains(get(gitea), name) {
			t.Errorf("the repository owner carries the account detail %s", name)
		}
	}
}

// An operation one product lacks is registered only for the other, under the same group and action.
func TestProductSpecificToolsRegisterOnlyForTheirProduct(t *testing.T) {
	reg := registry(t)
	if _, _, ok := reg.Lookup("forgejo.nodeinfo.get"); !ok {
		t.Error("the Forgejo tool is missing")
	}
	if _, _, ok := reg.Lookup("gitea.nodeinfo.get"); ok {
		t.Error("the Forgejo tool is registered for Gitea")
	}
	for _, shared := range []*tool{accountsMe, repositoriesList, repositoriesGet, settingsGet, versionGet, signingKeysGet,
		markdownRender, markupRender, issuesList, issuesSearch, issuesGet, issuesCreate, issuesUpdate, issuesClose,
		issuesReopen, issuesDelete, issueDeadlinesSet, issueTimelineList, pullRequestsList, pullRequestsGet,
		pullRequestsGetByBranches, pullRequestsListPinned, pullRequestsCheckMerged, commitPullRequestsGet,
		pullRequestFilesList, pullRequestCommitsList, pullRequestDiffsGet} {
		for _, p := range products {
			if _, _, ok := reg.Lookup(p.toolID(shared)); !ok {
				t.Errorf("%s is missing", p.toolID(shared))
			}
		}
	}
}

func TestBaseURLMustBeABareHTTPSOrigin(t *testing.T) {
	for raw, want := range map[string]string{
		"https://forge.example.test":        "https://forge.example.test",
		"https://Forge.Example.test/":       "https://forge.example.test",
		"https://forge.example.test:3000":   "https://forge.example.test:3000",
		" https://forge.example.test ":      "https://forge.example.test",
		"https://codeberg.org":              "https://codeberg.org",
		"https://[2001:db8::1]:8443":        "https://[2001:db8::1]:8443",
		"https://forge.example.test:443/":   "https://forge.example.test:443",
		"https://forge.example.test/":       "https://forge.example.test",
		"https://xn--frge-zra.example.test": "https://xn--frge-zra.example.test",
	} {
		if got, err := originOf(raw); err != nil || got != want {
			t.Errorf("originOf(%q) = %q, %v, want %q", raw, got, err, want)
		}
	}
	metadata := registry(t)
	validate, _ := metadata.ProviderMetadata(gitea.id)
	for _, raw := range []string{
		"", "http://forge.example.test", "ftp://forge.example.test", "https://", "https://forge.example.test/gitea",
		"https://forge.example.test/api/v1", "https://" + "user:secret-canary" + "@forge.example.test",
		"https://forge.example.test?token=secret-canary", "https://forge.example.test/?", "https://forge.example.test#x",
		"https://forge.example.test#", "forge.example.test", "https:forge.example.test",
	} {
		if _, err := originOf(raw); err == nil {
			t.Errorf("originOf(%q) accepted", raw)
		} else if strings.Contains(err.Error(), "secret-canary") || strings.Contains(err.Error(), "forge.example") {
			t.Errorf("originOf(%q) quotes the value: %v", raw, err)
		}
		if err := validate.ValidateBaseURL(raw); err == nil {
			t.Errorf("ValidateBaseURL(%q) accepted", raw)
		}
	}
}

// A base URL a configuration rejects never resolves the token or sends a request.
func TestUnusableBaseURLStopsBeforeTheToken(t *testing.T) {
	var calls []call
	serve(t, &calls, func(call) *http.Response { return notFoundResponse() })
	reads := 0
	resolved := resolvedFor(gitea)
	resolved.BaseURL = "https://forge.example.test/gitea"
	_, err := versionGet.handler(gitea)(context.Background(), resolved, resolver(&redact.Redactor{}, &reads), nil,
		json.RawMessage(`{}`))
	if classOfError(err) != provider.ClassProviderError || reads != 0 || len(calls) != 0 {
		t.Fatalf("err = %v, reads %d, calls %d", err, reads, len(calls))
	}
}

func TestStatusesMapToStableClassesWithoutProviderText(t *testing.T) {
	cases := []struct {
		status int
		header []string
		want   provider.Class
	}{
		{http.StatusUnauthorized, nil, provider.ClassAuth},
		{http.StatusForbidden, nil, provider.ClassNotFound},
		{http.StatusNotFound, nil, provider.ClassNotFound},
		{http.StatusTooManyRequests, []string{"Retry-After", "3"}, provider.ClassRateLimited},
		{http.StatusInternalServerError, nil, provider.ClassProviderError},
		{http.StatusFound, []string{"Location", "https://elsewhere.example.test/login"}, provider.ClassProviderError},
		{http.StatusServiceUnavailable, nil, provider.ClassUnreachable},
	}
	for _, tc := range cases {
		e := newEnvironment(t, gitea, func(call) *http.Response {
			return jsonResponse(tc.status, `{"message":"`+bodyCanary+`","url":"`+bodyCanary+`"}`, tc.header...)
		})
		_, err := e.invoke(t, versionGet, "open", `{}`)
		if classOfError(err) != tc.want {
			t.Errorf("status %d: %v, want %s", tc.status, err, tc.want)
		}
		if len(*e.calls) != 1 || strings.Contains(err.Error(), "elsewhere") {
			t.Errorf("status %d: %d calls, err %v; a redirect is never followed", tc.status, len(*e.calls), err)
		}
	}
}

func TestVersionGetReadsTheReportedVersion(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, giteaAt("28.2.0", nil))
		result, err := e.invoke(t, versionGet, "repo", `{}`)
		if err != nil || result["version"] != "28.2.0" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if paths := e.paths(); !slices.Equal(paths, []string{"/api/v1/version"}) || (*e.calls)[0].auth != "token "+tokenValue {
			t.Errorf("%s: calls %+v", p.id, *e.calls)
		}
	}
}

const selfJSON = `{"id":1,"login":"alice","full_name":"Alice","email":"alice@example.test","avatar_url":"a",` +
	`"html_url":"https://forge.example.test/alice","language":"en-US","is_admin":false,"restricted":false,` +
	`"active":true,"created":"2026-01-01T00:00:00Z","location":"","pronouns":"she/her","website":"",` +
	`"description":"","visibility":"public","type":"individual","followers_count":2,"following_count":3,` +
	`"starred_repos_count":4,"last_login":"2026-10-01T00:00:00Z","login_name":"alice","source_id":0,` +
	`"prohibit_login":false,"unknown":"x"}`

func TestAccountsMeReadsEveryMemberOfTheOwnAccount(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, func(c call) *http.Response {
			if c.path == "/api/v1/user" {
				return jsonResponse(http.StatusOK, selfJSON)
			}
			return notFoundResponse()
		})
		result, err := e.invoke(t, accountsMe, "owner", `{}`)
		if err != nil || result["login"] != "alice" || result["email"] != "alice@example.test" ||
			result["last_login"] != "2026-10-01T00:00:00Z" || result["unknown"] != nil {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if _, ok := result["pronouns"]; ok != (p == forgejo) {
			t.Errorf("%s: pronouns = %v", p.id, result["pronouns"])
		}
		if _, ok := result["type"]; ok != (p == gitea) {
			t.Errorf("%s: type = %v", p.id, result["type"])
		}
	}
}

// A connection scoped to a repository may not read the account behind its token; discovery hides the tool and
// a call is refused before the token is read.
func TestAccountsMeIsRefusedForRepositoryTargets(t *testing.T) {
	e := newEnvironment(t, forgejo, func(call) *http.Response { return jsonResponse(http.StatusOK, selfJSON) })
	_, err := e.invoke(t, accountsMe, "mixed", `{}`)
	if err == nil || len(*e.calls) != 0 || *e.reads != 0 {
		t.Fatalf("err = %v, calls %d, reads %d", err, len(*e.calls), *e.reads)
	}
	reach := reaches(forgejo)
	if ok, _ := reach(forgejo.toolID(accountsMe), []string{"repos/acme/widget"}); ok {
		t.Error("the account read reaches a repository binding")
	}
	if ok, _ := reach(forgejo.toolID(accountsMe), []string{"orgs/acme"}); !ok {
		t.Error("the account read does not reach an owner binding")
	}
	if ok, _ := reach(forgejo.toolID(repositoriesGet), []string{"repos/acme/widget"}); !ok {
		t.Error("the repository read does not reach a repository binding")
	}
}

func TestTargetsParseTheThreeForms(t *testing.T) {
	list, err := parseAllowlist([]string{"users/alice", "orgs/acme", "repos/acme/widget", "repos/bob/my.repo-1"})
	if err != nil || len(list) != 4 {
		t.Fatalf("parseAllowlist = %v, %v", list, err)
	}
	for _, raw := range []string{"", "alice", "users/", "users/a/b", "orgs/-acme", "repos/acme", "repos/acme/..",
		"repos/acme/wid get", "projects/acme/1", "repos/acme/*", "users/" + strings.Repeat("a", 41)} {
		if _, err := parseTarget(raw); err == nil {
			t.Errorf("parseTarget(%q) accepted", raw)
		} else if err.Error() != "a Forgejo or Gitea target must be users/NAME, orgs/NAME, or repos/OWNER/REPO" {
			t.Errorf("parseTarget(%q) = %v, want the fixed form message", raw, err)
		}
	}
	if _, err := parseAllowlist([]string{"users/acme", "orgs/ACME"}); err == nil {
		t.Error("users/NAME and orgs/NAME of one name count twice")
	}
	if _, err := parseAllowlist([]string{"repos/acme/widget", "repos/Acme/Widget"}); err == nil {
		t.Error("a repository named twice in another case is accepted")
	}
	if !list.allowsRepository("ACME", "anything") || !list.allowsRepository("Bob", "MY.repo-1") ||
		list.allowsRepository("bob", "other") || !list.allowsRepository("Alice", "x") {
		t.Error("allowsRepository ignores case or the owner rule")
	}
	if !list.listsOwner("BOB") || list.listsOwner("carol") || list.allowsOwner("bob") {
		t.Error("an owner with only a repository target lists, but is not allowed as a whole")
	}
}

func TestTheTokenIsRegisteredWithTheRedactor(t *testing.T) {
	e := newEnvironment(t, gitea, giteaAt("28.2.0", nil))
	if _, err := e.invoke(t, versionGet, "open", `{}`); err != nil {
		t.Fatal(err)
	}
	if got := e.red.Apply("token " + tokenValue); strings.Contains(got, tokenValue) {
		t.Errorf("the redactor keeps the token: %q", got)
	}
}
