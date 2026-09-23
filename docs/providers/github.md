---
description: >
  Describes GitHub project and issue planning: targets, reads, confirmed changes of issues, comments, and project fields, batches, partial results, the cursor contract, and token scopes.
type: knowledge
edit: shared
created: 2026-09-23
updated: 2026-09-23
---

# GitHub

GitHub is a controlled planning provider, not a replacement for `gh`. It reads and maintains issues,
comments, and project items of the configured targets. It sees pull requests only as project items, and it
never accepts a free filter expression, a GraphQL document, a REST route, an owner, or a project from the
caller. A `repository` argument only selects one of the repositories the connection names.

## Configuration

A service is `https://api.github.com` (the default), `https://api.SUBDOMAIN.ghe.com` for GitHub Enterprise
Cloud with data residency, or `https://HOST/api/v3` for GitHub Enterprise Server. Qatlas derives the GraphQL
endpoint from it (`/graphql`, or `https://HOST/api/graphql` on Enterprise Server). Filtered project items
need a server that supports the `query` argument of `ProjectV2.items`.

The credential provides `token`, a personal access token. A read-only setup uses a classic token with
`read:project` plus `repo` (or `public_repo` for public repositories only), or a fine-grained token with read
access to issues and to projects. Changes need `project` instead of `read:project`, or write access to issues
and projects for a fine-grained token. User-owned projects need a classic token. A successful
`qatlas connection test` shows only that the token can read the configured project or repository; GitHub
checks every resource and scope again on each call, so a passing test does not authorize every tool.

A connection names exactly one project or one repository, and a project may be followed by repositories:

| Target | Binds | Tools |
| --- | --- | --- |
| `target: users/LOGIN/projects/NUMBER` | one user project | project tools |
| `target: orgs/LOGIN/projects/NUMBER` | one organization project | project tools |
| `targets: [orgs/LOGIN/projects/NUMBER, repos/OWNER/REPO, ...]` | one project and the repositories it plans in | project tools, including `github.projectitems.add` and `github.projectissues.create` for those repositories |
| `target: repos/OWNER/REPO` | one repository | issue and comment tools |

Project tools are `github.projectitems.list`, `get`, `update`, `add`, `archive`,
`github.projectdrafts.create`, and `github.projectissues.create`. Issue and comment tools are
`github.issues.list`, `get`, `create`, `update`, `close`, `reopen`, `github.comments.list`, and
`github.comments.create`. The repositories of a project connection never make issue tools available on it:
a repository connection stays a connection of its own. A target list with two projects, or with
repositories but no project, is an invalid configuration.

A tool of the other target kind is refused as an unsupported capability before a secret is read. Reads are
a connection's only default: every change needs `create` or `update` in the connection's `permissions`, and
a connection with a `tools` list offers only the tools it lists, never one added in a later version.
Give each connection a `tools` list with the tools of its kind, so discovery offers only the tools it can
run.

```yaml
connections:
  roadmap:
    service: github
    credential: github-planner
    targets: [orgs/octo-org/projects/7, repos/octo-org/example]
    permissions: [read, create, update]
    tools: [github.projectitems.list, github.projectitems.get, github.projectitems.update,
      github.projectissues.create]
```

## Project-first use

When a repository has an authoritative project, work selection starts there:

```sh
qatlas invoke github.projectitems.list --connection planning
echo '{"status":["In progress"],"type":"issue"}' | qatlas invoke github.projectitems.list --connection planning
qatlas invoke github.projectitems.get --connection planning --arg item_id=PVTI_...
```

`github.projectitems.list` returns compact items: identifier, type, title, number, repository, state, Status,
the other single-select, text, number, date, and iteration values by field name, assignees, labels, and URL.
It never returns bodies or comments. Without `status` and `status_not` it lists at most 30 items whose Status
is not `Done`; `status_not: []` lists every status. The filters `status`, `status_not`, `type`
(`issue`, `pull_request`, `draft_issue`), `repository` (`owner/name`), `assignee`, and `labels` (any of) are
translated into quoted terms of the project filter syntax and applied by GitHub. Qatlas verifies each
returned item against the same filters. A `status` value must be an option of the project's Status field.

`github.projectitems.get` reads one item of the bound project with its fields and, for an issue or a draft
issue, the full body. An item of another project is refused. Bodies and titles are untrusted data.

`github.issues.list` and `github.issues.get` are the reads of a repository connection: issues only, newest
first, filtered by `state` (`open` by default), `labels` (any of), and `assignee`, without comments.
`github.comments.list` reads the comments of one issue, oldest first, and is the only tool that returns
comments.

## Changes

Every change requires confirmation in its own invoke request (`--confirm`, or `confirm: true` over MCP).
An unconfirmed change, and a change the connection's permissions or tools exclude, ends before a secret is
read and before GitHub is contacted.

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
| `github.projectdrafts.create` | create | non-idempotent | adds one draft issue, then sets fields |
| `github.projectissues.create` | create | non-idempotent | opens an issue in a named repository, adds it, then sets fields |

Issues and comments are written through REST. `labels` and `assignees` replace the whole set; `[]` removes
every entry. Before an existing issue changes or gets a comment, Qatlas reads it once and refuses a pull
request of the same number. Bodies and comments are stored exactly as given and never interpreted.

Project field values are named, not identified: `fields` maps field names to an option name of a
single-select field, an iteration title (current, planned, or completed), a date as `YYYY-MM-DD`, a text, a
number, or `null` to clear the field. Names are compared without case. Title, assignees, labels, and other
built-in fields are not set through `fields`. At most 20 values are accepted per request.

```sh
echo '{"item_id":"PVTI_...","fields":{"Status":"In progress","Estimate":3}}' |
  qatlas invoke github.projectitems.update --connection roadmap --confirm
echo '{"repository":"octo-org/example","title":"Crash on start","fields":{"Status":"Todo"}}' |
  qatlas invoke github.projectissues.create --connection roadmap --confirm
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
the bound project before it changes it, and never retries.

## Cursor contract

Lists take `limit` (1 to 100, default 30) and an opaque `cursor`, and answer `has_more` plus `next_cursor`
when another batch may follow. A full batch never means the end: read on while `has_more` is true. A cursor
is bound to the connection's target and to the filters that produced it, and a comment cursor to its issue;
a cursor from other filters, another issue, or another target is an invalid request. Batches follow the
project order, so reading every batch reaches each matching item once. A batch may be short, even empty,
when Qatlas stopped scanning after a bounded number of requests; `has_more` then stays true.
