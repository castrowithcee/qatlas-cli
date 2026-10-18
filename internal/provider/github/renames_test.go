package github

import (
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// currentToolIDs is the complete set of GitHub tool IDs.
var currentToolIDs = []string{
	"github.accounts.me",
	"github.actions-permissions.get",
	"github.actions-permissions.update",
	"github.blame.get",
	"github.branches.create",
	"github.branches.list",
	"github.code-quality-findings.get",
	"github.code-scanning-alerts.get",
	"github.code-scanning-alerts.list",
	"github.code.search",
	"github.collaborators.list",
	"github.comments.create",
	"github.comments.delete",
	"github.comments.list",
	"github.comments.update",
	"github.commits.get",
	"github.commits.list",
	"github.commits.search",
	"github.contents.delete",
	"github.contents.get",
	"github.contents.put",
	"github.copilot-assignments.create",
	"github.copilot-reviews.request",
	"github.custom-properties.get",
	"github.custom-properties.set",
	"github.dependabot-alerts.get",
	"github.dependabot-alerts.list",
	"github.discussion-categories.list",
	"github.discussion-comments.create",
	"github.discussion-comments.delete",
	"github.discussion-comments.list",
	"github.discussion-comments.update",
	"github.discussions.create",
	"github.discussions.get",
	"github.discussions.list",
	"github.files.push",
	"github.gists.create",
	"github.gists.delete",
	"github.gists.get",
	"github.gists.list",
	"github.gists.update",
	"github.global-advisories.get",
	"github.global-advisories.list",
	"github.issue-dependencies.add",
	"github.issue-dependencies.list",
	"github.issue-dependencies.remove",
	"github.issue-fields.list",
	"github.issue-fields.set",
	"github.issue-types.list",
	"github.issues.close",
	"github.issues.create",
	"github.issues.get",
	"github.issues.list",
	"github.issues.reopen",
	"github.issues.search",
	"github.issues.update",
	"github.labels.create",
	"github.labels.delete",
	"github.labels.get",
	"github.labels.list",
	"github.labels.update",
	"github.milestones.list",
	"github.notifications.dismiss",
	"github.notifications.get",
	"github.notifications.list",
	"github.notifications.mark-all",
	"github.organization-advisories.list",
	"github.organizations.search",
	"github.project-collaborators.update",
	"github.project-drafts.convert",
	"github.project-drafts.create",
	"github.project-drafts.update",
	"github.project-field-options.delete",
	"github.project-fields.create",
	"github.project-fields.delete",
	"github.project-fields.list",
	"github.project-fields.update",
	"github.project-issues.create",
	"github.project-items.add",
	"github.project-items.archive",
	"github.project-items.delete",
	"github.project-items.get",
	"github.project-items.list",
	"github.project-items.move",
	"github.project-items.unarchive",
	"github.project-items.update",
	"github.project-iterations.replace",
	"github.project-status.create",
	"github.project-status.delete",
	"github.project-status.list",
	"github.project-status.update",
	"github.project-teams.list",
	"github.project-templates.mark",
	"github.project-templates.unmark",
	"github.project-views.create",
	"github.project-views.delete",
	"github.project-views.list",
	"github.project-views.update",
	"github.project-workflows.delete",
	"github.project-workflows.list",
	"github.projects.copy",
	"github.projects.create",
	"github.projects.delete",
	"github.projects.link",
	"github.projects.link-team",
	"github.projects.list",
	"github.projects.unlink",
	"github.projects.unlink-team",
	"github.projects.update",
	"github.pull-request-branches.update",
	"github.pull-request-checks.list",
	"github.pull-request-comments.create",
	"github.pull-request-comments.list",
	"github.pull-request-commits.list",
	"github.pull-request-diffs.get",
	"github.pull-request-files.list",
	"github.pull-request-review-comments.list",
	"github.pull-request-review-comments.reply",
	"github.pull-request-review-threads.list",
	"github.pull-request-review-threads.resolve",
	"github.pull-request-review-threads.unresolve",
	"github.pull-request-reviewers.remove",
	"github.pull-request-reviewers.request",
	"github.pull-request-reviews.approve",
	"github.pull-request-reviews.create",
	"github.pull-request-reviews.list",
	"github.pull-requests.close",
	"github.pull-requests.create",
	"github.pull-requests.get",
	"github.pull-requests.list",
	"github.pull-requests.merge",
	"github.pull-requests.reopen",
	"github.pull-requests.search",
	"github.pull-requests.update",
	"github.reactions.add",
	"github.reactions.remove",
	"github.release-assets.list",
	"github.releases.create",
	"github.releases.delete",
	"github.releases.get",
	"github.releases.list",
	"github.releases.update",
	"github.repositories.create",
	"github.repositories.delete",
	"github.repositories.fork",
	"github.repositories.list",
	"github.repositories.search",
	"github.repository-advisories.list",
	"github.repository-settings.get",
	"github.repository-settings.update",
	"github.repository-subscriptions.set",
	"github.rulesets.create",
	"github.rulesets.delete",
	"github.rulesets.get",
	"github.rulesets.list",
	"github.rulesets.update",
	"github.secret-scanning-alerts.get",
	"github.secret-scanning-alerts.list",
	"github.stars.add",
	"github.stars.list",
	"github.stars.remove",
	"github.sub-issues.add",
	"github.sub-issues.list",
	"github.sub-issues.remove",
	"github.sub-issues.reprioritize",
	"github.tags.get",
	"github.tags.list",
	"github.team-members.list",
	"github.teams.list",
	"github.thread-subscriptions.set",
	"github.trees.get",
	"github.users.search",
	"github.workflow-artifacts.list",
	"github.workflow-files.create",
	"github.workflow-files.get",
	"github.workflow-files.list",
	"github.workflow-files.update",
	"github.workflow-jobs.get",
	"github.workflow-jobs.list",
	"github.workflow-jobs.log",
	"github.workflow-permissions.get",
	"github.workflow-permissions.update",
	"github.workflow-run-logs.delete",
	"github.workflow-runs.cancel",
	"github.workflow-runs.get",
	"github.workflow-runs.list",
	"github.workflow-runs.rerun",
	"github.workflow-runs.rerun-failed",
	"github.workflow-runs.usage",
	"github.workflows.disable",
	"github.workflows.dispatch",
	"github.workflows.enable",
	"github.workflows.get",
	"github.workflows.list",
}

func TestRegistryHoldsExactlyTheCurrentToolIDs(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range reg.All() {
		got = append(got, d.ID)
	}
	sort.Strings(got)
	want := append([]string(nil), currentToolIDs...)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") || len(got) != 194 {
		t.Errorf("registry holds %d tools, differing from the expected %d IDs", len(got), len(want))
	}
	if err := reg.ValidateRenames(); err != nil {
		t.Errorf("ValidateRenames() = %v", err)
	}
}

