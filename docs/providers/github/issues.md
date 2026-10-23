---
description: >
  Describes the GitHub issue tools: issue and comment reads and changes, labels, milestones, sub-issues, issue dependencies, issue types, issue fields, reactions, assigning Copilot to an issue, issue search, and the token each needs.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# GitHub issues

The issue tools of the [GitHub provider](README.md). Every tool takes `repository` as described under
[the target of a call](README.md#the-target-of-a-call); a change needs the confirmation described under
[confirmed changes](README.md#confirmed-changes).

## Reading issues and comments

`github.issues.list` and `github.issues.get` read issues only, newest first, without comments.
`github.issues.list` names the repository once at the result level and leaves the URL out of each issue,
derivable from the repository and the number. `github.comments.list` reads the comments of one issue, oldest
first, and is the only issue tool that returns comments. It and `github.pull-request-comments.list` answer
GitHub's issue comment in full. The object a call names is fixed by the call and not repeated in every entry,
so a comment carries no issue, pull request, or repository. Rendered variants repeat the text field in another
format and add no content. A comment's reactions and edit history are served as lists of their own, so it
reports their counts, and reaction groups only for kinds somebody gave.

`github.issues.get`, the issue changes, `github.issues.search`, `github.sub-issues.list`, and
`github.issue-dependencies.list` answer GitHub's issue in full without the rendered body variants. The issue
tools refuse pull requests, so an issue answer never carries the pull request member. An issue's repository
object is answered only where the call does not fix the repository, and a sub-issue leaves out the address of
its parent, the issue the call named.

## Changes

| Tool | Does |
| --- | --- |
| `github.issues.create` | opens one issue |
| `github.issues.update` | replaces the given fields; left-out fields stay |
| `github.issues.close` | closes an issue with a `state_reason` |
| `github.issues.reopen` | opens a closed issue again |
| `github.comments.create` | writes exactly one comment on one issue |
| `github.comments.update` | replaces the body of one comment |
| `github.comments.delete` | deletes one comment permanently; offered only where a connection's `tools` list names it |

Issues and comments are written through REST. `labels` and `assignees` replace the whole set; `[]` removes
every entry. GitHub creates a label the repository does not have yet, so a misspelled name adds a new label;
Qatlas does not check label names beforehand. Before an existing issue changes or gets a comment, Qatlas
reads it once and refuses a pull request of the same number. Bodies and comments are stored exactly as given
and never interpreted. `github.issues.get`, `github.issues.update`, `github.issues.close`,
`github.issues.reopen`, `github.comments.list`, and `github.comments.create` refuse the number of a pull
request as `invalid-request` and name the number and the repository, with a classic and a fine-grained token
alike; a change is never sent. A fine-grained token without access to pull requests is refused by GitHub on
such a number; only then Qatlas looks the number up once, and a refusal that names no pull request stays
`permission`. A pull request's own conversation comments are read and written by
`github.pull-request-comments.list` and `.create`
([Pull request reviews and comments](pull-requests.md#pull-request-reviews-and-comments)); an issue number
given to either is refused as `invalid-request`.

```text
qatlas: invalid-request: number 39 in repository octo-org/example is a pull request; issue tools do not handle pull requests
```

`github.issues.update` sets the milestone of an issue by the number `github.milestones.list` reports. `0` removes
the issue's current milestone, since a milestone number is never `0` and GitHub itself is asked with an explicit
`null` in that case. A milestone-only change is enough on its own.

`github.comments.update` and `github.comments.delete` address one existing issue comment by its `comment_id`,
the numeric identifier GitHub reports in a comment's URL after `#issuecomment-`. The `id` of
`github.comments.list` and `github.comments.create` is a GraphQL identifier the issue comment REST route does
not accept, so every comment tool also reports the numeric one as `database_id`; the reaction tools under
[Reactions](#reactions) take it for kind `issue_comment`. `github.pull-request-review-comments.list` and
`.reply` report the numeric identifier a line comment needs as `id`, since GitHub gives that route no GraphQL
identifier. Both change tools read the comment first: its route scopes it to the connection's bound
repository, so a comment of another repository answers `not-found`, and a pull request's own conversation
comment is refused as `invalid-request` before anything changes. `github.comments.delete` removes a comment
permanently and is offered only where a connection's `tools` list names it, as `github.labels.delete` is.

## Issue search

`github.issues.search` searches issues, never pull requests: it forces `is:issue` ahead of `terms`, next to the
target qualifiers described under [Search](README.md#search). Its filters are arguments of their own, never
part of `terms`. With `advanced_search` or `search_type` set, GitHub reads the query differently, so every match
is also checked against the connection's targets, and an answer outside them is refused as an invalid response.

## Labels

`github.labels.list` and `github.labels.get` read the labels of a repository; `github.labels.create` and
`github.labels.update` change them; `github.labels.delete` removes one permanently and is offered only where a
connection's `tools` list names it, since it also strips the label from every issue and pull request that
carries it. A label is addressed by its `name`, matched exactly and URL-encoded as one opaque path segment, so
a name with a space or another special character still reaches the right label.

| Tool | Does |
| --- | --- |
| `github.labels.list` | lists one bounded batch of the labels of a repository |
| `github.labels.get` | reads one label by name |
| `github.labels.create` | creates one new label; a name GitHub already holds is refused with a clear message |
| `github.labels.update` | changes a label's name, color, or description; at least one of them is required |
| `github.labels.delete` | deletes one label permanently; offered only where a connection's `tools` list names it |

`github.labels.list` is paged the same way [branches and tags](code.md#repository-contents-tree-blame-commits-branches-and-tags)
are, by GitHub's `Link` response header; see the [cursor contract](README.md#cursor-contract).

`github.labels.create` refuses a name the repository already holds as an `invalid-request` that names the
repeated name, checked by GitHub, rather than with the generic "rejected as invalid" message. Every argument
is checked before a credential is resolved, and a name or description carrying a control character is
refused.

GitHub keeps a label under a repository's issues and pull requests rather than under a scope of its own.
Reading the labels of a repository needs no scope for a public repository, or `repo` on a classic token, or
Issues: read on a fine-grained token, for a private one; changing them, including the delete which is
listed-only, needs Issues: read and write instead:

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.labels.list`, `github.labels.get` | `repo` (private) or none (public) | Issues: read |
| `github.labels.create`, `github.labels.update`, `github.labels.delete` | `repo` | Issues: read and write |

## Milestones

`github.milestones.list` reads one bounded batch of the milestones of a repository, paged the same way
[labels](#labels) are, by GitHub's `Link` response header. The description of a milestone is untrusted data.

Nothing here creates, changes, or deletes a milestone; `github.issues.update` sets or removes the milestone
of an existing issue instead, under [Changes](#changes). Reading milestones needs the same token as reading
[labels](#labels).

## Sub-issues

`github.sub-issues.list` reads one bounded batch of the sub-issues of one issue in their priority order;
`github.sub-issues.add` adds one and `github.sub-issues.reprioritize` moves one, both needing confirmation;
`github.sub-issues.remove` detaches one and needs confirmation as well, but is not listed-only, because it only
removes the relationship between the two named issues and deletes neither. A sub-issue is named by its issue
`number`, in the bound repository unless `sub_issue_repository` (as `OWNER/REPO`) names another one; that other
repository must lie inside the connection's targets as well, checked before any credential is resolved, since
GitHub allows a sub-issue relationship to cross repositories and organizations.

`github.sub-issues.list` pages the same way [labels](#labels) do. When the connection's targets name any, a
sub-issue whose repository they do not cover is left out and counted in `withheld` instead, so nothing of an
out-of-scope issue is reported; the bound repository's own sub-issues are always shown. `github.sub-issues.add`,
`.remove`, and `.reprioritize` resolve the issue numbers they are given into the internal issue identifiers the
REST routes need, in the same request budget as the change. `github.sub-issues.reprioritize` takes exactly one
of `after_number` or `before_number`, looked up in the same repository as `sub_issue_number`; this is narrower
than GitHub's own cross-repository reordering, which Qatlas does not offer a way to name.

Reading the sub-issues of an issue needs the same token as reading [labels](#labels); adding, removing, or
reprioritizing needs Issues: read and write instead, of every repository a sub-issue names.

Source: the official REST documentation (<https://docs.github.com/en/rest/issues/sub-issues>,
`X-GitHub-Api-Version: 2022-11-28`): List sub-issues, Add a sub-issue, Remove a sub-issue, and Reprioritize a
sub-issue are stable, generally available REST endpoints, not a preview feature.

## Issue dependencies

`github.issue-dependencies.list` reads both directions of one issue's dependencies: the issues that block it
and the issues it blocks. `github.issue-dependencies.add` records that one issue is blocked by another and needs
confirmation; `github.issue-dependencies.remove` detaches one blocked-by relationship and needs confirmation as
well, but, like removing a sub-issue, only detaches the relationship and deletes neither issue, so it is not
listed-only. GitHub exposes no route to add or remove a "blocking" entry directly; every change is made from
the blocked issue's own blocked-by list, and the blocking direction is only ever read. The blocking issue may
live in another repository, named by `blocking_issue_repository` (as `OWNER/REPO`), which must lie inside the
connection's targets as well, checked before any credential is resolved.

| Tool | Does |
| --- | --- |
| `github.issue-dependencies.list` | lists the issues blocking one issue and the issues it blocks |
| `github.issue-dependencies.add` | records that one issue is blocked by another |
| `github.issue-dependencies.remove` | detaches one blocked-by relationship |

`github.issue-dependencies.list` pages both directions together with the same page and page size, so a single
cursor advances both, which is narrower than paging each direction on its own. It withholds a dependency outside
the targets the way `github.sub-issues.list` withholds a sub-issue, counted across both directions.
`github.issue-dependencies.add` and `.remove` resolve the blocking issue's number into its internal identifier
the way the sub-issue tools do.

Reading the dependencies of an issue needs the same token as reading [labels](#labels); adding or removing
needs Issues: read and write instead, of every repository a dependency names.

Source: the official REST documentation (<https://docs.github.com/en/rest/issues/issue-dependencies>,
`X-GitHub-Api-Version: 2022-11-28`): List issues blocked by, List issues blocking, and Add and Remove a
blocked-by dependency are stable, generally available REST endpoints, not a preview feature.

## Issue types

`github.issue-types.list` reads every issue type an organization declares. GitHub scopes issue types to an
organization as a whole, never to one repository or one user, so the tool takes an `owner` argument (as
`orgs/LOGIN`) checked the same broad way `github.repositories.list` checks its own owner argument: an explicit
owner target, a repository target of that organization, or a connection without targets allow the call; a
connection whose targets name only a project, or only a different organization, refuses it as an invalid
request before any credential is resolved.

`github.issues.list`, `github.issues.get`, `github.issues.search`, the issue changes, `github.sub-issues.list`,
and `github.issue-dependencies.list` name an issue's type as `issue_type`, by the name `github.issue-types.list`
reports; every tool that answers GitHub's issue in full carries the type object beside it as
`issue_type_detail`. `github.issues.update` sets an issue's type with its optional `type` argument, or removes it
with `""`. GitHub silently drops a type change from a token without push access to the repository rather than
refusing it, so the answer shows the type GitHub kept.

Defining, enabling, or disabling an organization's issue types (`POST`/`PUT`/`DELETE
/orgs/{org}/issue-types`) is organization administration, out of this provider's scope; it is not offered.

Reading the issue types of an organization needs `read:org` on a classic token, or, as far as GitHub documents
it, the organization permission Issue types: read, or Administration: read, on a fine-grained token; setting an
issue's type through `github.issues.update` needs the same scope as writing an issue: `repo` on a classic
token, or Issues: read and write on a fine-grained token.

Source: the official REST documentation and the OpenAPI description that backs it
(<https://docs.github.com/en/rest/orgs/issue-types>, <https://github.com/github/rest-api-description>,
`X-GitHub-Api-Version: 2022-11-28`): "List issue types for an organization" (`GET /orgs/{org}/issue-types`) is
a stable, generally available REST endpoint, not a preview feature, returning every issue type as a plain
array with no pagination parameters of its own; "Update an issue" (`PATCH /repos/{owner}/{repo}/issues/{issue_number}`)
documents its `type` request body property as `null`, a plain type-name string, or an object with metadata Qatlas
does not send (see [Issue fields](#issue-fields)), and returns the resulting `type` as a full object.

## Issue fields

`github.issue-fields.list` reads one bounded batch of the organization issue fields of a repository, inherited
from its organization, or, with `organization` instead of `repository`, directly of an organization. Exactly one
of `repository` or `organization` is required, never both; a repository call is checked the way every other
repository tool is, and the organization-wide call the same broad way `github.issue-types.list` checks its own
owner argument. `github.issue-fields.set` writes one or more field values on one issue of a repository the
connection allows, each named by the `field_id` (and, for a select field, the option's own `id`)
`github.issue-fields.list` reports; GitHub applies every value of one call as a single mutation, so it either
applies as a whole or not at all.

| Tool | Does |
| --- | --- |
| `github.issue-fields.list` | lists the issue fields of a repository or an organization |
| `github.issue-fields.set` | sets or clears one or more issue field values |

`github.issue-fields.set` does not send the `confidence`, `rationale`, and `suggest` properties of GitHub's
`IssueFieldCreateOrUpdateInput`: the official `github/github-mcp-server` (MIT) exposes them only behind its own
`update_issue_suggestions` GraphQL feature-flag context, an agent-suggestion workflow of that server's own, not
a capability GitHub documents as generally available to every token.

Reading the issue fields of a repository or an organization needs `read:org` on a classic token, or, as far as
GitHub documents it, the organization permission Issue types: read, or Administration: read, on a fine-grained
token; setting a value needs the same scope as writing an issue: `repo` on a classic token, or Issues: read and
write on a fine-grained token.

Source: GitHub's public GraphQL reference schema
(<https://docs.github.com/public/fpt/schema.docs.graphql>, the schema backing
<https://docs.github.com/en/graphql>): `Repository.issueFields` and `Organization.issueFields` and the
`setIssueFieldValue` mutation carry no `@preview` marker in this schema, the same stability signal
[Sub-issues](#sub-issues) and [Issue dependencies](#issue-dependencies) were checked by; they are not reachable
from the rendered pages under <https://docs.github.com/en/graphql/reference>, so this rests on the schema
alone, not on a rendered reference page.

## Reactions

`github.reactions.add` and `github.reactions.remove` react to one issue, one issue or pull request
conversation comment, or one pull request review line comment of the bound repository, with the account
behind the connection's token. The target is named by `kind` and a numeric `id`:

| `kind` | `id` | Addresses |
| --- | --- | --- |
| `issue` | an issue or pull request number | an issue, or a pull request's own conversation as a whole, since a pull request conversation is an issue for GitHub's reaction routes |
| `issue_comment` | the numeric `database_id` `github.comments.list`, `.create`, `github.pull-request-comments.list`, and `.create` report | an issue comment or a pull request conversation comment alike |
| `review_comment` | the `id` `github.pull-request-review-comments.list` and `.reply` report | one pull request line comment |

Every `id` is addressed below the connection's bound repository, so an `id` of another repository, or one GitHub
does not hold, answers `not-found` before anything changes; unlike `github.comments.update` and `github.comments.delete`,
kind `issue_comment` accepts a pull request conversation comment, since GitHub keeps its reactions under the
same route as an issue comment's.

| Tool | Does |
| --- | --- |
| `github.reactions.add` | adds the account's reaction with one `content` |
| `github.reactions.remove` | removes the account's own reaction of one `content` |

`github.reactions.add` sends one `POST`, which GitHub itself answers idempotently: with the reaction newly
created, or with the one this account already held for this content, never a duplicate; `added` reports which
of the two happened. `github.reactions.remove` never accepts a reaction identifier from its caller, which
would risk deleting another account's reaction: it reads the account behind the connection's token, lists the
existing reactions of this `content`, and removes only the one this account left, when there is one; a
reaction of another account is never touched.

GitHub keeps a reaction under the resource it reacts to rather than a scope of its own, so reacting needs the
same scope as writing that resource. `github.reactions.remove` additionally reads the account behind the
token, which needs no scope beyond its own identity on either kind of token:

| Kind | Classic token | Fine-grained token |
| --- | --- | --- |
| `issue`, `issue_comment` | `repo` | Issues: read and write |
| `review_comment` | `repo` | Pull requests: read and write |

## Copilot assignments

`github.copilot-assignments.create` assigns the Copilot coding agent to one issue of the bound repository and
keeps the other assignees. It first reads the issue and the actors the repository lists as assignable, all in the
bound repository. `rationale` and `confidence` record why Copilot was chosen and must be named together;
`is_suggestion=true` then records only a pending assignment that does not start the agent and excludes `base_ref`
and `custom_instructions`. Without any of the three intent fields the plain assignment is sent. Where Copilot is
not among the assignable actors (no Copilot license with the coding agent, or the agent is not enabled for the
repository) the call fails as `permission` with that reason and changes nothing; an issue that the repository
does not hold is `not-found`. The tool is non-idempotent: a repeated call asks the agent again. It does not wait
for what Copilot does afterwards.

It covers the official tools `assign_copilot_to_issue` and `assign_copilot_to_issue_with_intent` of the GitHub MCP
server (MIT, `pkg/github/copilot.go` at commit 85598ba) in one tool per operation. Unlike the official assignment
tools it does not poll for the pull request the agent opens later.

Both Copilot tools, this one and [`github.copilot-reviews.request`](pull-requests.md#copilot-reviews), need a user
token, never a GitHub App installation token.

| Tool | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.copilot-assignments.create` | `repo` | Metadata: read; Actions, Contents, Issues, and Pull requests: read and write |

Sources: <https://docs.github.com/en/copilot/how-tos/use-copilot-agents/cloud-agent/use-cloud-agent-via-the-api> (user
token only, the fine-grained permissions for the assignment, and the check of Copilot among the repository's
`suggestedActors`). The docs name no classic scope for the assignment; `repo` is the scope of the issue and pull
request routes it changes.
