---
description: >
  Describes the setup profiles of the GitHub provider: the starting selections the terminal editor offers, which tools each ticks, and which tools no profile selects.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# GitHub setup profiles

The terminal editor starts a new connection on the setup profile `read`, which selects the reads
`github.projects.list`, `github.repositories.list`, `github.project-items.list`, `github.project-items.get`,
`github.issues.list`, `github.issues.get`, and `github.comments.list`. The profile `planning` ticks
`[read, create, update]` with the owner lists, the project reads, `github.project-items.update`,
`github.project-items.add`, `github.project-items.move`, `github.project-drafts.create`,
`github.project-drafts.update`, `github.project-drafts.convert`, and `github.project-issues.create`; archiving
and restoring stay unticked. The profile
`projects` selects the owner lists, the
[project lifecycle](projects.md#project-lifecycle) tools except `github.projects.delete`, the template tools,
`github.project-fields.list`, `create`, and `update` of the [project fields](projects.md#project-fields),
`github.project-views.list`, `create`, and `update` of the [project views](projects.md#project-views),
`github.project-status.list`, `create`, and `update` of the [status updates](projects.md#project-status-updates),
`github.project-teams.list` of the [teams](projects.md#project-collaborators-and-teams), and
`github.project-workflows.list` of the [automations](projects.md#project-automations); no profile ticks a tool with the
effect `delete` or one that changes access. The
profiles `actions-observer` and `actions-operator` are described under [GitHub Actions](actions.md#github-actions), the
not-recommended profiles `pull-requests` and `pull-requests-operator` under
[Pull requests](pull-requests.md#pull-requests), the not-recommended profile `pull-request-reviews` under
[Pull request reviews and comments](pull-requests.md#pull-request-reviews-and-comments), the not-recommended profile
`releases` under [Releases](../github.md#releases), the not-recommended profiles `discovery` and `stars` under
[account, organization teams, and stars](../github.md#discovery-account-organization-teams-and-stars), and the
not-recommended profile `repository-reader` under
[repository contents, tree, blame, commits, branches, and tags](../github.md#repository-contents-tree-blame-commits-branches-and-tags),
and the not-recommended profile `repository-admin-reader` under
[repository lifecycle and collaborators](../github.md#repository-lifecycle-and-collaborators), which also lists the
rulesets under [Rulesets](../github.md#rulesets-repository-and-organization-governance) and the custom property values or
schema under [Custom properties](../github.md#custom-properties). The not-recommended profile `issue-maintainer` lists and
reads the labels of a repository, creates and updates them, under [Labels](issues.md#labels), lists the milestones
under [Milestones](issues.md#milestones), updates issue comments under [Changes](issues.md#changes), reacts to issues,
comments, and pull request line comments under [Reactions](issues.md#reactions), lists, adds, removes, and
reprioritizes sub-issues under [Sub-issues](issues.md#sub-issues), lists, adds, and removes issue dependencies
under [Issue dependencies](issues.md#issue-dependencies), and lists issue types, lists issue fields, and sets issue
field values under [Issue types](issues.md#issue-types) and [Issue fields](issues.md#issue-fields); deleting a label or a
comment stays unticked, since each is offered only where a connection's `tools` list names it. The
not-recommended profile `discussions` reads discussion categories, discussions, and discussion comments, starts
discussions, and comments, replies, and edits comments under [Discussions](../github.md#discussions); deleting a
discussion comment stays unticked, since it is offered only where a connection's `tools` list names it. The
not-recommended profile `notifications` lists and reads notifications, marks a thread read or done, and sets
thread and repository subscriptions under [Notifications](../github.md#notifications); marking everything read stays
unticked, since it is offered only where a connection's `tools` list names it. The not-recommended
profile `gists` lists and reads gists and creates and updates them under [Gists](../github.md#gists); deleting a gist stays
unticked, since it is offered only where a connection's `tools` list names it. The not-recommended profile
`security` lists and reads the code scanning, Dependabot, and secret scanning alerts of a repository, the global
and repository security advisories, and code quality findings under [Security alerts](../github.md#security-alerts) and
changes nothing. The not-recommended profile `copilot` assigns Copilot to an issue and requests a Copilot review
of a pull request under [Copilot assignments](issues.md#copilot-assignments) and [Copilot reviews](pull-requests.md#copilot-reviews); every change needs its own confirmation. A
profile is a
visible starting selection, not a role: only the ticked `tools` are saved, every tick can be
changed before saving, and a saved connection never follows a profile.

```yaml
connections:
  roadmap:
    service: github
    credential: github-planner
    targets: [orgs/octo-org/projects/7, repos/octo-org/*]
    tools: [github.project-items.list, github.project-items.get, github.project-items.update,
      github.project-issues.create, github.issues.list, github.issues.get]
```

`roadmap` defaults `project` to its one project. `repository` comes from the call, and only repositories of
`octo-org` are accepted.
