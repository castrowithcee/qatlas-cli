---
description: >
  Describes GitHub project and issue planning, GitHub Actions, and pull request reads and changes: targets as an optional allow-list, target arguments and their defaults, the owner lists of projects and repositories, the project lifecycle and templates, the project field schema, project views, status updates, collaborators and teams, and built-in automations, reads, confirmed changes of issues, comments, project fields, project items, and drafts, batches, partial results, the Actions observer and operator tools, log limits, the listed-only workflow maintainer and Actions administrator tools, pull request reads, confirmed changes, the listed-only merge and its conflicts, the cursor contract, and token scopes.
type: knowledge
edit: shared
created: 2026-09-23
updated: 2026-09-26
---

# GitHub

GitHub is a controlled planning provider, not a replacement for `gh`. It lists the projects and repositories
of a user or an organization, creates, changes, copies, and deletes projects, links them to repositories, and
marks them as templates, reads and maintains the fields of a project with their options and iterations,
its views, and its status updates, changes its collaborators and teams, reads and deletes its built-in
automations, reads and maintains issues,
comments, project items, and draft issues, and it observes and, when allowed, operates the
GitHub Actions of a repository.
On a connection that names them explicitly, it also maintains workflow files and Actions settings. On the
not-recommended profile `pull-requests` it reads the pull requests of a repository: their list, one pull
request, its changed files, its commits, its diff, and the checks at its head commit. The not-recommended
profile `pull-requests-operator` adds their changes: it creates, updates, closes, and reopens a pull request,
and updates its head branch with its base while the head is still a given commit. Only the merge is not in
any profile: a connection offers it only while its `tools` list names it, because a merge into the wrong
repository's default branch is the costliest mistake there. Its project items still see a pull request only
as an item, and its issue and comment tools still refuse a pull request number. It never accepts a free
filter expression, a GraphQL document, or a REST route from the caller. A `repository`, `project`, or `owner`
argument names exactly one target, and it must lie inside the connection's targets when the connection lists
any.

## Configuration

A service is `https://api.github.com` (the default), `https://api.SUBDOMAIN.ghe.com` for GitHub Enterprise
Cloud with data residency, or `https://HOST/api/v3` for GitHub Enterprise Server. A service without
`base_url` uses the default; Qatlas applies it when reading the file and leaves the file unchanged, so
`qatlas config validate` accepts `provider: github` alone. Qatlas derives the GraphQL
endpoint from it (`/graphql`, or `https://HOST/api/graphql` on Enterprise Server). Filtered project items
need a server that supports the `query` argument of `ProjectV2.items`.

