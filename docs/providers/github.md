---
description: >
  Describes GitHub project and issue planning and GitHub Actions: targets as an optional allow-list, target arguments and their defaults, reads, confirmed changes of issues, comments, and project fields, batches, partial results, the Actions observer and operator tools, log limits, the listed-only workflow maintainer and Actions administrator tools, the cursor contract, and token scopes.
type: knowledge
edit: shared
created: 2026-09-23
updated: 2026-09-23
---

# GitHub

GitHub is a controlled planning provider, not a replacement for `gh`. It reads and maintains issues,
comments, and project items, and it observes and, when allowed, operates the GitHub Actions of a repository.
On a connection that names them explicitly, it also maintains workflow files and Actions settings. It sees
pull requests only as project items, and it never accepts a free filter expression, a GraphQL document, or a
REST route from the caller. A `repository` or `project` argument names exactly one target, and it must lie
inside the connection's targets when the connection lists any.

## Configuration

A service is `https://api.github.com` (the default), `https://api.SUBDOMAIN.ghe.com` for GitHub Enterprise
Cloud with data residency, or `https://HOST/api/v3` for GitHub Enterprise Server. Qatlas derives the GraphQL
endpoint from it (`/graphql`, or `https://HOST/api/graphql` on Enterprise Server). Filtered project items
need a server that supports the `query` argument of `ProjectV2.items`.

