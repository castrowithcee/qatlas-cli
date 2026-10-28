---
description: >
  Describes the GitHub provider as a whole: its role and tool groups, the service and credential, targets as an optional allow-list, target arguments and their defaults, errors, choosing targets, the owner lists, confirmed changes with unclear outcomes and conflicts, and the cursor contract.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-28
---

# GitHub

GitHub is a controlled planning provider, not a replacement for `gh`. It never accepts a free filter expression,
a GraphQL document, or a REST route from the caller. A `repository`, `project`, or `owner` argument names
exactly one target, and it must lie inside the connection's targets when the connection lists any. A connection
offers only what its `effects` or `tools` select; the [setup profiles](profiles.md) are starting selections for
the terminal editor, and a tool marked listed only is offered only when a `tools` list names it.

The tools are sorted into groups; each group has a page: [issues](issues.md), [pull requests](pull-requests.md),
[projects](projects.md), [Actions](actions.md), [code](code.md), [repositories](repositories.md),
[releases](releases.md), [security](security.md), [discussions](discussions.md), [gists](gists.md),
[notifications](notifications.md), and [accounts](accounts.md). The rules every search tool shares are
described under [search](search.md). The [setup profiles](profiles.md) have their own
page. [Set up GitHub](setup.md) leads through creating a token and lists the rights it needs.

## Configuration

A service is `https://api.github.com` (the default), `https://api.SUBDOMAIN.ghe.com` for GitHub Enterprise
Cloud with data residency, or `https://HOST/api/v3` for GitHub Enterprise Server. A service without
`base_url` uses the default; Qatlas applies it when reading the file and leaves the file unchanged, so
`qatlas config validate` accepts `provider: github` alone. Qatlas derives the GraphQL
endpoint from it (`/graphql`, or `https://HOST/api/graphql` on Enterprise Server). Filtered project items
need a server that supports the `query` argument of `ProjectV2.items`.

`qatlas config validate` checks the base URL against these same forms, so a host that is not an
`https://api.…` API host and does not end in `/api/v3`, or one that is not even `https`, fails validation
with `config-invalid` instead of only at the first call.

The credential provides `token`, a personal access token. Which kind of token to create, the rights each tool
group needs, the fields Qatlas leaves out without a scope, and how to test the connection are described in
[Set up GitHub](setup.md).

### Targets

`targets` is an optional allow-list. A connection without it reaches every repository and project its token
reaches; a connection with it reaches only the listed ones. Entries of every kind can be mixed, in any number
and order:

| Entry | Allows |
| --- | --- |
| `repos/OWNER/REPO` | one repository |
| `users/LOGIN/projects/NUMBER` | one user project |
| `orgs/LOGIN/projects/NUMBER` | one organization project |
| `repos/OWNER/*` | every repository of one owner |
| `users/LOGIN/projects/*`, `orgs/LOGIN/projects/*` | every project of one user or organization |
| `users/LOGIN`, `orgs/LOGIN` | listing every project and repository of one user or organization, and creating or copying projects there |