The credential provides `token`, a personal access token. A read-only setup uses a classic token with
`read:project` plus `repo` (or `public_repo` for public repositories only), or a fine-grained token with read
access to issues and to projects. Changes need `project` instead of `read:project`, or write access to issues
and projects for a fine-grained token. User-owned projects need a classic token. The Actions tools have
their own requirements, listed under [GitHub Actions](#github-actions), and so have the tools of
[workflow maintenance and Actions administration](#workflow-maintenance-and-actions-administration) and of
[pull requests](#tokens-for-pull-requests). A successful `qatlas connection test` shows only that the token
can read the first project or repository the
connection's targets name exactly, or, without such a target, its own user; GitHub checks every resource and
scope again on each call, so a passing test does not authorize every tool.

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

An owner entry allows the [owner lists](#owners-projects-and-repositories), `github.projects.create`, and the
destination of `github.projects.copy` only: it names what the lists may show and where a new project may be
made, not a repository or an existing project any other tool may act on. A new project has no number yet, so
a project pattern such as `orgs/LOGIN/projects/*` does not allow creating or copying one; only the owner entry
or a connection without targets does. `*` is accepted only as the whole last segment of a
repository or project entry; there are no ranges and no other patterns. Owners, repositories, and projects
are compared without case, like GitHub compares them, and an entry listed twice makes the file invalid. The
token stays an independent upper bound: the targets never grant what the token lacks, and the token never
widens the targets, so a call reaches only what both allow. The single `target: ...` form and existing lists,
such as a project followed by the repositories it plans in, stay valid and are read as the same allow-list.

One GitHub connection carries the project, issue, comment, and Actions tools together. Which of them it
offers is decided by `permissions` and `tools` alone. Reads are a connection's only default: every change
needs `create` or `update` in the connection's `permissions`, every Actions execution needs `execute`, and a
connection with a `tools` list offers only the tools it lists, never one added in a later version.
`github.projects.delete`, `github.projectfields.delete`, `github.projectfieldoptions.delete`,
`github.projectiterations.replace`, `github.projectviews.delete`, `github.projectitems.delete`,
`github.projectstatus.delete`, and `github.projectworkflows.delete` need `delete`. They, the
[access tools](#project-collaborators-and-teams) that change who reaches a project, and the workflow
maintainer and Actions administrator tools are listed only: a connection without a `tools` list never offers
them, whatever its permissions.

### The target of a call

Every repository tool (issues, comments, Actions, workflow maintenance, and Actions administration) takes
`repository` as `OWNER/REPO`. Every project tool (`github.projectitems.list`, `get`, `update`, `archive`,
`unarchive`, `move`, `delete`, `github.projectdrafts.create`, `update`, `github.projects.update`, `delete`,
the [field schema tools](#project-fields), and the tools of views, status updates, collaborators, teams, and
automations) takes `project` as
`users/LOGIN/projects/NUMBER` or `orgs/LOGIN/projects/NUMBER`. `github.projectitems.add`,
`github.projectissues.create`, `github.projectdrafts.convert`, `github.projects.link`, and
`github.projects.unlink` touch both, take both, and check both against the targets. In `github.projectitems.list`, `repository` stays a filter on the items of
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

A tool that the connection's `permissions` or `tools` exclude stays an unsupported capability, refused before
a secret is read as well.

Every result names the target the tool acted on in a field named like its argument, whether the argument
chose it or a default did: `repository` as `OWNER/REPO`, `project` as `users/LOGIN/projects/NUMBER` or
`orgs/LOGIN/projects/NUMBER`, both for `github.projectitems.add`, `github.projectissues.create`,
`github.projectdrafts.convert`, `github.projects.link`, and `github.projects.unlink`, and `owner` as `users/LOGIN` or `orgs/LOGIN` for the
owner lists, `github.projects.create`, and, beside `project`, `github.projects.copy`. The field sits beside the tool's own fields,
so `github.issues.get` without an argument answers, for example:

```json
{"assignees": ["hubot"], "body": "...", "labels": ["bug"], "number": 42, "repository": "octo-org/example",
 "state": "open", "title": "Crash on start"}
```

`github.projectitems.get` keeps its own `repository` field for the repository of the item's content and names
its target as `project`.

### Errors

GitHub answers a repository, project, issue, or item it does not hold exactly like one the token may not see:
a REST 404 or a GraphQL `NOT_FOUND`, or an answer that leaves the requested project or issue out. Qatlas reports all of them as
`not-found` and names the target the request addressed, including a target a default chose, with what to
check. Inside a repository it names the issue, the workflow run, job, or workflow by its identifier, the
workflow file or `.github/workflows` directory with the ref it was read at, the pull request by its number,
or, for `github.pullrequestchecks.list` once the pull request itself was found, the commit its checks were
asked for, where the call named one:

```text
qatlas: not-found: list issues: GitHub does not hold repository octo-org/example or does not show it to this token; check the name, and that the token can see it (classic: scope repo for a private repository; fine-grained: access to this repository)
qatlas: not-found: list project items: GitHub does not hold project users/octocat/projects/3 or does not show it to this token; check the name, and that the token can see it (classic: scope read:project; fine-grained: Projects access of its organization, as a user-owned project needs a classic token)
qatlas: not-found: get issue: GitHub does not hold issue #5 in repository octo-org/example or does not show it to this token; check the arguments, and that the token can see the repository (...)
qatlas: not-found: dispatch workflow: GitHub does not hold workflow file .github/workflows/release.yml at ref other in repository octo-org/example or does not show it to this token; check the arguments, and that the token can see the repository (...)
qatlas: not-found: list repositories: GitHub does not hold owner orgs/octocat or does not show it to this token; check the login, and users/ for a user or orgs/ for an organization
qatlas: not-found: get pull request: GitHub does not hold pull request #99 in repository octo-org/example or does not show it to this token; check the arguments, and that the token can see the repository (...)
qatlas: permission: list projects: this GitHub token may not read the projects of owner users/octocat; check its scopes or permissions; classic: scope read:project; fine-grained: Projects: read of the organization, as the projects of a user need a classic token
qatlas: auth: list issues: GitHub rejected the token; check or renew the credential of this connection with 'qatlas credential set <credential> <role>' or in 'qatlas tui'
```

| Code | GitHub's answer |
| --- | --- |
| `auth` | HTTP 401: GitHub rejected the token itself, or Qatlas cannot send it; the message adds the next step, checking or renewing the credential with `qatlas credential set <credential> <role>` or in `qatlas tui` |
| `permission` | HTTP 403, or GraphQL `FORBIDDEN` or `INSUFFICIENT_SCOPES`: GitHub saw the target and refused this token explicitly, for example a classic token without `read:project`, or a fine-grained token on a user-owned project; the message names the target and, as the next step, the scopes or permissions of the token to check |
| `not-found` | HTTP 404 or GraphQL `NOT_FOUND`: the target or a resource inside it does not exist, or the token may not see it |
| `invalid-request` | the number an issue tool names belongs to a pull request, which issue tools do not handle |
| `rate-limited` | GitHub asked to wait |
| `provider-error` | GitHub rejected the request in another way, for example as invalid or in the current state of the resource |

A change GitHub answered with `not-found` was not applied. The Actions and workflow maintenance tools keep
their own permission messages, listed under [Tokens for Actions](#tokens-for-actions) and
[Credential](#credential).

### Setup profiles

The terminal editor starts a new connection on the setup profile `read`, which ticks `[read]` and the reads
`github.projects.list`, `github.repositories.list`, `github.projectitems.list`, `github.projectitems.get`,
`github.issues.list`, `github.issues.get`, and `github.comments.list`. The profile `planning` ticks
`[read, create, update]` with the owner lists, the project reads, `github.projectitems.update`,
`github.projectitems.add`, `github.projectitems.move`, `github.projectdrafts.create`,
`github.projectdrafts.update`, `github.projectdrafts.convert`, and `github.projectissues.create`; archiving
and restoring stay unticked. The profile
`projects` ticks `[read, create, update]` with the owner lists, the
[project lifecycle](#project-lifecycle) tools except `github.projects.delete`, the template tools,
`github.projectfields.list`, `create`, and `update` of the [project fields](#project-fields),
`github.projectviews.list`, `create`, and `update` of the [project views](#project-views),
`github.projectstatus.list`, `create`, and `update` of the [status updates](#project-status-updates),
`github.projectteams.list` of the [teams](#project-collaborators-and-teams), and
`github.projectworkflows.list` of the [automations](#project-automations); no profile ticks a tool with the
effect `delete` or one that changes access. The
profiles `actions-observer` and `actions-operator` are described under [GitHub Actions](#github-actions), and
the not-recommended profiles `pull-requests` and `pull-requests-operator` under
[Pull requests](#pull-requests). A profile is a
visible starting selection, not a role: only the ticked `permissions` and `tools` are saved, every tick can be
changed before saving, and a saved connection never follows a profile.

```yaml
connections:
  roadmap:
    service: github
    credential: github-planner
    targets: [orgs/octo-org/projects/7, repos/octo-org/*]
    permissions: [read, create, update]
    tools: [github.projectitems.list, github.projectitems.get, github.projectitems.update,
      github.projectissues.create, github.issues.list, github.issues.get]
```

`roadmap` defaults `project` to its one project. `repository` comes from the call, and only repositories of
`octo-org` are accepted.

## Owners: projects and repositories

The owner lists find a project number or a repository name without knowing it in advance:

```sh
qatlas invoke github.projects.list --connection planning --arg owner=orgs/octo-org
qatlas invoke github.repositories.list --connection planning --arg owner=users/octocat
```

| Tool | Lists | Each entry |
| --- | --- | --- |
| `github.projects.list` | the projects of the owner, in number order, open and closed | `project` (`users/LOGIN/projects/NUMBER` or `orgs/LOGIN/projects/NUMBER`), `number`, `title`, `url`, `closed` |
| `github.repositories.list` | the repositories the owner owns, in name order, archived ones included | `repository` (`OWNER/REPO`), `visibility` (`public`, `private`, `internal`), `archived` |

`project` and `repository` are the values the other tools take as arguments. Titles are untrusted data. A
connection without targets lists everything of the owner that its token can see. A connection with targets
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
its own kind, and the other answers `not-found`. A user's repositories are the ones the user owns, not those
the user collaborates on.

Reading the projects needs `read:project` on a classic token. A fine-grained token lists the projects of an
organization with Projects read access, but not the projects of a user; GitHub refuses that with
`permission`. The repositories need no scope for public ones, `repo` on a classic token for private ones, or
access to the repositories on a fine-grained token.

## Project lifecycle

The lifecycle tools make and maintain projects themselves, not their items. Each one is a change that needs
confirmation in its own invoke request, is sent exactly once, and follows the rules under
[unclear outcomes](#unclear-outcomes-and-conflicts).

| Tool | Effect | Idempotency | Does |
| --- | --- | --- | --- |
| `github.projects.create` | create | non-idempotent | creates one empty project with `title` in `owner` |
| `github.projects.update` | update | idempotent | changes `title`, `short_description`, `readme`, `public`, or `closed`; left-out settings stay |
| `github.projects.copy` | create | non-idempotent | copies `project` with its fields and views into a new project `title` of `owner`; `include_drafts: true` copies its draft issues as well |
| `github.projects.link` | update | idempotent | links `project` to `repository`; a linked project stays linked once |
| `github.projects.unlink` | update | idempotent | removes the link between `project` and `repository` |
| `github.projecttemplates.mark` | update | idempotent | marks `project` of an organization as a template |
| `github.projecttemplates.unmark` | update | idempotent | stops offering `project` as a template |
| `github.projects.delete` | delete | unknown | deletes `project` with its items, fields, and views; listed only |

`closed: true` closes a project and `closed: false` reopens it; `public` switches its visibility, which an
organization policy may forbid. At least one setting is required. `short_description` and `readme` need at
least one character: GitHub ignores an empty value and answers with success, so it cannot clear either
through its API, and Qatlas refuses `""` before a secret is read. A create and a copy answer the new
project in `created` as `project`, `number`, `title`, `url`, `closed`, and `public`; an update answers the
project's state after the change. A repeated link or unlink succeeds again and leaves the project linked
once or unlinked. A repeated delete ends as `not-found` when the project is resolved, before any mutation;
its idempotency stays `unknown` because a delete addresses the project by number: read the current state
before repeating one.

A template is a project its organization offers when someone creates a project; `github.projects.copy` copies
any allowed project, template or not. The template tools answer the project's state with `template`. GitHub
marks only projects of an organization as templates: it refuses a user's project as unprocessable, so
Qatlas refuses `github.projecttemplates.mark` for a `users/LOGIN/projects/NUMBER` project with
`invalid-request` before a secret is read. Unmarking a project that is no template, a user's project
included, succeeds and changes nothing.

```sh
echo '{"owner":"orgs/octo-org","title":"Roadmap 2027"}' | qatlas invoke github.projects.create --connection projects --confirm
echo '{"project":"orgs/octo-org/projects/7","closed":true}' | qatlas invoke github.projects.update --connection projects --confirm
echo '{"project":"orgs/octo-org/projects/7","repository":"octo-org/example"}' |
  qatlas invoke github.projects.link --connection projects --confirm
```

`github.projects.create` and the destination of `github.projects.copy` need the owner as an owner entry of
the targets, or a connection without targets; `owner` may be left out when the targets name exactly one owner
entry. Every other lifecycle tool, and the source of a copy, needs the project inside the targets like any
project tool, and a link or an unlink needs the repository inside them as well. `github.projects.delete`
needs `delete` in the connection's `permissions` and its name in the connection's `tools`:

```yaml
connections:
  projects:
    service: github
    credential: github-planner
    targets: [orgs/octo-org, orgs/octo-org/projects/*, repos/octo-org/*]
    permissions: [read, create, update, delete]
    tools: [github.projects.list, github.repositories.list, github.projects.create, github.projects.update,
      github.projects.copy, github.projects.link, github.projects.unlink, github.projects.delete]
```

The lifecycle tools need `project` on a classic token, or Projects read and write access of the organization
on a fine-grained token; the projects of a user need a classic token. A link or an unlink also needs a token
that can see the repository. Deleting a project needs the rights of a project administrator. A copy takes
the views of its source along. No lifecycle tool deletes a field, an option, a view, or an item; the
[fields](#project-fields) and the [views](#project-views) have tools of their own.

## Project fields

The field schema tools read the fields of a project and create, change, and delete them. Fields, options, and
iterations are named, never identified: names are compared without case, and every name is resolved against
the project's fields in one query before the one change is sent. Each change needs confirmation in its own
invoke request, is sent exactly once, and follows the rules under
[unclear outcomes](#unclear-outcomes-and-conflicts).

| Tool | Effect | Idempotency | Does |
| --- | --- | --- | --- |
| `github.projectfields.list` | read | safe | lists every field with its type, options, and iterations |
| `github.projectfields.create` | create | non-idempotent | creates one text, number, date, single-select, multi-select, or iteration field |
| `github.projectfields.update` | update | idempotent | renames a field, or adds, renames, recolors, describes, or reorders its options; every value stays |
| `github.projectfieldoptions.delete` | delete | unknown | removes named options; every item loses a removed option; listed only |
| `github.projectiterations.replace` | delete | non-idempotent | changes the iteration settings and adds, changes, or removes iterations; every item loses its value of the field; listed only |
| `github.projectfields.delete` | delete | unknown | deletes a field that is not built in, with its values; listed only |

`github.projectfields.list` answers `fields` in project order. Each field has `name`, `type` (`text`,
`number`, `date`, `single_select`, `multi_select`, `iteration`, or the type of a built-in field such as
`title`, `assignees`, `labels`, or `linked_pull_requests`), and `built_in`. A select field lists its
`options` with `name`, `color`, and `description`; an iteration field has `iteration` with `duration` in
days, `start_day` (such as `monday`), and `iterations` in start order, each with `title`, `start_date`,
`duration`, and `state` (`completed`, `current`, or `planned`). Built-in fields cannot be created or deleted.
The `Status` field every project has is built in as well: GitHub neither deletes nor renames it, but its
options are maintained like those of any single-select field. Names and descriptions are untrusted data.

```sh
qatlas invoke github.projectfields.list --connection projects --arg project=orgs/octo-org/projects/7
echo '{"project":"orgs/octo-org/projects/7","name":"Priority","type":"single_select",
  "options":[{"name":"P1","color":"red"},{"name":"P2","color":"yellow"},{"name":"P3"}]}' |
  qatlas invoke github.projectfields.create --connection projects --confirm
echo '{"project":"orgs/octo-org/projects/7","name":"Sprint","type":"iteration","iteration":{"start_date":"2026-10-05",
  "duration":14,"iterations":[{"title":"Sprint 1","start_date":"2026-10-05"},{"title":"Sprint 2","start_date":"2026-10-19"}]}}' |
  qatlas invoke github.projectfields.create --connection projects --confirm
echo '{"project":"orgs/octo-org/projects/7","field":"Status","options":[{"name":"Review","color":"purple"}],
  "order":["Todo","In progress","Review","Done"]}' | qatlas invoke github.projectfields.update --connection projects --confirm
```

`github.projectfields.create` takes `name`, unique in the project, and `type`. A select field needs
`options`, at most 50, each with `name` and optionally `color` (`gray`, `blue`, `green`, `yellow`, `orange`,
`red`, `pink`, or `purple`; `gray` when omitted) and `description` (empty when omitted). An iteration field
needs `iteration` with `start_date` (`YYYY-MM-DD`) and `duration` (1 to 365 days), and takes `iterations`,
each with `title`, `start_date`, and optionally `duration`, which defaults to the field's; without
`iterations` the field starts empty. A name the project already holds is refused before the change; a
repeated create is refused that way, so it never makes a second field of one name.

`github.projectfields.update` addresses the field by `field` and changes `name`, `options`, or `order`.
An entry of `options` names an option: `new_name`, `color`, and `description` change an existing one, and a
name the field lacks adds an option at the end. `order` names every option after these changes exactly once,
in the new order. GitHub replaces the options of a field as a whole, so Qatlas sends every option the field
keeps together with its identifier: renamed, recolored, and reordered options keep the values items hold. An
option left out of `options` stays as it is.

The value-losing changes are separate tools with the effect `delete`, offered only by a connection whose
`permissions` include `delete` and whose `tools` list names them:

- `github.projectfieldoptions.delete` removes the named `options` of a select field. Every item that holds a
  removed option loses it; at least one option stays, and deleting the field removes the last one.
- `github.projectiterations.replace` changes the iterations of an iteration field. `start_date` and
  `duration` change the settings; an entry of `iterations` names an iteration by `title` and changes its
  `new_title`, `start_date`, or `duration`, or adds it when the field lacks the title, which then needs
  `start_date`; `remove` names iterations to remove. Iterations left out stay, completed ones included.
  GitHub takes iterations without identifiers and recreates every iteration of the field with such a change,
  so every item loses its value of this field, whichever iterations the change names; set the values again
  afterwards. Renaming an iteration field through `github.projectfields.update` keeps its values.
- `github.projectfields.delete` deletes a field that is not built in, with its value on every item.

A field, option, or iteration the project lacks, an option or iteration named twice, and an `order` that
leaves an option out are refused before any change and name what the field holds. A repeated removal or
delete is refused that way once the name is gone; its idempotency stays `unknown`, because a name may be
taken again in between.

```yaml
connections:
  project-admin:
    service: github
    credential: github-planner
    targets: [orgs/octo-org/projects/7]
    permissions: [read, create, update, delete]
    tools: [github.projectfields.list, github.projectfields.create, github.projectfields.update,
      github.projectfieldoptions.delete, github.projectiterations.replace, github.projectfields.delete]
```

The field tools need `project` on a classic token, or Projects read and write access of the organization on a
fine-grained token; the projects of a user need a classic token. The issue fields an organization defines for
its issues (`createProjectV2IssueField`) are not supported.

## Project views

The view tools read the views of a project and create, change, and delete them. A view is addressed by
`view`, its number in the project, and the fields it shows by their names, compared without case and resolved
against the project's fields and views in one query before the one change is sent. Each change needs
confirmation in its own invoke request, is sent exactly once, and follows the rules under
[unclear outcomes](#unclear-outcomes-and-conflicts).

| Tool | Effect | Idempotency | Does |
| --- | --- | --- | --- |
| `github.projectviews.list` | read | safe | lists every view with its layout, filter, visible fields, grouping, board columns, and sorting |
| `github.projectviews.create` | create | non-idempotent | creates one `table`, `board`, or `roadmap` view with `name`, `fields`, and `filter` |
| `github.projectviews.update` | update | idempotent | changes `name`, `layout`, `filter`, or `fields`; left-out settings stay |
| `github.projectviews.delete` | delete | unknown | deletes one view; the items and fields stay; listed only |

`github.projectviews.list` answers `views` in project order, at most 100. Each view has `number`, `name`,
`layout` (`table`, `board`, or `roadmap`), `filter` (empty without one), `fields` (the visible fields in
the view's order, the title first), `group_by` and `column_by` (field names), and `sort_by` (entries with
`field` and `direction`, `asc` or `desc`). Names and filters are untrusted data.

```sh
qatlas invoke github.projectviews.list --connection projects --arg project=orgs/octo-org/projects/7
echo '{"project":"orgs/octo-org/projects/7","name":"Bugs","layout":"board","fields":["Assignees","Priority"],
  "filter":"label:bug -status:Done"}' | qatlas invoke github.projectviews.create --connection projects --confirm
echo '{"project":"orgs/octo-org/projects/7","view":2,"name":"Open work","fields":["Status","Sprint"]}' |
  qatlas invoke github.projectviews.update --connection projects --confirm
```

`fields` names every field the view shows, in the view's order. GitHub shows the title first in every
view, so `[]` leaves only the title; without `fields`, a new view shows GitHub's default selection. A roadmap
takes no visible fields: GitHub refuses them, so Qatlas refuses `fields` for a roadmap, new or existing,
before any change. A new board without further settings takes its columns from `Status`; a board changed to
another layout no longer reports `column_by`.

`filter` is written in GitHub's project filter syntax, such as `status:Todo` or `label:bug -status:Done`. It is
the only filter expression Qatlas takes from a caller, and it reaches GitHub as one value, never as part of a
query. GitHub judges the syntax and keeps a filter it cannot read as written. A filter holds at most 512
characters, GitHub's own limit, and no control characters; `""` removes it.

GitHub creates a view without a filter, so `github.projectviews.create` with a non-empty `filter` sends two
changes: the create, and then the filter of the new view. Once the view exists, the answer reports it in
`view` together with `complete`. When the filter could not be set, `complete` is false and `error` says why,
including when the filter may have been set without a confirmation; set it with `github.projectviews.update`
then, instead of creating the view again. A repeated create makes a second view.

A view number the project lacks is refused before any change and names the project's view numbers, and an
unknown field names the project's fields. `github.projectviews.delete` needs `delete` in the connection's
`permissions` and its name in the connection's `tools`. GitHub keeps the last view of a project, so Qatlas
refuses to delete it. A repeated delete is refused once the number is gone; its idempotency stays `unknown`,
because a delete addresses the view by number.

GitHub's API does not set the grouping, the sorting, the board column field, field widths, the slice, or
field sums of a view. The list reads the grouping, the sorting, and the board columns as GitHub reports them;
widths, slices, and sums are neither read nor set. Change them in GitHub itself.

The view tools need `project` on a classic token, or Projects read and write access of the organization on a
fine-grained token; the projects of a user need a classic token.

## Project status updates

The status update tools read the status updates of a project and post, change, and delete them. A status
update is addressed by `status_update_id`, the `id` the list names; it is resolved together with the project in
one query and must belong to it before the one change is sent. Each change needs confirmation in its own
invoke request, is sent exactly once, and follows the rules under
[unclear outcomes](#unclear-outcomes-and-conflicts).

| Tool | Effect | Idempotency | Does |
| --- | --- | --- | --- |
| `github.projectstatus.list` | read | safe | lists the status updates, newest first, in batches |
| `github.projectstatus.create` | create | non-idempotent | posts one status update with `status`, `start_date`, `target_date`, and `body` |
| `github.projectstatus.update` | update | idempotent | changes `status`, `start_date`, `target_date`, or `body`; left-out settings stay |
| `github.projectstatus.delete` | delete | idempotent | deletes one status update; listed only |

`github.projectstatus.list` follows the [cursor contract](#cursor-contract) with a cursor bound to the project.
Each status update has `id`, `status` (`inactive`, `on_track`, `at_risk`, `off_track`, `complete`, or empty
without one), `start_date` and `target_date` (`YYYY-MM-DD` or empty), `body`, `author`, `created_at`, and
`updated_at`. Bodies are untrusted data.

```sh
echo '{"project":"orgs/octo-org/projects/7","status":"at_risk","target_date":"2026-12-18",
  "body":"The migration slips by one sprint."}' | qatlas invoke github.projectstatus.create --connection projects --confirm
echo '{"project":"orgs/octo-org/projects/7","status_update_id":"PVTSU_...","status":"on_track","target_date":""}' |
  qatlas invoke github.projectstatus.update --connection projects --confirm
```

A create needs at least one setting; GitHub posts a status update without a status or dates as well. In an
update, `""` removes a date and empties `body`. GitHub does not check that the target date follows the start
date. A status update of another project, or one GitHub does not show, is `not-found` before any change; a
repeated delete ends that way. The tools need `project` on a classic token, or Projects read and write access
of the organization on a fine-grained token; the projects of a user need a classic token.

## Project collaborators and teams

The access tools read the teams a project is linked to and change who reaches it. The changes are listed
only: a connection offers them only while its `tools` list names them, and no setup profile ticks them. Each
change needs confirmation in its own invoke request, is sent exactly once, and follows the rules under
[unclear outcomes](#unclear-outcomes-and-conflicts).

| Tool | Effect | Idempotency | Does |
| --- | --- | --- | --- |
| `github.projectteams.list` | read | safe | lists the teams the project is linked to, in name order, in batches |
| `github.projectcollaborators.update` | update | idempotent | grants users or teams a role, changes it, or removes their direct access; listed only |
| `github.projects.linkteam` | update | idempotent | links a project of an organization to one of its teams, which GitHub grants read access; listed only |
| `github.projects.unlinkteam` | update | idempotent | removes that link and the read access it granted; listed only |

`github.projectteams.list` follows the [cursor contract](#cursor-contract) with a cursor bound to the project.
Each entry has `team`, the slug `github.projects.linkteam` takes, and `name`, which is untrusted data. A
project of a user is linked to no team and lists none. GitHub shows teams only to a token that may read the
organizations, even for a project of a user: without `read:org` on a classic token, or Members read access on a
fine-grained one, the list ends with `permission`, and the message names that scope.

`collaborators` names at most 20 entries, each with either `user` (a login) or `team` (the slug of a team of the
organization that owns the project) and `role`: `none` removes the direct access, `reader` views, `writer`
edits, and `admin` also manages the project's settings. Collaborators left out stay unchanged. The answer
repeats the roles GitHub accepted. Every user and team is resolved with the project in one query first: an
unknown login or a team the organization lacks is `not-found` before any change. A team is looked up inside the
project's organization only, so a team of another organization can never be named.

```sh
echo '{"project":"orgs/octo-org/projects/7","collaborators":[{"team":"design","role":"writer"},
  {"user":"octocat","role":"none"}]}' | qatlas invoke github.projectcollaborators.update --connection project-access --confirm
echo '{"project":"orgs/octo-org/projects/7","team":"design"}' |
  qatlas invoke github.projects.linkteam --connection project-access --confirm
```

Teams belong to an organization, so Qatlas refuses a team for a `users/LOGIN/projects/NUMBER` project, in
`github.projectcollaborators.update` and in the team links, with `invalid-request` before a secret is read.
GitHub's API reads neither the collaborators of a project nor their roles, so there is no tool that lists them;
check them in GitHub itself. Changing collaborators needs the rights of a project administrator and `project`
on a classic token, or Projects read and write access of the organization on a fine-grained token; resolving a
team may also need `read:org` on a classic token, or Members read access on a fine-grained one. Inviting people
by email, managing the teams themselves, and the settings of an organization are out of scope.

## Project automations

The automation tools read the built-in workflows of a project, such as `Item closed` or `Auto-archive items`,
and delete one. A workflow is addressed by `workflow`, its number in the project, and resolved with the project
in one query before the change is sent.

| Tool | Effect | Idempotency | Does |
| --- | --- | --- | --- |
| `github.projectworkflows.list` | read | safe | lists every workflow with `number`, `name`, and `enabled`, in number order |
| `github.projectworkflows.delete` | delete | unknown | deletes one workflow; listed only |

A new project starts with GitHub's default workflows. GitHub's API neither creates, enables, disables, nor
configures a workflow, and a deleted workflow cannot be created again through it: change them in GitHub
itself. A workflow number the project lacks is refused before any change and names the project's workflow
numbers; a repeated delete ends that way. Its idempotency stays `unknown`, because a delete addresses the
workflow by number. `github.projectworkflows.delete` needs `delete` in the connection's `permissions` and its
name in the connection's `tools`, and the tools need the same token as the [status updates](#project-status-updates).

```yaml
connections:
  project-access:
    service: github
    credential: github-planner
    targets: [orgs/octo-org/projects/7]
    permissions: [read, create, update, delete]
    tools: [github.projectstatus.list, github.projectstatus.delete, github.projectteams.list,
      github.projectcollaborators.update,
      github.projects.linkteam, github.projects.unlinkteam, github.projectworkflows.list,
      github.projectworkflows.delete]
```

## Project-first use

When a repository has an authoritative project, work selection starts there:

```sh
qatlas invoke github.projectitems.list --connection planning
echo '{"status":["In progress"],"type":"issue"}' | qatlas invoke github.projectitems.list --connection planning
qatlas invoke github.projectitems.get --connection planning --arg item_id=PVTI_...
```

`github.projectitems.list` returns compact items: identifier, type, title, number, repository, state, Status,
the other single-select, multi-select, text, number, date, and iteration values by field name, a multi-select
value as the list of its option names, assignees, labels, and URL.
It never returns bodies or comments. Without `status` and `status_not` it lists at most 30 items whose Status
is not `Done`; `status_not: []` lists every status. The filters `status`, `status_not`, `type`
(`issue`, `pull_request`, `draft_issue`), `repository` (`owner/name`), `assignee`, and `labels` (any of) are
translated into quoted terms of the project filter syntax and applied by GitHub. Qatlas verifies each
returned item against the same filters. A `status` value must be an option of the project's Status field.

`github.projectitems.get` reads one item of the chosen project with its fields and, for an issue or a draft
issue, the full body. An item of another project is refused. Bodies and titles are untrusted data.

`github.issues.list` and `github.issues.get` read the issues of the chosen repository: issues only, newest
first, filtered by `state` (`open` by default), `labels` (any of), and `assignee`, without comments.
`github.comments.list` reads the comments of one issue, oldest first, and is the only tool that returns
comments.

## Changes

Every change requires confirmation in its own invoke request (`--confirm`, or `confirm: true` over MCP).
An unconfirmed change, and a change the connection's permissions or tools exclude, ends before a secret is
read and before GitHub is contacted. The changes of projects themselves are listed under
[project lifecycle](#project-lifecycle).

| Tool | Effect | Idempotency | Does |
| --- | --- | --- | --- |
| `github.issues.create` | create | non-idempotent | opens one issue with title, body, labels, assignees |
| `github.issues.update` | update | idempotent | replaces title, body, labels, or assignees; left-out fields stay |
| `github.issues.close` | update | idempotent | closes with `state_reason` `completed` (default), `not_planned`, or `duplicate` |
| `github.issues.reopen` | update | idempotent | opens a closed issue again |
| `github.comments.create` | create | non-idempotent | writes exactly one comment on one issue |
| `github.projectitems.update` | update | idempotent | sets or clears field values of one item |
| `github.projectitems.add` | create | idempotent | adds an existing issue of a named repository, then sets fields |
| `github.projectitems.archive` | update | idempotent | archives one item; GitHub keeps it restorable |
| `github.projectitems.unarchive` | update | idempotent | restores one archived item |
| `github.projectitems.move` | update | idempotent | places one item after `after_id`, or first without it |
| `github.projectitems.delete` | delete | idempotent | removes one item from the project; an issue or pull request stays, a draft is deleted; listed only |
| `github.projectdrafts.create` | create | non-idempotent | adds one draft issue, then sets fields |
| `github.projectdrafts.update` | update | idempotent | replaces the title, body, or assignees of a draft; left-out fields stay |
| `github.projectdrafts.convert` | create | idempotent | turns a draft into an issue of a named repository; the item keeps its identifier, place, and fields |
| `github.projectissues.create` | create | non-idempotent | opens an issue in a named repository, adds it, then sets fields |

Issues and comments are written through REST. `labels` and `assignees` replace the whole set; `[]` removes
every entry. GitHub creates a label the repository does not have yet, so a misspelled name adds a new label;
Qatlas does not check label names beforehand. Before an existing issue changes or gets a comment, Qatlas
reads it once and refuses a pull request of the same number. Bodies and comments are stored exactly as given
and never interpreted. `github.issues.get`, `github.issues.update`, `github.issues.close`,
`github.issues.reopen`, `github.comments.list`, and `github.comments.create` refuse the number of a pull
request as `invalid-request` and name the number and the repository, with a classic and a fine-grained token
alike; a change is never sent. A fine-grained token without access to pull requests is refused by GitHub on
such a number; only then Qatlas looks the number up once, and a refusal that names no pull request stays
`permission`.

```text
qatlas: invalid-request: number 39 in repository octo-org/example is a pull request; issue tools do not handle pull requests
```

Project field values are named, not identified: `fields` maps field names to an option name of a
single-select field, a list of option names of a multi-select field, an iteration title (current, planned, or
completed), a date as `YYYY-MM-DD`, a text, a number, or `null` to clear the field. A multi-select list
replaces the selected options, names at most 50 of them, and clears the field when it is empty (`[]`). Names
are compared without case. Title, assignees, labels, and other built-in fields are not set through `fields`.
At most 20 values are accepted per request. The fields a project holds and their options are listed under
[project fields](#project-fields).

```sh
echo '{"item_id":"PVTI_...","fields":{"Status":"In progress","Estimate":3,"Areas":["API","Docs"]}}' |
  qatlas invoke github.projectitems.update --connection roadmap --confirm
echo '{"repository":"octo-org/example","title":"Crash on start","fields":{"Status":"Todo"}}' |
  qatlas invoke github.projectissues.create --connection roadmap --confirm
```

### Items and drafts

The item tools address an item by the `item_id` of `github.projectitems.list`. Each resolves the project and
every item it names in one query and refuses an item of another project, or one GitHub does not show, as
`not-found` before any change; that holds for `after_id` of `github.projectitems.move` as well.
`github.projectitems.move` sets the project order, which views without a sort show: it places the item
directly after `after_id`, or first when `after_id` is left out. `github.projectitems.delete` removes the item
and its field values; the issue or pull request stays in its repository, while a draft issue exists only as
its item and is deleted with it. A repeated delete finds no item and is refused, and an issue added again
gets a new item, so a repetition never removes anything else. `github.projectitems.archive` reads whether
the item is archived with the project; an archived item is answered with `archived: true` without a change,
so a repeated archive succeeds like the first one.

`github.projectdrafts.update` and `github.projectdrafts.convert` accept only the item of a draft issue and
refuse an issue or pull request as `invalid-request`. `assignees` names logins and replaces every assignee of
the draft; `[]` removes them all, and a login GitHub does not know is `not-found`. `body: ""` empties the
body. `github.projectdrafts.convert` creates the issue in `repository`, which the targets must allow beside
the project, and answers with its number and URL. The item keeps its identifier, its place, and its field
values, so a repeated conversion finds no draft and is refused without opening a second issue. The token
needs write access to the issues of that repository besides the project.

```sh
echo '{"item_id":"PVTI_...","after_id":"PVTI_..."}' |
  qatlas invoke github.projectitems.move --connection roadmap --confirm
echo '{"item_id":"PVTI_...","repository":"octo-org/example"}' |
  qatlas invoke github.projectdrafts.convert --connection roadmap --confirm
```

### Batches and partial results

A project change reads the project, its fields, their options and iterations, and the item or issue it
concerns in one query, and resolves every field value before the first change is sent. An unknown field,
option, or iteration, a value of the wrong type, or an item of another project is refused without any
change. The values are then written in field-name order in serial GraphQL requests of at most 10 aliased
mutations each. Adding an item and setting its fields are separate requests, because GitHub does not allow
both in one; `github.projectissues.create` creates the issue, adds it, and sets its fields in three steps.

The answer lists every field with its `result`: `updated`, `failed`, `unknown` when the value may have been
written without a confirmation, or `not_sent` when an earlier failure stopped the request. A failure ends the
request after its batch. `complete` is true only when every step succeeded; otherwise `error` says what
stopped it. `github.projectitems.update` fails as a whole only when no value was or may have been written.
Once an issue, a draft, or an item exists, the answer always reports it, including an issue that was created
but could not be added to the project, so a caller never creates it a second time to learn the outcome.

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

`github.pullrequests.list`, `github.pullrequestfiles.list`, and `github.pullrequestcommits.list` page by
number as well, but GitHub answers these plain array routes without a total count, so `has_more` follows
whether GitHub's `Link` response header names a following page instead. `github.pullrequestchecks.list`
answers no cursor at all: it reads up to 100 check runs and 100 legacy statuses at the head commit in one
call and reports `truncated` when GitHub held more of either.

## GitHub Actions

A connection can observe the GitHub Actions of a repository it allows and, separately, operate them. The two
groups are distinct tools with distinct effects: the observer tools are reads, and the operator tools are
executions with the effect `execute`. Every route lies below the chosen repository; no tool accepts an owner
or a free REST path.

| Tool | Effect | Idempotency | Confirmation | Does |
| --- | --- | --- | --- | --- |
| `github.workflows.list` | read | safe | none | lists workflows: identifier, name, file path, state |
| `github.workflows.get` | read | safe | none | reads one workflow by identifier or file name such as `ci.yml` |
| `github.workflowruns.list` | read | safe | none | lists compact runs, newest first, with structured filters |
| `github.workflowruns.get` | read | safe | none | reads one run |
| `github.workflowjobs.list` | read | safe | none | lists the compact jobs of one run, without steps |
| `github.workflowjobs.get` | read | safe | none | reads one job with its compact steps |
| `github.workflowjobs.log` | read | safe | none | reads the last lines of one job log within a hard size limit |
| `github.workflowartifacts.list` | read | safe | none | lists the artifact metadata of one run |
| `github.workflows.dispatch` | execute | non-idempotent | required | starts one `workflow_dispatch` run on a branch or tag |
| `github.workflowruns.rerun` | execute | non-idempotent | required | re-runs every job of a completed run |
| `github.workflowruns.rerunfailed` | execute | non-idempotent | required | re-runs the failed jobs of a completed run and their dependents |
| `github.workflowruns.cancel` | execute | idempotent | required | asks GitHub to cancel a run; a completed run is reported with its state |

No observer or operator tool changes a workflow file or a setting; that is the listed-only group under
[workflow maintenance and Actions administration](#workflow-maintenance-and-actions-administration). No tool
force-cancels a run, approves a deployment, deletes a log, or administers secrets, variables, environments,
runners, deployments, or an organization.

### Observer and operator

An observer is a connection whose `permissions` lack `execute`, or whose `tools` list names no
operator tool. It neither discovers nor runs an operator tool: `qatlas tools`, `qatlas.search`, and
`qatlas.describe` do not offer them for it, and an invocation is refused as an unsupported capability before
a secret is read. Planning permissions (`create`, `update`) never allow an execution; only `execute` does.

The terminal editor offers two setup profiles that are never preselected. `actions-observer` ticks `[read]`
and the eight observer tools. `actions-operator` ticks `[read, execute]`, the observer tools, and the four
operator tools. The recommended profile `read` stays without Actions tools. A connection without a
`tools` list offers every read, the observer tools included, but never a listed-only tool; give it a `tools`
list to narrow that.

```yaml
connections:
  ci-observer:
    service: github
    credential: github-reader
    target: repos/octo-org/example
    permissions: [read]
    tools: [github.workflows.list, github.workflows.get, github.workflowruns.list, github.workflowruns.get,
      github.workflowjobs.list, github.workflowjobs.get, github.workflowjobs.log, github.workflowartifacts.list]
  ci-operator:
    service: github
    credential: github-operator
    target: repos/octo-org/example
    permissions: [read, execute]
    tools: [github.workflowruns.list, github.workflowruns.get, github.workflowjobs.list,
      github.workflowjobs.log, github.workflows.dispatch, github.workflowruns.rerunfailed,
      github.workflowruns.cancel]
```

### Diagnosing a run

```sh
echo '{"status":"failure","branch":"main","limit":5}' | qatlas invoke github.workflowruns.list --connection ci-observer
qatlas invoke github.workflowjobs.list --connection ci-observer --arg run_id=30433642
qatlas invoke github.workflowjobs.log --connection ci-observer --arg job_id=399444496 --arg lines=40
```

`github.workflowruns.list` filters by `workflow` (identifier or file name), `status` (a status or a
conclusion: `completed`, `action_required`, `cancelled`, `failure`, `neutral`, `skipped`, `stale`,
`success`, `timed_out`, `in_progress`, `queued`, `requested`, `waiting`, `pending`), `branch`, `event`
(such as `push` or `workflow_dispatch`), `actor` (a login, `[bot]` allowed), and `created_from` /
`created_to` (a date `YYYY-MM-DD` or a UTC time `YYYY-MM-DDTHH:MM:SSZ`, both inclusive). A run carries its
identifier, workflow name and identifier, title, run number, attempt, event, status, conclusion, branch,
commit, actor, times, and URL; pull requests, the repository, and the commit message are left out. The title
is untrusted data. `github.workflowjobs.list` takes `filter`: `latest` (default) for the latest attempt or
`all` for every attempt.

### Logs and artifacts

A job log may carry anything a workflow printed and is treated as sensitive, untrusted data. Only
`github.workflowjobs.log` reads it; no other answer embeds a log. The tool returns the last `lines` lines (1
to 500, default 50) of at most the last `max_bytes` bytes (1024 to 65536, default 8192) and `truncated` when
the log holds more. Invalid UTF-8, terminal escape sequences, and control characters other than tab and line
feed are removed, so no binary content reaches the answer. The log passes through memory into the answer
only: Qatlas never writes it to disk, and audit events never carry it.

GitHub answers the log route with a redirect to a short-lived signed address, usually on another host.
Qatlas follows exactly that one https redirect without the token and without any other GitHub header, asks
for the last `max_bytes` bytes only, and follows no further redirect. A storage that ignores the range is
read up to 8 MiB, of which only the end is kept; a longer log is then refused.

Artifacts are zip archives. A bounded download would still be binary and would need unpacking or storing,
so Qatlas reads only their metadata: identifier, name, size in bytes, expiry, and digest. Run logs, which
GitHub serves as a zip archive as well, are read per job instead.

### Executions

Every execution needs `execute` in the connection's `permissions`, the tool in its `tools` list if it has
one, and confirmation in its own invoke request (`--confirm`, or `confirm: true` over MCP). A missing
permission or confirmation ends the request before a secret is read and before GitHub is contacted. An
execution is sent exactly once; an unclear outcome (a timeout, a dropped connection, a server error) says
that the execution may have happened, is never repeated by Qatlas, and must be checked with
`github.workflowruns.list` or `github.workflowruns.get` before a repetition. After each execution the next
request of the same token waits one second.

`github.workflows.dispatch` takes `workflow`, `ref` (a branch or tag), and `inputs`, a flat map of input
names to string values (at most 25, each at most 4096 characters; write a boolean as `"true"` or `"false"`).
Before it dispatches, Qatlas reads the workflow and its file at `ref` and checks the inputs against the
`on.workflow_dispatch.inputs` the file declares: an undeclared input, a missing required input without a
default, a `boolean` other than `true` or `false`, a `number` that is no number, and a `choice` outside its
options are refused without a dispatch. A workflow without the `workflow_dispatch` trigger at `ref`, an
inactive workflow, and a workflow without a file are refused as well. GitHub validates the dispatch again.
GitHub answers a dispatch without the new run; list the runs with `event: workflow_dispatch` and the branch
to follow it.

```sh
echo '{"workflow":"release.yml","ref":"main","inputs":{"channel":"beta"}}' |
  qatlas invoke github.workflows.dispatch --connection ci-operator --confirm
```

The re-runs and the cancel read the run first. A re-run needs a completed run, so GitHub's refusal never
reads like a missing permission; it answers `run_id` and `accepted`.

The cancel is idempotent. For a run that has not completed it sends one cancel and answers `run_id` and
`accepted: true`; the run then ends asynchronously. For a run that has already completed, cancelled or not,
it sends nothing and answers `accepted: false` with the `status` and `conclusion` it found. When the run
completes between the read and the cancel, GitHub refuses the cancel; Qatlas reads the run again and answers
the same way.

### Tokens for Actions

GitHub decides with every request. A refused Actions request names what such a request needs; it never
claims what the configured token holds.

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| observer tools | `repo` for a private repository; a public repository needs no scope | Actions: read |
| `github.workflows.dispatch` | `repo` | Actions: read and write, and Contents: read for the workflow file |
| re-runs and cancel | `repo` | Actions: read and write |

A repository or organization policy may forbid an execution although the token would allow it. A classic
token needs the `workflow` scope only to change workflow files, which only the listed-only
`github.workflowfiles.create` and `github.workflowfiles.update` do.

## Pull requests

GitHub's project items and its issue and comment tools still see a pull request only as an item, or refuse
its number as an `invalid-request`; the tools of this section read, create, change, close, reopen, and merge
the pull requests of a repository themselves. They offer no way to review a pull request or to comment on
one. The six reads are offered by the not-recommended setup profile `pull-requests`; the not-recommended
profile `pull-requests-operator` adds the changes, described under [Changes](#changes-1) below, but never the
merge, described under [Merge and conflicts](#merge-and-conflicts): it is offered only where a connection's
`tools` list names `github.pullrequests.merge`, because a merge into the wrong repository's default branch is
the costliest mistake there. Neither profile is offered by the recommended profile `read`. Every route lies
below the chosen repository; no tool accepts an owner or a free REST path.

| Tool | Effect | Idempotency | Confirmation | Does |
| --- | --- | --- | --- | --- |
| `github.pullrequests.list` | read | safe | none | lists compact pull requests, filtered by `state`, `base`, and `head` |
| `github.pullrequests.get` | read | safe | none | reads one pull request with its body, merge state, and requested reviewers |
| `github.pullrequestfiles.list` | read | safe | none | lists its changed files with a bounded patch excerpt |
| `github.pullrequestcommits.list` | read | safe | none | lists its commits |
| `github.pullrequestdiffs.get` | read | safe | none | reads its unified diff, cut to at most 65536 bytes from its start |
| `github.pullrequestchecks.list` | read | safe | none | reads the check runs and the combined commit status at its head commit |

```yaml
connections:
  pull-reader:
    service: github
    credential: github-reader
    target: repos/octo-org/example
    permissions: [read]
    tools: [github.pullrequests.list, github.pullrequests.get, github.pullrequestfiles.list,
      github.pullrequestcommits.list, github.pullrequestdiffs.get, github.pullrequestchecks.list]
```

`github.pullrequests.list` filters by `state` (`open`, `closed`, or `all`; `open` when omitted), `base` (a
branch name), and `head` (a branch name, or `LOGIN:branch` for a fork), in GitHub's own order; a pull request
carries its number, title, state, whether it is a draft, its author, its head and base branch, its head
commit, its labels, whether it is merged, its times, and its URL. Title, body, branch names, and commit
messages are untrusted data. `github.pullrequests.get` adds the full body, `mergeable` (absent while GitHub
is still computing it), `mergeable_state`, `merge_commit_sha`, the base commit, the requested reviewers, and
the number of commits and changed files.

```sh
echo '{"state":"open","base":"main","limit":10}' | qatlas invoke github.pullrequests.list --connection pull-reader
qatlas invoke github.pullrequests.get --connection pull-reader --arg number=42
```

`github.pullrequestfiles.list` reads the changed files with path, status, additions, deletions, changes, the
previous path for a rename, and a patch excerpt: at most 4096 bytes of it, cut at a valid character boundary,
with `patch_truncated` when GitHub's patch held more; a file GitHub sends no patch for, such as a binary
file, carries none. `github.pullrequestcommits.list` reads each commit's SHA, its author (the linked GitHub
login when GitHub reports one, otherwise the commit's author name), its message, and its time; the message is
untrusted data. `github.pullrequestdiffs.get` reads the unified diff through GitHub's diff media type, from
its start, up to the hard limit of 65536 bytes; `truncated` says when the diff held more. None of the three is
ever read unbounded.

`github.pullrequestchecks.list` takes `number` alone: it reads the pull request to resolve its head commit,
then the check runs (the GitHub Checks API, which Actions and most third-party CI systems report through) and
the combined legacy commit status (the older Status API) at that commit, and merges them into one compact
list with name, status, conclusion, the most recent time, and a details URL; a legacy status is mapped into
the same vocabulary, with `pending` becoming `in_progress` without a conclusion. `overall` summarises them:
`pending` while a check still runs, `failure` once a finished check did not succeed, `success` when every one
did, and `none` when nothing reported a check for the commit. `truncated` says when GitHub held more than the
100 check runs or 100 statuses read per call.

### Changes

| Tool | Effect | Idempotency | Confirmation | Does |
| --- | --- | --- | --- | --- |
| `github.pullrequests.create` | create | non-idempotent | required | opens one pull request with `title`, `head`, `base`, `body`, `draft`, `maintainer_can_modify` |
| `github.pullrequests.update` | update | idempotent | required | replaces `title`, `body`, or `base`, marks it ready for review or a draft, or changes `maintainer_can_modify`; left-out fields stay |
| `github.pullrequests.close` | update | idempotent | required | closes it without merging; a pull request already closed, merged or not, is left as it is |
| `github.pullrequests.reopen` | update | idempotent | required | opens a closed pull request again; a merged one is refused |
| `github.pullrequestbranches.update` | update | non-idempotent | required | merges the base branch into the head branch while the head is still `expected_head_sha` |

`github.pullrequests.create` and `github.pullrequests.update` write `title` and `body` through REST, exactly
as the issue tools do; `head` is a branch, or `LOGIN:branch` for a fork. `github.pullrequests.update` writes a
`draft` change through a separate GraphQL mutation, `markPullRequestReadyForReview` or
`convertPullRequestToDraft`, because GitHub's REST route has no field for it; when both a REST field and
`draft` are given, the REST change is sent first, then the draft change, and the pull request is read once
more only then, so the answer never reports a draft state the REST answer could not have known about.
`github.pullrequests.close` and `github.pullrequests.reopen` read the pull request first: a repeated call on
a pull request already in the state asked for changes nothing and is success, like the issue tools; reopening
a pull request GitHub reports as merged is refused before a request is sent, because GitHub has no way to
reopen a merged pull request.

```sh
echo '{"title":"Add retry logic","head":"feature/retry","base":"main"}' |
  qatlas invoke github.pullrequests.create --connection pull-operator --confirm
echo '{"number":42,"draft":false}' | qatlas invoke github.pullrequests.update --connection pull-operator --confirm
qatlas invoke github.pullrequests.close --connection pull-operator --arg number=42 --confirm
```

`github.pullrequestbranches.update` requires `expected_head_sha`, the pull request's current head commit as
`github.pullrequests.get` reports it, so a head that changed since it was last read is never merged into
blindly; GitHub queues the merge of the base branch into the head branch and answers `accepted: true` before
it finishes, so the new head commit shows up only on a later read. A stale `expected_head_sha` is refused
without queuing anything.

### Merge and conflicts

`github.pullrequests.merge` merges one pull request with a REST `PUT`. It is high risk, on the scale of the
[listed-only workflow maintenance tools](#workflow-maintenance-and-actions-administration): a connection
offers it only while its `tools` list names it, whatever its permissions, and no profile a new connection
starts with, recommended or not, may select it.

| Tool | Effect | Idempotency | Confirmation | Does |
| --- | --- | --- | --- | --- |
| `github.pullrequests.merge` | update | idempotent | required | merges with `sha`, and optionally `method` (`merge`, `squash`, or `rebase`), `commit_title`, `commit_message`; offered only where a connection's `tools` list names it |

`sha` is required: the pull request's current head commit, exactly as `github.pullrequests.get` reports it.
Qatlas reads the pull request first. Already merged with exactly that head commit is success without another
request, so a repeated merge with the same `sha` never fails; already merged with a different head commit is
refused as `invalid-request`, naming both commits. Otherwise Qatlas sends the merge exactly once and never
retries it:

- **409 Conflict** means GitHub compared `sha` against a head that changed since it was read. Qatlas reads
  the pull request again to name its now-current head and refuses as `invalid-request`, naming the expected
  and the current commit, so the next call can read the pull request again and merge with the head it now
  has, or accept it and merge again.
- **405 Method Not Allowed** means GitHub cannot merge the pull request in its current state, for example
  branch protection, a missing required review, or a failing check. Qatlas answers `provider-error` with that
  reason; the merge is never repeated automatically, since GitHub's decision only changes once whatever
  blocks the merge is resolved.

```yaml
connections:
  pull-operator:
    service: github
    credential: github-operator
    target: repos/octo-org/example
    permissions: [read, create, update]
    tools: [github.pullrequests.list, github.pullrequests.get, github.pullrequestfiles.list,
      github.pullrequestcommits.list, github.pullrequestdiffs.get, github.pullrequestchecks.list,
      github.pullrequests.create, github.pullrequests.update, github.pullrequests.close,
      github.pullrequests.reopen, github.pullrequestbranches.update]
  pull-merger:
    service: github
    credential: github-operator
    target: repos/octo-org/example
    permissions: [read, update]
    tools: [github.pullrequests.get, github.pullrequests.merge]
```

```sh
qatlas invoke github.pullrequests.get --connection pull-merger --arg number=42
echo '{"number":42,"sha":"6cb1a1e...","method":"squash"}' |
  qatlas invoke github.pullrequests.merge --connection pull-merger --confirm
```

`pull-operator` never offers the merge, whatever its permissions, because its `tools` list does not name it;
`pull-merger` offers only the read `github.pullrequests.get` and the merge, so a caller reads the current head
commit and merges with it in two calls on the same connection.

### Tokens for pull requests

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.pullrequests.list`, `get`, `github.pullrequestfiles.list`, `github.pullrequestcommits.list`, `github.pullrequestdiffs.get` | `repo` for a private repository; `public_repo` for a public one | Pull requests: read |
| `github.pullrequestchecks.list` | `repo` | Checks: read, and Commit statuses: read |
| `github.pullrequests.create`, `update`, `close`, `reopen`, `github.pullrequestbranches.update` | `repo` | Pull requests: read and write |
| `github.pullrequests.merge` | `repo` | Pull requests: read and write, plus Contents: read and write |

## Workflow maintenance and Actions administration

Two more groups of repository tools change what runs in a repository and with which rights: the workflow
maintainer writes workflow files, which decide what code runs with the repository's secrets, and the Actions
administrator decides whether Actions run at all, which actions they may use, and what the `GITHUB_TOKEN` of
every run may do. They are high risk, and Qatlas keeps them behind a boundary of their own:

- **Listed only.** A connection offers such a tool only when its `tools` list names it and its `permissions`
  allow the tool's effect. A connection without a `tools` list never offers one, whatever its permissions: a
  planning connection with `create` and `update`, an operator with `execute`, or a connection with every
  permission neither discovers nor runs them. `qatlas tools`, `qatlas describe`, `qatlas.search`,
  `qatlas.describe`, route selection, and invoke apply the same rule, and the complete tool contract
  (`qatlas describe --full`) shows `requires_tool_allow_list: true`. The terminal editor marks these tools
  `(listed only)`.
- **Never preselected.** No profile a new connection starts with selects them, and the recommended profile
  of a provider may not. The profiles `workflow-maintainer` and `actions-admin` exist only to be chosen on
  purpose; they keep the two groups apart.
- **One repository per call.** Each call acts on one repository inside the connection's targets, and every
  route lies below that repository. Without targets, such a connection reaches every repository its token can
  change, so give it targets that name exactly the repositories it may maintain.
- **Confirmed and sent once.** Every change needs confirmation in its own invoke request, is sent exactly
  once, and is never retried; an unclear outcome says that the change may have been applied. A refused or
  unconfirmed request ends before a secret is read and before GitHub is contacted.

| Tool | Effect | Idempotency | Confirmation | Does |
| --- | --- | --- | --- | --- |
| `github.workflowfiles.list` | read | safe | none | lists the workflow files with path, blob SHA, and size, without content |
| `github.workflowfiles.get` | read | safe | none | reads one workflow file with its content and blob SHA |
| `github.workflowfiles.create` | create | idempotent | required | commits one new workflow file; fails when the file exists |
| `github.workflowfiles.update` | update | idempotent | required | replaces one workflow file while it still has the given blob SHA |
| `github.workflows.enable` | update | idempotent | required | enables one workflow |
| `github.workflows.disable` | update | idempotent | required | disables one workflow; a disabled workflow stays as it is |
| `github.actionspermissions.get` | read | safe | none | reads `enabled` and `allowed_actions` |
| `github.actionspermissions.update` | update | idempotent | required | changes `enabled` or `allowed_actions` |
| `github.workflowpermissions.get` | read | safe | none | reads `default_workflow_permissions` and `can_approve_pull_request_reviews` |
| `github.workflowpermissions.update` | update | idempotent | required | changes either of them |

The reads are listed only as well: a workflow file and the Actions settings are part of what this group
maintains, and the observer tools already read what diagnosing a run needs. A create and an update are
idempotent because a repetition writes nothing: GitHub refuses a create of an existing file and an update
whose blob SHA the file no longer has.

### Paths, blob SHAs, and content

A `path` is `.github/workflows/NAME.yml` or `.github/workflows/NAME.yaml`, where `NAME` has 1 to 100
letters, digits, `.`, `_`, or `-`, does not start with `.`, and holds no `..`. Nothing else is accepted: no
subdirectory, no other directory, no absolute or relative prefix, no percent-encoding, no whitespace, and no
character outside ASCII, so a lookalike slash or dot cannot lead elsewhere. The path is checked before a
secret is read, and a path of another form, `ci.yml` as well as a subdirectory, is an invalid request that
names the rule: `$.path does not have the required form a file directly in .github/workflows/ ending in .yml
or .yaml`. No tool deletes or renames a file or writes any other path.

`github.workflowfiles.update` needs `sha`, the blob SHA `github.workflowfiles.get` or
`github.workflowfiles.list` reported. GitHub writes the file only while it still has that blob; otherwise
nothing is written, and the refusal says so: read the file again and apply the change to its current
content. `github.workflowfiles.create` takes no `sha` and never replaces an existing file.

`content` is the complete new file as text: 1 byte to 512 KiB of valid UTF-8 without control characters
other than tab, line feed, and carriage return. `message` is the commit message, 1 to 1000 characters
without control characters other than tab and line breaks. `branch` names the branch to commit to and is the
default branch when omitted; reads take `ref`, a branch or tag. Qatlas does not generate, repair, or
interpret workflows; GitHub decides whether a file is a valid workflow. `github.workflowfiles.get` refuses a
file larger than 512 KiB or one that is not text, and returns the content as untrusted data only on that
explicit read.

A change answers with metadata only: `path`, `branch`, `previous_sha` (for an update), the new blob `sha`,
`size`, `commit_sha`, and `commit_url`. It never echoes the content or the commit message, and the audit
event of a change names only the request, tool, connection, confirmation, result, and time.

`github.workflows.enable` and `github.workflows.disable` take `workflow`, an identifier or a file name such
as `ci.yml`, accept only a workflow whose file lies below `.github/workflows/`, and answer `previous_state`
and `state`. Both are idempotent: enabling an active workflow answers `active` for both. A workflow already in
a disabled state (`disabled_manually`, `disabled_inactivity`, or `disabled_fork`) is not changed by a disable,
which sends nothing and answers that state as `previous_state` and `state`.

```sh
qatlas invoke github.workflowfiles.get --connection ci-maintainer --arg path=.github/workflows/ci.yml
echo '{"path":".github/workflows/ci.yml","sha":"3d21ec53a331a6f037a91c368710b99387d012c1",
  "content":"name: CI\non: [push, pull_request]\njobs: {}\n","message":"ci: also run on pull requests"}' |
  qatlas invoke github.workflowfiles.update --connection ci-maintainer --confirm
```

### Actions settings

`github.actionspermissions.update` takes `enabled` and `allowed_actions` (`all`, `local_only`, or
`selected`); `selected` keeps the selection of allowed actions maintained in the repository settings, which
Qatlas does not change: that pattern list is a separate, wider surface. `github.workflowpermissions.update`
takes `default_workflow_permissions` (`read` or `write`) and `can_approve_pull_request_reviews`. At least one
value is required, and a value left out stays as it is: GitHub replaces a setting as a whole, so Qatlas
reads it first and sends it with the requested values in one change. `allowed_actions` applies only while
Actions are enabled and is refused for disabled Actions unless `enabled: true` comes with it. The answer
holds `before` and `after`.
An organization or enterprise policy may fix a value; GitHub's refusal is reported, and nothing is changed or
retried.

### Credential

Give these connections a credential of their own, used by no other connection, ideally a fine-grained token
limited to the one repository with only the permissions of the tools the connection lists. GitHub decides on
every request and documents the requirements per endpoint; a refusal names what the request needs and never
claims what the token holds. A branch protection or repository rule may refuse a file change although the
token would allow it.

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.workflowfiles.list`, `get` | `repo` for a private repository | Contents: read |
| `github.workflowfiles.create`, `update` | `repo` and `workflow` | Contents: read and write, and Workflows: read and write |
| `github.workflows.enable`, `disable` | `repo` | Actions: read and write |
| `github.actionspermissions.get`, `github.workflowpermissions.get` | `repo`, as a repository administrator | Administration: read |
| `github.actionspermissions.update`, `github.workflowpermissions.update` | `repo`, as a repository administrator | Administration: read and write |

```yaml
connections:
  ci-maintainer:
    service: github
    credential: github-maintainer
    target: repos/octo-org/example
    permissions: [read, create, update]
    tools: [github.workflows.list, github.workflows.get, github.workflowfiles.list, github.workflowfiles.get,
      github.workflowfiles.create, github.workflowfiles.update, github.workflows.enable, github.workflows.disable]
  ci-admin:
    service: github
    credential: github-maintainer
    target: repos/octo-org/example
    permissions: [read, update]
    tools: [github.actionspermissions.get, github.actionspermissions.update, github.workflowpermissions.get,
      github.workflowpermissions.update]
```
