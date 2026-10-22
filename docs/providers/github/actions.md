---
description: >
  Describes the GitHub Actions tools: the observer and operator tools, diagnosing a run, log and artifact limits, billable time, the listed-only deletion of run logs, executions, and the listed-only workflow maintainer and Actions administrator tools with their paths, settings, and credential.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# GitHub Actions

The Actions tools of the [GitHub provider](README.md). Every tool takes `repository` as described under
[the target of a call](README.md#the-target-of-a-call).

A connection can observe the GitHub Actions of a repository it allows and, separately, operate them. The two
groups are distinct tools with distinct effects: the observer tools are reads, and the operator tools are
executions with the effect `execute`. Every route lies below the chosen repository; no tool accepts an owner
or a free REST path.

| Tool | Does |
| --- | --- |
| `github.workflows.list` | lists workflows |
| `github.workflows.get` | reads one workflow by identifier or file name such as `ci.yml` |
| `github.workflow-runs.list` | lists compact runs, newest first, with structured filters |
| `github.workflow-runs.get` | reads one run |
| `github.workflow-jobs.list` | lists the compact jobs of one run, without steps |
| `github.workflow-jobs.get` | reads one job with its compact steps |
| `github.workflow-jobs.log` | reads the last lines of one job log within a hard size limit |
| `github.workflow-artifacts.list` | lists the artifact metadata of one run |
| `github.workflow-runs.usage` | reads the billable time of one run for each runner operating system |
| `github.workflows.dispatch` | starts one `workflow_dispatch` run on a branch or tag |
| `github.workflow-runs.rerun` | re-runs every job of a completed run |
| `github.workflow-runs.rerun-failed` | re-runs the failed jobs of a completed run and their dependents |
| `github.workflow-runs.cancel` | asks GitHub to cancel a run; a completed run is reported with its state |
| `github.workflow-run-logs.delete` | deletes all logs of one run; listed-only, see [Deleting run logs](#deleting-run-logs) |

No observer or operator tool changes a workflow file or a setting; that is the listed-only group under
[workflow maintenance and Actions administration](#workflow-maintenance-and-actions-administration). No tool
force-cancels a run, approves a deployment, or administers secrets, variables, environments,
runners, deployments, or an organization.

## Observer and operator

An observer is a connection whose `effects` lack `execute`, or whose `tools` list names no
operator tool. It neither discovers nor runs an operator tool: `qatlas tools`, `qatlas.search`, and
`qatlas.describe` do not offer them for it, and an invocation is refused as an unsupported capability before
a secret is read. Planning effects (`create`, `update`) never allow an execution; only `execute` does.

The terminal editor offers two setup profiles that are never preselected. `actions-observer` selects
the nine observer tools. `actions-operator` selects the observer tools and the four
operator tools. No profile selects `github.workflow-run-logs.delete`. The recommended profile `read` stays without Actions tools. A connection without a
`tools` list offers every read, the observer tools included, but never a listed-only tool; give it a `tools`
list to narrow that.

```yaml
connections:
  ci-observer:
    service: github
    credential: github-reader
    target: repos/octo-org/example
    tools: [github.workflows.list, github.workflows.get, github.workflow-runs.list, github.workflow-runs.get,
      github.workflow-jobs.list, github.workflow-jobs.get, github.workflow-jobs.log, github.workflow-artifacts.list,
      github.workflow-runs.usage]
  ci-operator:
    service: github
    credential: github-operator
    target: repos/octo-org/example
    tools: [github.workflow-runs.list, github.workflow-runs.get, github.workflow-jobs.list,
      github.workflow-jobs.log, github.workflows.dispatch, github.workflow-runs.rerun-failed,
      github.workflow-runs.cancel]
```

## Diagnosing a run

`github.workflow-runs.list` names the repository once at the result level and leaves out pull requests, the per-run
repository, the commit message, and the URL, the URL because it is derivable from the repository and the
identifier. Its `status` filter takes a status or a conclusion. The title is untrusted data.
`github.workflow-runs.get` reads one run, including its URL. `github.workflow-jobs.list` names the repository once
at the result level the same way and leaves the URL out of each job; `github.workflow-jobs.get` reads one job
with its compact steps and its URL.

## Logs and artifacts

A job log may carry anything a workflow printed and is treated as sensitive, untrusted data. Only
`github.workflow-jobs.log` reads it; no other answer embeds a log. The tool returns the last lines of at most the last
`max_bytes` bytes and `truncated` when the log holds more. Invalid UTF-8, terminal escape sequences, and control characters other than tab and line
feed are removed, so no binary content reaches the answer. The log passes through memory into the answer
only: Qatlas never writes it to disk, and audit events never carry it.

GitHub answers the log route with a redirect to a short-lived signed address, usually on another host.
Qatlas follows exactly that one https redirect without the token and without any other GitHub header, asks
for the last `max_bytes` bytes only, and follows no further redirect. A storage that ignores the range is
read up to 8 MiB, of which only the end is kept; a longer log is then refused.

Artifacts are zip archives. A bounded download would still be binary and would need unpacking or storing,
so Qatlas reads only their metadata. Run logs, which
GitHub serves as a zip archive as well, are read per job instead.

## Billable time

`github.workflow-runs.usage` reads the billable time of one run in the chosen repository for each GitHub-hosted
runner operating system GitHub reports. Re-runs are included; the macOS and Windows multiplier is not applied
and nothing is rounded to whole minutes. Billable time only
exists for private repositories on GitHub-hosted runners. At most 500 `job_runs` are answered for each
operating system, and `truncated` is true when more were cut. GitHub has announced that this endpoint is being
closed down; a refusal then comes back as GitHub answers it.

The download of artifacts and of the log archive of a run, and their download links, are deliberately not
offered: a signed link acts like a short-lived credential.

## Deleting run logs

`github.workflow-run-logs.delete` deletes all logs of one run in the chosen repository and cannot be undone. It is listed-only: a connection offers it only while its `tools` list names it, and it needs
confirmation (`--confirm`). No profile selects it. Qatlas reads the run first,
so a missing run is answered not found. GitHub
documents only the success answer `204`; when GitHub answers the delete of an existing run with not found,
Qatlas reads the logs as already deleted and answers `deleted: true` with `already_deleted: true`. The delete
is sent once and never repeated.

## Executions

Every execution needs the tool offered by the connection, by `execute` in its `effects` or by name in its
`tools` list, and confirmation in its own invoke request (`--confirm`, or `confirm: true` over MCP). A missing
confirmation ends the request before a secret is read and before GitHub is contacted. An
execution is sent exactly once; an unclear outcome (a timeout, a dropped connection, a server error) says
that the execution may have happened, is never repeated by Qatlas, and must be checked with
`github.workflow-runs.list` or `github.workflow-runs.get` before a repetition. After each execution the next
request of the same token waits one second.

`github.workflows.dispatch` takes `inputs` as a flat map of input names to string values; write a boolean as
`"true"` or `"false"`. Before it dispatches, Qatlas reads the workflow and its file at `ref` and checks the inputs against the
`on.workflow_dispatch.inputs` the file declares: an undeclared input, a missing required input without a
default, a `boolean` other than `true` or `false`, a `number` that is no number, and a `choice` outside its
options are refused without a dispatch. A workflow without the `workflow_dispatch` trigger at `ref`, an
inactive workflow, and a workflow without a file are refused as well. GitHub validates the dispatch again.
GitHub answers a dispatch without the new run; list the runs with `event: workflow_dispatch` and the branch
to follow it.

The re-runs and the cancel read the run first. A re-run needs a completed run, so GitHub's refusal never
reads like a missing permission.

The cancel is idempotent. For a run that has not completed it sends one cancel and answers `accepted: true`;
the run then ends asynchronously. For a run that has already completed, cancelled or not,
it sends nothing and answers `accepted: false` with the `status` and `conclusion` it found. When the run
completes between the read and the cancel, GitHub refuses the cancel; Qatlas reads the run again and answers
the same way.

## Tokens for Actions

GitHub decides with every request. A refused Actions request names what such a request needs; it never
claims what the configured token holds.

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| observer tools | `repo` for a private repository; a public repository needs no scope | Actions: read |
| `github.workflows.dispatch` | `repo` | Actions: read and write, and Contents: read for the workflow file |
| re-runs and cancel | `repo` | Actions: read and write |
| `github.workflow-runs.usage` | `repo` for a private repository | Actions: read |
| `github.workflow-run-logs.delete` | `repo` | Actions: write |

A repository or organization policy may forbid an execution although the token would allow it. A classic
token needs the `workflow` scope only to change workflow files, which only the listed-only
`github.workflow-files.create` and `github.workflow-files.update` do.

## Workflow maintenance and Actions administration

Two more groups of repository tools change what runs in a repository and with which rights: the workflow
maintainer writes workflow files, which decide what code runs with the repository's secrets, and the Actions
administrator decides whether Actions run at all, which actions they may use, what the `GITHUB_TOKEN` of
every run may do, and how the repository accepts merges (the [merge and branch settings](repositories.md#repository-merge-and-branch-settings)). They are high risk,
and Qatlas keeps them behind a boundary of their own:

- **Listed only.** A connection offers such a tool only when its `tools` list names it. A connection that
  selects by `effects` never offers one: a planning connection with `create` and `update`, an operator with
  `execute`, or a connection with every effect neither discovers nor runs them. `qatlas tools`, `qatlas describe`, `qatlas.search`,
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

| Tool | Does |
| --- | --- |
| `github.workflow-files.list` | lists the workflow files, without content |
| `github.workflow-files.get` | reads one workflow file with its content and blob SHA |
| `github.workflow-files.create` | commits one new workflow file; fails when the file exists |
| `github.workflow-files.update` | replaces one workflow file while it still has the given blob SHA |
| `github.workflows.enable` | enables one workflow |
| `github.workflows.disable` | disables one workflow; a disabled workflow stays as it is |
| `github.actions-permissions.get` | reads whether Actions run and which actions they may use |
| `github.actions-permissions.update` | changes whether Actions run and which actions they may use |
| `github.workflow-permissions.get` | reads the default `GITHUB_TOKEN` permissions and whether Actions may approve pull request reviews |
| `github.workflow-permissions.update` | changes either of them |

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

`github.workflow-files.update` needs `sha`, the blob SHA `github.workflow-files.get` or
`github.workflow-files.list` reported. GitHub writes the file only while it still has that blob; otherwise
nothing is written, and the refusal says so: read the file again and apply the change to its current
content. `github.workflow-files.create` takes no `sha` and never replaces an existing file.

Qatlas does not generate, repair, or interpret workflows; GitHub decides whether a file is a valid workflow.
`github.workflow-files.get` refuses a file larger than 512 KiB or one that is not text, and returns the content
as untrusted data only on that explicit read.

A change answers with metadata only. It never echoes the content or the commit message, and the audit event of
a change names only the request, tool, connection, confirmation, result, and time.

`github.workflows.enable` and `github.workflows.disable` accept only a workflow whose file lies below
`.github/workflows/`. Both are idempotent: enabling an active workflow leaves it `active`. A workflow already in
a disabled state (`disabled_manually`, `disabled_inactivity`, or `disabled_fork`) is not changed by a disable,
which sends nothing and answers that state as `previous_state` and `state`.

### Actions settings

`allowed_actions` set to `selected` keeps the selection of allowed actions maintained in the repository settings,
which Qatlas does not change: that pattern list is a separate, wider surface. At least one value is required, and
a value left out stays as it is: GitHub replaces a setting as a whole, so Qatlas reads it first and sends it with
the requested values in one change. `allowed_actions` applies only while Actions are enabled and is refused for
disabled Actions unless `enabled: true` comes with it. An organization or enterprise policy may fix a value; GitHub's refusal is reported, and nothing is changed or
retried.

### Credential

Give these connections a credential of their own, used by no other connection, ideally a fine-grained token
limited to the one repository with only the permissions of the tools the connection lists. GitHub decides on
every request and documents the requirements per endpoint; a refusal names what the request needs and never
claims what the token holds. A branch protection or repository rule may refuse a file change although the
token would allow it.

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.workflow-files.list`, `get` | `repo` for a private repository | Contents: read |
| `github.workflow-files.create`, `update` | `repo` and `workflow` | Contents: read and write, and Workflows: read and write |
| `github.workflows.enable`, `disable` | `repo` | Actions: read and write |
| `github.actions-permissions.get`, `github.workflow-permissions.get` | `repo`, as a repository administrator | Administration: read |
| `github.actions-permissions.update`, `github.workflow-permissions.update` | `repo`, as a repository administrator | Administration: read and write |

```yaml
connections:
  ci-maintainer:
    service: github
    credential: github-maintainer
    target: repos/octo-org/example
    tools: [github.workflows.list, github.workflows.get, github.workflow-files.list, github.workflow-files.get,
      github.workflow-files.create, github.workflow-files.update, github.workflows.enable, github.workflows.disable]
  ci-admin:
    service: github
    credential: github-maintainer
    target: repos/octo-org/example
    tools: [github.actions-permissions.get, github.actions-permissions.update, github.workflow-permissions.get,
      github.workflow-permissions.update, github.repository-settings.get, github.repository-settings.update]
```