func TestRenamedToolsCoverEveryChangedID(t *testing.T) {
	if len(renamedTools) != 115 {
		t.Errorf("renamedTools has %d entries, want 115", len(renamedTools))
	}
	current := map[string]bool{}
	for _, id := range currentToolIDs {
		current[id] = true
	}
	for old, next := range renamedTools {
		if current[old] || !current[next] {
			t.Errorf("rename %s -> %s: old must be gone and new registered", old, next)
		}
		if strings.ReplaceAll(next, "-", "") != old {
			t.Errorf("rename %s -> %s changes more than word boundaries", old, next)
		}
	}
	// Every ID with a hyphen in a tool segment is the target of exactly one rename.
	targets := map[string]bool{}
	for _, next := range renamedTools {
		targets[next] = true
	}
	for id := range current {
		if strings.Contains(id, "-") && !targets[id] {
			t.Errorf("hyphenated tool %s has no former ID", id)
		}
	}
}

func TestFormerGitHubToolIDsLoadWithWarnings(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	document := `version: 1
services:
  gh:
    provider: github
    base_url: https://api.github.com
credentials:
  gh-reader:
    provider: github
    type: env
    values:
      token: QATLAS_GH_TOKEN
connections:
  planning:
    service: gh
    credential: gh-reader
    tools: [github.projectitems.list, github.pullrequestreviews.list, github.workflowruns.usage]
`
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	warnings := strings.Join(cfg.ToolWarnings(), "\n")
	for old, next := range map[string]string{
		"github.projectitems.list":       "github.project-items.list",
		"github.pullrequestreviews.list": "github.pull-request-reviews.list",
		"github.workflowruns.usage":      "github.workflow-runs.usage",
	} {
		if !strings.Contains(warnings, `tool "`+old+`" was renamed to "`+next+`"`) {
			t.Errorf("warnings lack the rename %s -> %s:\n%s", old, next, warnings)
		}
	}
}
