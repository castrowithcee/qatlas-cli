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
		want = append(want, p.id+".branches.create", p.id+".branches.delete", p.id+".branches.get",
			p.id+".branches.list", p.id+".branches.rename")
		if p == gitea {
			want = append(want, p.id+".branches.update-ref")
		}
		want = append(want, p.id+".repositories.create", p.id+".repositories.update", p.id+".repositories.fork",
			p.id+".repositories.list-forks", p.id+".repositories.generate", p.id+".repositories.sync-fork",
			p.id+".repositories.delete", p.id+".repositories.transfer", p.id+".repositories.accept-transfer",
			p.id+".repositories.reject-transfer", p.id+".repository-mirrors.sync",
			p.id+".repository-avatars.update", p.id+".repository-avatars.delete")
		if p == forgejo {
			want = append(want, p.id+".repositories.get-sync-state", p.id+".repositories.convert")
		}
		want = append(want, p.id+".contents.list", p.id+".contents.get", p.id+".contents.get-raw",
			p.id+".archives.get", p.id+".editorconfig.get", p.id+".contents.create", p.id+".contents.update",
			p.id+".contents.change-files", p.id+".contents.apply-diff", p.id+".contents.delete")
		if p == gitea {
			want = append(want, p.id+".contents.get-ext", p.id+".contents.get-many")
		}
		want = append(want, p.id+".commit-pull-requests.get", p.id+".pull-request-commits.list",
			p.id+".pull-request-diffs.get", p.id+".pull-request-files.list", p.id+".pull-requests.check-merged",
			p.id+".pull-requests.get", p.id+".pull-requests.get-by-branches", p.id+".pull-requests.list",
			p.id+".pull-requests.list-pinned", p.id+".pull-request-branches.update",
			p.id+".pull-requests.cancel-auto-merge", p.id+".pull-requests.close", p.id+".pull-requests.create",
			p.id+".pull-requests.merge", p.id+".pull-requests.reopen", p.id+".pull-requests.update")
		want = append(want, p.id+".issue-pins.list", p.id+".issue-pins.check-allowed", p.id+".issue-pins.pin",
			p.id+".issue-pins.unpin", p.id+".issue-pins.move", p.id+".issue-subscriptions.list",
			p.id+".issue-subscriptions.check", p.id+".issue-subscriptions.add", p.id+".issue-subscriptions.remove")
		want = append(want, p.id+".stars.list", p.id+".stars.check", p.id+".stars.add", p.id+".stars.remove",
			p.id+".repository-stargazers.list", p.id+".repository-subscribers.list", p.id+".repository-subscriptions.get",
			p.id+".repository-subscriptions.set", p.id+".repository-subscriptions.delete", p.id+".subscriptions.list")
		want = append(want, p.id+".issue-locks.lock", p.id+".issue-locks.unlock")
		want = append(want, p.id+".notifications.list", p.id+".notifications.check-new", p.id+".notifications.get",
			p.id+".notifications.mark-thread", p.id+".notifications.mark-all-read",
			p.id+".repository-notifications.list", p.id+".repository-notifications.mark-read")
		want = append(want, p.id+".pull-request-reviews.list", p.id+".pull-request-reviews.get",
			p.id+".pull-request-reviews.create", p.id+".pull-request-reviews.submit",
			p.id+".pull-request-reviews.approve", p.id+".pull-request-reviews.delete",
			p.id+".pull-request-reviews.dismiss", p.id+".pull-request-reviews.undismiss",
			p.id+".pull-request-review-comments.list")
		if p == forgejo {
			want = append(want, p.id+".pull-request-review-comments.get", p.id+".pull-request-review-comments.create",
				p.id+".pull-request-review-comments.delete")
		} else {
			want = append(want, p.id+".pull-request-review-comments.reply", p.id+".pull-request-review-comments.resolve",
				p.id+".pull-request-review-comments.unresolve")
		}
		want = append(want, p.id+".workflow-runs.list", p.id+".workflow-runs.get", p.id+".workflow-runs.delete",
			p.id+".workflow-runs.cancel", p.id+".workflow-runs.rerun", p.id+".workflow-run-logs.get",
			p.id+".workflow-jobs.list", p.id+".workflow-jobs.get", p.id+".workflow-jobs.rerun",
			p.id+".workflow-job-logs.get", p.id+".actions-tasks.list", p.id+".workflow-artifacts.list",
			p.id+".workflow-artifacts.get", p.id+".workflow-artifacts.download", p.id+".workflow-artifacts.delete",
			p.id+".workflows.dispatch")
		if p == gitea {
			want = append(want, p.id+".workflows.list", p.id+".workflows.get")
			want = append(want, p.id+".workflow-runs.force-cancel", p.id+".workflow-runs.rerun-failed",
				p.id+".workflow-runs.approve", p.id+".workflow-run-attempts.get", p.id+".workflow-run-attempts.list-jobs")
		}
		want = append(want, p.id+".commits.list", p.id+".commits.get", p.id+".commits.get-diff", p.id+".compare.get",
			p.id+".commit-statuses.list", p.id+".commit-statuses.get-combined", p.id+".commit-statuses.create",
			p.id+".git-blobs.get", p.id+".git-trees.get", p.id+".git-refs.list", p.id+".git-tags.get",
			p.id+".git-notes.get", p.id+".repositories.search", p.id+".repository-languages.list",
			p.id+".repository-activities.list", p.id+".repository-signing-keys.get")
		if p == forgejo {
			want = append(want, p.id+".git-blobs.list", p.id+".git-notes.set", p.id+".git-notes.delete")
		}
		want = append(want, p.id+".pull-request-reviewers.list", p.id+".pull-request-reviewers.request",
			p.id+".pull-request-reviewers.remove")
		want = append(want, p.id+".actions-secrets.list", p.id+".actions-secrets.set", p.id+".actions-secrets.delete",
			p.id+".actions-variables.list", p.id+".actions-variables.get", p.id+".actions-variables.create",
			p.id+".actions-variables.update", p.id+".actions-variables.delete")
		want = append(want, p.id+".actions-runners.list", p.id+".actions-runners.get", p.id+".actions-runners.delete")
		if p == forgejo {
			want = append(want, p.id+".actions-runner-jobs.search")
		} else {
			want = append(want, p.id+".actions-runners.update", p.id+".workflows.enable", p.id+".workflows.disable")
		}
		want = append(want, p.id+".admin-runners.list", p.id+".admin-runners.get", p.id+".admin-runners.delete")
		if p == forgejo {
			want = append(want, p.id+".admin-runner-jobs.search")
		} else {
			want = append(want, p.id+".admin-runners.update", p.id+".admin-workflow-runs.list",
				p.id+".admin-workflow-jobs.list", p.id+".admin-packages.list")
		}
		want = append(want, p.id+".topics.search", p.id+".repository-topics.list", p.id+".repository-topics.set",
			p.id+".repository-topics.add", p.id+".repository-topics.remove", p.id+".gitignore-templates.list",
			p.id+".gitignore-templates.get", p.id+".label-templates.list", p.id+".label-templates.get",
			p.id+".license-templates.list", p.id+".license-templates.get")
		if p == gitea {
			want = append(want, p.id+".repository-licenses.list")
		}
		want = append(want, p.id+".orgs.list", p.id+".orgs.list-mine", p.id+".orgs.list-user", p.id+".orgs.get",
			p.id+".orgs.create", p.id+".orgs.update", p.id+".orgs.rename", p.id+".orgs.delete",
			p.id+".org-avatars.update", p.id+".org-avatars.delete", p.id+".org-activities.list",
			p.id+".org-permissions.get", p.id+".org-members.list", p.id+".org-members.check", p.id+".org-members.remove",
			p.id+".org-public-members.list", p.id+".org-public-members.check", p.id+".org-public-members.publicize",
			p.id+".org-public-members.conceal", p.id+".org-blocks.list", p.id+".org-blocks.add",
			p.id+".org-blocks.remove")
		if p == gitea {
			want = append(want, p.id+".org-blocks.check")
		}
		want = append(want, p.id+".teams.list", p.id+".teams.search", p.id+".teams.get", p.id+".teams.create",
			p.id+".teams.update", p.id+".teams.delete", p.id+".team-activities.list", p.id+".team-members.list",
			p.id+".team-members.check", p.id+".team-members.add", p.id+".team-members.remove",
			p.id+".team-repositories.list", p.id+".team-repositories.check", p.id+".team-repositories.add",
			p.id+".team-repositories.remove", p.id+".repository-teams.list", p.id+".repository-teams.check",
			p.id+".repository-teams.add", p.id+".repository-teams.remove")
		want = append(want, p.id+".users.search", p.id+".users.get", p.id+".user-activities.list",
			p.id+".user-heatmap.get", p.id+".user-followers.list", p.id+".user-following.list",
			p.id+".user-following.check", p.id+".user-follows.add", p.id+".user-follows.remove")
		want = append(want, p.id+".user-settings.get", p.id+".user-settings.update", p.id+".user-avatars.update",
			p.id+".user-avatars.delete", p.id+".user-emails.list", p.id+".user-emails.delete", p.id+".user-blocks.list",
			p.id+".user-blocks.add", p.id+".user-blocks.remove")
		if p == gitea {
			want = append(want, p.id+".user-blocks.check")
		}
		want = append(want, p.id+".wiki-pages.list", p.id+".wiki-pages.get", p.id+".wiki-pages.create",
			p.id+".wiki-pages.update", p.id+".wiki-pages.delete", p.id+".wiki-revisions.list")
		want = append(want, p.id+".packages.list", p.id+".packages.get", p.id+".package-files.list",
			p.id+".packages.link", p.id+".packages.unlink", p.id+".packages.delete-version")
		if p == gitea {
			want = append(want, p.id+".package-versions.list", p.id+".package-versions.get-latest",
				p.id+".packages.delete")
		}
		want = append(want, accessToolIDs(p)...)
		if p == forgejo {
			want = append(want, p.id+".quota.get", p.id+".quota.list-artifacts", p.id+".quota.list-attachments",
				p.id+".quota.list-packages", p.id+".quota.check")
			want = append(want, quotaAdminToolIDs...)
		}
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
	for _, action := range []string{"get", "list-artifacts", "list-attachments", "list-packages", "check"} {
		if _, _, ok := reg.Lookup("forgejo.quota." + action); !ok {
			t.Errorf("forgejo.quota.%s is missing", action)
		}
		if _, _, ok := reg.Lookup("gitea.quota." + action); ok {
			t.Errorf("gitea.quota.%s is registered", action)
		}
	}
	for _, id := range quotaAdminToolIDs {
		if _, _, ok := reg.Lookup(id); !ok {
			t.Errorf("%s is missing", id)
		}
		if _, _, ok := reg.Lookup("gitea" + strings.TrimPrefix(id, "forgejo")); ok {
			t.Errorf("gitea%s is registered", strings.TrimPrefix(id, "forgejo"))
		}
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

func TestReviewCommentToolsRegisterOnlyForTheirProduct(t *testing.T) {
	reg := registry(t)
	for _, shared := range []*tool{reviewsList, reviewsGet, reviewsCreate, reviewsSubmit, reviewsApprove,
		reviewsDelete, reviewsDismiss, reviewsUndismiss, reviewCommentsList} {
		for _, p := range products {
			if _, _, ok := reg.Lookup(p.toolID(shared)); !ok {
				t.Errorf("%s is missing", p.toolID(shared))
			}
		}
	}
	for _, only := range []*tool{reviewCommentsGet, reviewCommentsCreate, reviewCommentsDelete} {
		if _, _, ok := reg.Lookup(gitea.toolID(only)); ok {
			t.Errorf("%s is registered for Gitea", gitea.toolID(only))
		}
		if _, _, ok := reg.Lookup(forgejo.toolID(only)); !ok {
			t.Errorf("%s is missing", forgejo.toolID(only))
		}
	}
	for _, only := range []*tool{reviewCommentsReply, reviewCommentsResolve, reviewCommentsUnresolve} {
		if _, _, ok := reg.Lookup(forgejo.toolID(only)); ok {
			t.Errorf("%s is registered for Forgejo", forgejo.toolID(only))
		}
		if _, _, ok := reg.Lookup(gitea.toolID(only)); !ok {
			t.Errorf("%s is missing", gitea.toolID(only))
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