An owner entry allows the [owner lists](#owner-lists), `github.projects.create`, and the
destination of `github.projects.copy` only: it names what the lists may show and where a new project may be
made, not a repository or an existing project any other tool may act on. A new project has no number yet, so
a project pattern such as `orgs/LOGIN/projects/*` does not allow creating or copying one; only the owner entry
or a connection without targets does. `*` is accepted only as the whole last segment of a
repository or project entry; there are no ranges and no other patterns. Owners, repositories, and projects
are compared without case, like GitHub compares them, and an entry listed twice makes the file invalid. The
token stays an independent upper bound: the targets never grant what the token lacks, and the token never
widens the targets, so a call reaches only what both allow. The single `target: ...` form and existing lists,
such as a project followed by the repositories it plans in, stay valid and are read as the same allow-list.

`exclude` is an optional deny-list in the same spelling, taking `repos/OWNER/REPO`, `repos/OWNER/*`, `users/LOGIN`,
`orgs/LOGIN`, `users/LOGIN/projects/NUMBER`, `orgs/LOGIN/projects/NUMBER`, and `users/LOGIN/projects/*` or
`orgs/LOGIN/projects/*`; a lone `*` or a `*` anywhere else is invalid. An exclusion always wins over a target, even
a `*` pattern: an owner exclusion takes every repository, project, and gist of that owner out of reach, and
`users/X` and `orgs/X` name the same login. An exclusion that equals a target is invalid; one that covers nothing
is valid and harmless. Exclusions never withhold a tool. A call whose argument names an excluded object is an
`invalid-request` before any I/O; an object found only after a read is `not-found`, and neither refusal names the
object or the exclusion. Lists, stars, notifications, and gist lists drop excluded rows; forking or creating into,
and copying a project into, an excluded owner is refused. A linked repository inside an exclusion shows only its
identifiers, sub-issues and dependencies from exclusions count in `withheld`, and references to issues or pull
requests from exclusions are left out. The profile tools (`github.accounts.me`,
`github.users.search`, `github.organizations.search`, profile fields) stay visible, and counts that GitHub sums
(`public_repos`, `totalCount`, `total_count`) still include excluded objects. [Search](search.md#exclusions) and
[project items](project-items.md#items-and-drafts) describe their own enforcement.

Two limits hold: repository lookups ask GitHub not to follow renames, but a repository renamed outside Qatlas
leaves a name-based exclusion no longer matching it; and organization rulesets and organization custom properties
stay allowed, even for an excluded repository or owner.

One GitHub connection carries the project, issue, comment, and Actions tools together. Which of them it
offers is decided by `effects` or `tools` alone. Reads are a connection's only default: a connection that
selects by `effects` needs `create` or `update` among them for a change and `execute` for an Actions
execution, and a connection with a `tools` list offers only the tools it lists, never one added in a later
version.
`github.projects.delete`, `github.project-fields.delete`, `github.project-field-options.delete`,
`github.project-iterations.replace`, `github.project-views.delete`, `github.project-items.delete`,
`github.project-status.delete`, and `github.project-workflows.delete` need `delete`. They, the
[access tools](projects.md#project-collaborators-and-teams) that change who reaches a project, and the workflow
maintainer and Actions administrator tools are listed only: a connection without a `tools` list never offers
them, whatever its `effects`.

### The target of a call

Every repository tool (issues, comments, Actions, workflow maintenance, and Actions administration) takes
`repository` as `OWNER/REPO`. Every project tool (`github.project-items.list`, `get`, `update`, `archive`,
`unarchive`, `move`, `delete`, `github.project-drafts.create`, `update`, `github.projects.update`, `delete`,
the [field schema tools](projects.md#project-fields), and the tools of views, status updates, collaborators, teams, and
automations) takes `project` as
`users/LOGIN/projects/NUMBER` or `orgs/LOGIN/projects/NUMBER`. `github.project-items.add`,
`github.project-issues.create`, `github.project-drafts.convert`, `github.projects.link`, and
`github.projects.unlink` touch both, take both, and check both against the targets. In `github.project-items.list`, `repository` stays a filter on the items of
the project, not a target. The owner lists `github.projects.list` and `github.repositories.list` and
`github.projects.create` take `owner` as `users/LOGIN` for a user or `orgs/LOGIN` for an organization;
`github.projects.copy` takes the source as `project` and the destination as `owner`, and checks both.

An argument may be left out only when the targets allow exactly one repository, exactly one project, or
exactly one owner as an owner entry, and not as a pattern: that one is the default of its kind. Nothing else
chooses a target; in particular, the git remote of the working directory never does, for `qatlas invoke` and
a `qatlas mcp` broker alike, and neither does it choose an owner. A connection without targets, or with a
pattern, therefore needs `repository` as `OWNER/REPO` on every repository call.

A default never widens or narrows the targets, and an explicit argument always wins. Every target is checked
before a secret is read and before GitHub is contacted, and every refusal is an `invalid-request` that names
the next step without echoing the value:

| Case | Next step the message names |
| --- | --- |
| a `repository`, `project`, or `owner` outside the targets | pass one the targets allow, or add it to the targets |
| no argument, and no default settles it | pass `repository` as `OWNER/REPO`, `project` as `users/LOGIN/projects/NUMBER` or `orgs/LOGIN/projects/NUMBER`, or `owner` as `users/LOGIN` or `orgs/LOGIN` |
| the targets name no target of the tool's kind | add one to the targets, or use another connection |
| a malformed argument, or a pattern as an argument | the argument's form |

A tool that the connection's `effects` or `tools` exclude stays an unsupported capability, refused before
a secret is read as well.

Every result names the target the tool acted on in a field named like its argument, whether the argument
chose it or a default did: `repository` as `OWNER/REPO`, `project` as `users/LOGIN/projects/NUMBER` or
`orgs/LOGIN/projects/NUMBER`, both for `github.project-items.add`, `github.project-issues.create`,
`github.project-drafts.convert`, `github.projects.link`, and `github.projects.unlink`, and `owner` as `users/LOGIN` or `orgs/LOGIN` for the
owner lists, `github.projects.create`, and, beside `project`, `github.projects.copy`. The field sits beside the tool's own fields.

`github.project-items.get` keeps its own `repository` field for the repository of the item's content and names
its target as `project`.

### Errors

GitHub answers a repository, project, issue, or item it does not hold exactly like one the token may not see:
a REST 404 or a GraphQL `NOT_FOUND`, or an answer that leaves the requested project or issue out. Qatlas reports all of them as
`not-found` and names the target the request addressed, including a target a default chose, with what to
check. Inside a repository it names the issue, the workflow run, job, or workflow by its identifier, the
workflow file or `.github/workflows` directory with the ref it was read at, `github.contents.get`'s path or
the repository root with its ref, `github.trees.get`'s ref, `github.blame.get`'s ref or its path at that ref,
`github.commits.get`'s ref, `github.tags.get`'s tag, the discussion by its number, a code scanning, Dependabot, or secret scanning alert as "code scanning alert N", "Dependabot alert N", or "secret scanning alert N" and their lists as "the code scanning alerts of this repository" and so on, a code quality finding as "code quality finding N", the repository security advisories as "the repository security advisories" (of a repository or of "orgs/LOGIN"), and a global advisory as "global security advisory GHSA-..." (the list as "the global security advisories"), a gist as "this gist" (also for a gist of a user the connection's targets do not name) and a user's gists as "the gists of users/LOGIN", a notification thread as "this notification thread" (also for a thread of a repository the connection's targets do not admit), the subscription of a thread, a fork or the collaborators `github.repositories.fork` and
`github.collaborators.list` address, the pull request by its number (also for a Copilot review request), the issue a Copilot assignment addresses by its number, the sub-issues or the dependencies of an
issue as "the sub-issues of issue #N", "a sub-issue of issue #N", "the sub-issue order of issue #N", "the
issues blocking issue #N", "the issues issue #N blocks", or "a dependency of issue #N", or, for
`github.pull-request-checks.list` once the pull request itself was found, the commit its checks were asked
for, where the call named one.

| Code | GitHub's answer |
| --- | --- |
| `auth` | HTTP 401: GitHub rejected the token itself, or Qatlas cannot send it; the message adds the next step, checking or renewing the credential with `qatlas credential set <credential> <role>` or in `qatlas tui` |
| `permission` | HTTP 403, or GraphQL `FORBIDDEN` or `INSUFFICIENT_SCOPES`: GitHub saw the target and refused this token explicitly; the message names the target and, as the next step, the scopes or permissions of the token to check, see [Fixing errors](setup.md#fixing-errors) |
| `not-found` | HTTP 404 or GraphQL `NOT_FOUND`: the target or a resource inside it does not exist, or the token may not see it |
| `invalid-request` | the number an issue tool names belongs to a pull request, which issue tools do not handle |
| `rate-limited` | GitHub asked to wait |
| `provider-error` | GitHub rejected the request in another way, for example as invalid or in the current state of the resource |

A change GitHub answered with `not-found` was not applied. The tools' permission messages and what to do about
them are listed under [Fixing errors](setup.md#fixing-errors).

## Owner lists

`github.projects.list` ([projects](projects.md#owners)) and `github.repositories.list`
([repositories](repositories.md#owners-repositories)) find a project number or a repository name without knowing it
in advance. The `project` and `repository` values they report are the values the other tools take as arguments.
A connection without targets lists everything of the owner that its token can see. A connection with targets
lists the projects or repositories of an owner only when its targets name that owner, name a project or
repository pattern of it, or name one of its projects or repositories:

| Targets of the connection | `github.projects.list` of `orgs/octo-org` | `github.repositories.list` of `orgs/octo-org` |
| --- | --- | --- |
| `orgs/octo-org` | every project | every repository |
| `orgs/octo-org/projects/*` | every project | refused |
| `repos/octo-org/*` | refused | every repository |
| `orgs/octo-org/projects/7`, `repos/octo-org/example` | project 7 only | `octo-org/example` only |

A refusal is an `invalid-request` before a secret is read. `owner` may be left out only when the targets name
exactly one owner entry. `users/` and `orgs/` must match the owner's kind: GitHub resolves a login only under
its own kind, and the other answers `not-found`.

## Choosing targets

A person chooses the targets of a connection in two levels: the owner, then its repositories or projects. Owners
are the account behind the token (`users/LOGIN`) and the organizations it belongs to (`orgs/LOGIN`). Under an
owner, every repository it owns (`repos/OWNER/REPO`) and every project, open or closed, is offered. The
repository list of an owner begins with all its repositories (`repos/OWNER/*`); beside it the owner itself
(`users/LOGIN` or `orgs/LOGIN`) can be chosen, each with its usual meaning. The
connection's own targets neither filter nor narrow what is offered. Choosing only reads, through no tool and
for no agent.

Repositories can be searched by name at GitHub within the owner; owners and projects have no search and are
filtered by what has been loaded. Project titles are untrusted data and are shortened and cleaned.

A URL of the web interface is resolved to a target:

| URL | Target |
| --- | --- |
| `github.com/OWNER/REPO`, also with a page below it such as `/issues/1` or `/tree/main/x` | `repos/OWNER/REPO` |
| `github.com/orgs/LOGIN/projects/NUMBER` or `github.com/users/LOGIN/projects/NUMBER`, also with `/views/1` | the project |
| `github.com/LOGIN` | `users/LOGIN` or `orgs/LOGIN`, after one read of the account |

Pages of github.com that are no owner (`settings`, `orgs` without a project, `marketplace`, `search`, `login`
and the like) and any other host resolve to nothing. The web host follows the connection's service:
`github.com` for `https://api.github.com`, `SUBDOMAIN.ghe.com` for `https://api.SUBDOMAIN.ghe.com`, and for
GitHub Enterprise Server its own host.

## Confirmed changes

Every change requires confirmation in its own invoke request (`--confirm`, or `confirm: true` over MCP). An
unconfirmed change, and a change the connection's effects or tools exclude, ends before a secret is read and
before GitHub is contacted.

### Unclear outcomes and conflicts

Qatlas sends every change exactly once and never repeats it. When a change may have reached GitHub without a
confirmed answer (a timeout, a dropped connection, a server error, or an unreadable answer), the error or the
field message says that the change may have been applied: read the current state before repeating it, and
never repeat a create blindly. After each change, the next request of the same token waits one second, as
GitHub asks of integrations that write.

GitHub offers no precondition for issue or project changes: an ETag only saves a repeated read, and the last
write wins. Qatlas therefore addresses items by their current identifier, verifies that the item belongs to
the chosen project before it changes it, and never retries.

## Cursor contract

Lists take `limit` (1 to 100, default 30) and an opaque `cursor`, and answer `has_more` plus `next_cursor`
when another batch may follow. A full batch never means the end: read on while `has_more` is true. A cursor
is bound to the repository or project of the call and to the filters that produced it, an owner list cursor
to its owner and its list, and a comment cursor to its issue; a cursor from other filters, another issue, or
another repository, project, or owner is an invalid request. A checksum covers the whole cursor, so one that
was altered, cut short, or not issued by Qatlas is an invalid request as well; every such refusal happens
before GitHub is asked and names the next step: `cursor is not a next_cursor of this list; start the list
again without cursor`. The owner lists show only what the targets allow, so a batch may hold fewer entries
than `limit` while `has_more` stays true. Batches follow the project order, so reading every batch reaches
each matching item once. A batch may be short, even empty, when Qatlas stopped scanning after a bounded
number of requests; `has_more` then stays true.

The Actions lists follow the same contract, but GitHub pages them by number: a continuation keeps the batch
size of its first batch, whatever `limit` it names. They are ordered newest first, so a run started while a
caller pages can move an older run onto the next batch, where it appears again; no run is skipped by that.
GitHub answers at most 1000 runs of a run list filtered by `status`, `branch`, `event`, `actor`, or time;
`has_more` turns false there, and a narrower filter reaches older runs.

`github.pull-requests.list`, `github.pull-request-files.list`, and `github.pull-request-commits.list` page by
number as well, but GitHub answers these plain array routes without a total count, so `has_more` follows
whether GitHub's `Link` response header names a following page instead. `github.pull-request-checks.list`
answers no cursor at all: it reads up to 100 check runs and 100 legacy statuses at the head commit in one
call and reports `truncated` when GitHub held more of either.

## Result objects

A flat field of a tool result stays as it is. A tool that adds the object behind it names the object
`<field>_detail`, or `<field>_details` for a list. In an object, `url` is the web address; the REST `url` and
every other `*_url` sit in its `links`, and each nested object has its own `links`.

A connection GitHub also serves as a list of its own is read with that list; this tool reports its count. A
tool that does read the first page of a connection takes a fixed number of entries and reports
`<field>_total_count` and `<field>_truncated`.

`temp_clone_token` grants clone access without sign-in and is withheld. A repository outside the connection's
targets is named only by its identifiers.