The credential provides `token`, a personal access token. A read-only setup uses a classic token with
`read:project` plus `repo` (or `public_repo` for public repositories only), or a fine-grained token with read
access to issues and to projects. Changes need `project` instead of `read:project`, or write access to issues
and projects for a fine-grained token. User-owned projects need a classic token. The Actions tools have
their own requirements, listed under [GitHub Actions](#github-actions), and so have the tools of
[workflow maintenance and Actions administration](#workflow-maintenance-and-actions-administration). A
successful `qatlas connection test` shows only that the token can read the first project or repository the
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

`*` is accepted only as the whole last segment; there are no ranges and no other patterns. Owners,
repositories, and projects are compared without case, like GitHub compares them, and an entry listed twice
makes the file invalid. The token stays an independent upper bound: the targets never grant what the token
lacks, and the token never widens the targets, so a call reaches only what both allow. The single
`target: ...` form and existing lists, such as a project followed by the repositories it plans in, stay valid
and are read as the same allow-list.

One GitHub connection carries the project, issue, comment, and Actions tools together. Which of them it
offers is decided by `permissions` and `tools` alone. Reads are a connection's only default: every change
needs `create` or `update` in the connection's `permissions`, every Actions execution needs `execute`, and a
connection with a `tools` list offers only the tools it lists, never one added in a later version. The
workflow maintainer and Actions administrator tools are listed only: a connection without a `tools` list
never offers them, whatever its permissions.

### The target of a call

Every repository tool (issues, comments, Actions, workflow maintenance, and Actions administration) takes
`repository` as `OWNER/REPO`. Every project tool (`github.projectitems.list`, `get`, `update`, `archive`,
`github.projectdrafts.create`) takes `project` as `users/LOGIN/projects/NUMBER` or
`orgs/LOGIN/projects/NUMBER`. `github.projectitems.add` and `github.projectissues.create` touch both, take
both, and check both against the targets. In `github.projectitems.list`, `repository` stays a filter on the
items of the project, not a target.

An argument may be left out only when the targets allow exactly one repository, or exactly one project, and
not as a pattern: that one is the default of its kind. Nothing else chooses a target; in particular, the git
remote of the working directory never does, for `qatlas invoke` and a `qatlas mcp` broker alike. A connection
without targets, or with a pattern, therefore needs `repository` as `OWNER/REPO` on every repository call.

A default never widens or narrows the targets, and an explicit argument always wins. Every target is checked
before a secret is read and before GitHub is contacted, and every refusal is an `invalid-request` that names
the next step without echoing the value:

| Case | Next step the message names |
| --- | --- |
| a `repository` or `project` outside the targets | pass one the targets allow, or add it to the targets |
| no argument, and no default settles it | pass `repository` as `OWNER/REPO` or `project` as `users/LOGIN/projects/NUMBER` or `orgs/LOGIN/projects/NUMBER` |
| the targets name no target of the tool's kind | add one to the targets, or use another connection |
| a malformed argument, or a pattern as an argument | the argument's form |

A tool that the connection's `permissions` or `tools` exclude stays an unsupported capability, refused before
a secret is read as well.

Every result names the target the tool acted on in a field named like its argument, whether the argument
chose it or a default did: `repository` as `OWNER/REPO`, `project` as `users/LOGIN/projects/NUMBER` or
`orgs/LOGIN/projects/NUMBER`, and both for `github.projectitems.add` and `github.projectissues.create`. The
field sits beside the tool's own fields, so `github.issues.get` without an argument answers, for example:

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
check:

```text
qatlas: not-found: list issues: GitHub does not hold repository octo-org/example or does not show it to this token; check the name, and that the token can see it (classic: scope repo for a private repository; fine-grained: access to this repository)
qatlas: not-found: list project items: GitHub does not hold project users/octocat/projects/3 or does not show it to this token; check the name, and that the token can see it (classic: scope read:project; fine-grained: Projects access of its organization, as a user-owned project needs a classic token)
qatlas: not-found: get issue: GitHub does not hold issue #5 in repository octo-org/example or does not show it to this token; check the arguments, and that the token can see the repository (...)
```

| Code | GitHub's answer |
| --- | --- |
| `auth` | HTTP 401: GitHub rejected the token itself |
| `permission` | HTTP 403, or GraphQL `FORBIDDEN` or `INSUFFICIENT_SCOPES`: GitHub saw the target and refused this token explicitly, for example a classic token without `read:project`, or a fine-grained token on a user-owned project; the message names the target |
| `not-found` | HTTP 404 or GraphQL `NOT_FOUND`: the target or a resource inside it does not exist, or the token may not see it |
| `rate-limited` | GitHub asked to wait |
| `provider-error` | GitHub rejected the request in another way, for example as invalid or in the current state of the resource |

A change GitHub answered with `not-found` was not applied. The Actions and workflow maintenance tools keep
their own permission messages, listed under [Tokens for Actions](#tokens-for-actions) and
[Credential](#credential).

### Setup profiles

The terminal editor starts a new connection on the setup profile `read`, which ticks `[read]` and the reads
`github.projectitems.list`, `github.projectitems.get`, `github.issues.list`, `github.issues.get`, and
`github.comments.list`. The profile `planning` ticks
`[read, create, update]` with the project reads, `github.projectitems.update`, `github.projectitems.add`,
`github.projectdrafts.create`, and `github.projectissues.create`; archiving stays unticked. The profiles
`actions-observer` and `actions-operator` are described under [GitHub Actions](#github-actions). A profile is a
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

`github.projectitems.get` reads one item of the chosen project with its fields and, for an issue or a draft
issue, the full body. An item of another project is refused. Bodies and titles are untrusted data.

`github.issues.list` and `github.issues.get` read the issues of the chosen repository: issues only, newest
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
the chosen project before it changes it, and never retries.

## Cursor contract

Lists take `limit` (1 to 100, default 30) and an opaque `cursor`, and answer `has_more` plus `next_cursor`
when another batch may follow. A full batch never means the end: read on while `has_more` is true. A cursor
is bound to the repository or project of the call and to the filters that produced it, and a comment cursor
to its issue; a cursor from other filters, another issue, or another repository or project is an invalid
request. Batches follow the
project order, so reading every batch reaches each matching item once. A batch may be short, even empty,
when Qatlas stopped scanning after a bounded number of requests; `has_more` then stays true.

The Actions lists follow the same contract, but GitHub pages them by number: a continuation keeps the batch
size of its first batch, whatever `limit` it names. They are ordered newest first, so a run started while a
caller pages can move an older run onto the next batch, where it appears again; no run is skipped by that.
GitHub answers at most 1000 runs of a run list filtered by `status`, `branch`, `event`, `actor`, or time;
`has_more` turns false there, and a narrower filter reaches older runs.

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
| `github.workflowruns.cancel` | execute | idempotent | required | asks GitHub to cancel a run that has not completed |

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

The re-runs and the cancel read the run first: a re-run needs a completed run and a cancel one that has not
completed, so GitHub's refusal of either never reads like a missing permission.

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

## Workflow maintenance and Actions administration

Two more groups of repository tools change what runs in a repository and with which rights: the workflow
maintainer writes workflow files, which decide what code runs with the repository's secrets, and the Actions
administrator decides whether Actions run at all, which actions they may use, and what the `GITHUB_TOKEN` of
every run may do. They are high risk, and Qatlas keeps them behind a boundary of their own:

- **Listed only.** A connection offers such a tool only when its `tools` list names it and its `permissions`
  allow the tool's effect. A connection without a `tools` list never offers one, whatever its permissions: a
  planning connection with `create` and `update`, an operator with `execute`, or a connection with every
  permission neither discovers nor runs them. `qatlas tools`, `qatlas describe`, `qatlas.search`,
  `qatlas.describe`, route selection, and invoke apply the same rule, and the tool contract shows
  `requires_tool_allow_list: true`. The terminal editor marks these tools `(listed only)`.
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
| `github.workflows.disable` | update | idempotent | required | disables one workflow |
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
secret is read. No tool deletes or renames a file or writes any other path.

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
and `state`.

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
