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
		want := []string{p.id + ".accounts.me", p.id + ".assignees.list", p.id + ".comments.create",
			p.id + ".comments.delete", p.id + ".comments.get", p.id + ".comments.list",
			p.id + ".comments.list-repository", p.id + ".comments.update", p.id + ".comment-reactions.add", p.id + ".comment-reactions.list",
			p.id + ".comment-reactions.remove", p.id + ".issue-reactions.add", p.id + ".issue-reactions.list",
			p.id + ".issue-reactions.remove", p.id + ".issue-config.get", p.id + ".issue-dependencies.add",
			p.id + ".issue-dependencies.list", p.id + ".issue-dependencies.remove", p.id + ".issue-blocks.add",
			p.id + ".issue-blocks.list", p.id + ".issue-blocks.remove",
			p.id + ".issue-config.validate", p.id + ".issue-deadlines.set", p.id + ".issue-labels.add",
			p.id + ".issue-labels.clear", p.id + ".issue-labels.list", p.id + ".issue-labels.remove",
			p.id + ".issue-labels.replace", p.id + ".issue-templates.list", p.id + ".issue-timeline.list",
			p.id + ".issues.close", p.id + ".issues.create", p.id + ".issues.delete", p.id + ".issues.get",
			p.id + ".issues.list", p.id + ".issues.reopen", p.id + ".issues.search", p.id + ".issues.update",
			p.id + ".labels.create", p.id + ".labels.delete", p.id + ".labels.get", p.id + ".labels.list",
			p.id + ".labels.update", p.id + ".markdown.render", p.id + ".markup.render", p.id + ".milestones.create",
			p.id + ".milestones.delete", p.id + ".milestones.get", p.id + ".milestones.list",
			p.id + ".milestones.update", p.id + ".org-labels.create", p.id + ".org-labels.delete",
			p.id + ".org-labels.get", p.id + ".org-labels.list", p.id + ".org-labels.update",
			p.id + ".release-assets.delete", p.id + ".release-assets.download", p.id + ".release-assets.get",
			p.id + ".release-assets.list", p.id + ".release-assets.update", p.id + ".release-assets.upload",
			p.id + ".releases.create", p.id + ".releases.delete", p.id + ".releases.delete-by-tag",
			p.id + ".releases.get", p.id + ".releases.get-by-tag", p.id + ".releases.get-latest",
			p.id + ".releases.list", p.id + ".releases.update", p.id + ".repositories.get", p.id + ".repositories.list",
			p.id + ".settings.get", p.id + ".signing-keys.get", p.id + ".tags.create", p.id + ".tags.delete",
			p.id + ".tags.get", p.id + ".tags.list", p.id + ".version.get"}
		if p == forgejo {
			want = append(want, p.id+".nodeinfo.get")
			slices.Sort(want)
		}
		if p == gitea {
			want = append(want, p.id+".assignees.check", p.id+".issue-assignees.add", p.id+".issue-assignees.check",
				p.id+".issue-assignees.remove", p.id+".projects.list", p.id+".projects.get", p.id+".projects.create",
				p.id+".projects.update", p.id+".projects.delete", p.id+".project-columns.list",
				p.id+".project-columns.get", p.id+".project-columns.create", p.id+".project-columns.update",
				p.id+".project-columns.delete", p.id+".project-columns.move", p.id+".project-columns.set-default",
				p.id+".project-issues.list", p.id+".project-issues.add", p.id+".project-issues.remove",
				p.id+".project-issues.move")
			slices.Sort(want)
		}
		want = append(want, p.id+".issue-times.add", p.id+".issue-times.delete", p.id+".issue-times.list",
			p.id+".issue-times.reset", p.id+".issue-stopwatches.delete", p.id+".issue-stopwatches.start",
			p.id+".issue-stopwatches.stop", p.id+".my-stopwatches.list", p.id+".my-times.list",
			p.id+".repository-times.list", p.id+".user-times.list")
		want = append(want, p.id+".issue-attachments.delete", p.id+".issue-attachments.download",
			p.id+".issue-attachments.get", p.id+".issue-attachments.list", p.id+".issue-attachments.update",
			p.id+".issue-attachments.upload", p.id+".comment-attachments.delete", p.id+".comment-attachments.download",
			p.id+".comment-attachments.get", p.id+".comment-attachments.list", p.id+".comment-attachments.update",
			p.id+".comment-attachments.upload")
		want = append(want, p.id+".commit-pull-requests.get", p.id+".pull-request-commits.list",
			p.id+".pull-request-diffs.get", p.id+".pull-request-files.list", p.id+".pull-requests.check-merged",
			p.id+".pull-requests.get", p.id+".pull-requests.get-by-branches", p.id+".pull-requests.list",
			p.id+".pull-requests.list-pinned", p.id+".pull-request-branches.update",
			p.id+".pull-requests.cancel-auto-merge", p.id+".pull-requests.close", p.id+".pull-requests.create",
			p.id+".pull-requests.merge", p.id+".pull-requests.reopen", p.id+".pull-requests.update")
		want = append(want, p.id+".issue-pins.list", p.id+".issue-pins.check-allowed", p.id+".issue-pins.pin",
			p.id+".issue-pins.unpin", p.id+".issue-pins.move", p.id+".issue-subscriptions.list",
			p.id+".issue-subscriptions.check", p.id+".issue-subscriptions.add", p.id+".issue-subscriptions.remove")
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
	for _, shared := range []*tool{accountsMe, repositoriesList, repositoriesGet, settingsGet, versionGet,
		signingKeysGet, markdownRender, markupRender, issuesList, issuesSearch, issuesGet, issuesCreate, issuesUpdate,
		issuesClose, issuesReopen, issuesDelete, issueDeadlinesSet, issueTimelineList, issueTemplatesList,
		issueConfigGet, issueConfigValidate, assigneesList, labelsList, labelsGet, labelsCreate, labelsUpdate,
		labelsDelete, orgLabelsList, orgLabelsGet, orgLabelsCreate, orgLabelsUpdate, orgLabelsDelete, issueLabelsList,
		issueLabelsAdd, issueLabelsReplace, issueLabelsRemove, issueLabelsClear, milestonesList, milestonesGet,
		milestonesCreate, milestonesUpdate, milestonesDelete, pullRequestsList, pullRequestsGet,
		pullRequestsGetByBranches, pullRequestsListPinned, pullRequestsCheckMerged, commitPullRequestsGet,
		pullRequestFilesList, pullRequestCommitsList, pullRequestDiffsGet, releasesList, releasesGet, releasesGetLatest,
		releasesGetByTag, releasesCreate, releasesUpdate, releasesDelete, releasesDeleteByTag, pullRequestsCreate,
		pullRequestsUpdate, pullRequestsClose, pullRequestsReopen, pullRequestsCancelAutoMerge, pullRequestsMerge,
		pullRequestBranchesUpdate, releaseAssetsList, releaseAssetsGet, releaseAssetsDownload, releaseAssetsUpload,
		releaseAssetsUpdate, releaseAssetsDelete} {
		for _, p := range products {
			if _, _, ok := reg.Lookup(p.toolID(shared)); !ok {
				t.Errorf("%s is missing", p.toolID(shared))
			}
		}
	}
}

func TestAssigneeToolsAreGiteaOnlyExceptTheList(t *testing.T) {
	reg := registry(t)
	for _, only := range []*tool{assigneesCheck, issueAssigneesAdd, issueAssigneesRemove, issueAssigneesCheck} {
		if _, _, ok := reg.Lookup(forgejo.toolID(only)); ok {
			t.Errorf("%s is registered for Forgejo", forgejo.toolID(only))
		}
		if _, _, ok := reg.Lookup(gitea.toolID(only)); !ok {
			t.Errorf("%s is missing", gitea.toolID(only))
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
