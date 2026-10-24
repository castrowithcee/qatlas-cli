---
description: >
  Describes the Actions tools of the Gitea and Forgejo providers: reading workflow runs, jobs, tasks, workflows,
  artifacts, and job logs, downloading the log archive of a run and the archive of an artifact, cancelling,
  re-running, approving, and deleting runs, dispatching a workflow, deleting artifacts, the two Actions setup
  profiles, their scopes, the versions that have each operation, how runs, jobs, and artifacts are bound to the
  targets, the owner level only Gitea has, and the limits.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-24
---

# Actions

The Actions tools observe and operate the workflow runs, jobs, artifacts, and workflows of one repository inside the
connection's targets. They hold for Gitea and Forgejo alike unless a line names one product. Secrets and variables are
described in [Actions administration](actions-admin.md); runners are not offered.

## Scopes

The tools presumably need `read:repository` to read and `write:repository` to cancel, re-run, approve, dispatch, and delete,
since the API serves Actions in the repository route group. None was checked against a live instance; a missing
scope shows as `not-found`.

## Versions

Forgejo added its Actions API in steps; an instance below the version of an operation is refused before the
request, naming the version it needs.

| Tool | Forgejo | Gitea |
| --- | --- | --- |
| `workflow-runs.list`, `workflow-runs.get`, `actions-tasks.list` | 15 | 1.27 |
| `workflow-runs.delete`, `workflow-jobs.list`, `workflow-job-logs.get` | 16 | 1.27 |
| `workflow-runs.cancel` | 16 | 28 |
| `workflow-run-logs.get` | 16 | 28 |
| `workflow-runs.rerun`, `workflow-jobs.get`, `workflow-jobs.rerun` | 17 | 1.27 |
| `workflow-runs.force-cancel`, `workflow-runs.approve` | none | 28 |
| `workflow-runs.rerun-failed`, `workflow-run-attempts.get`, `workflow-run-attempts.list-jobs` | none | 1.27 |
| `workflow-artifacts.list`, `.get`, `.download`, `.delete` | 16 | 1.27 |
| `workflows.dispatch` | 15 | 1.27 |
| `workflows.list`, `workflows.get` | none | 1.27 |

The `step`, `q`, `qi`, and `format` arguments of `workflow-job-logs.get` need Forgejo 17; `attempt` works from 16.
`GET /actions/run`, which answers for a Forgejo job token only, is not offered.

## Targets and binding

Every tool takes `repository` (`OWNER/REPO`), checked against the targets before the credential is resolved and before
any request; it may be left out only when the targets name exactly one repository. A run, job, or artifact ID travels
in a fixed path below the repository, which binds it to that repository. An answer must carry the ID asked for, and a
run, job, or artifact that names its repository must name one the targets allow; a violation is refused as `invalid-provider-response`
without naming the repository. A list drops the rows that fail the check. A change sends one request and reads
nothing first.

### Owner level (Gitea)

`workflow-runs.list` and `workflow-jobs.list` also list for an organization (`owner` as `orgs/NAME`) or for the user
behind the token (`users/NAME`) instead of one repository; pass either `repository` or `owner`. The connection must
name the owner as an owner target of the same kind: a repository target does not allow it, `users/NAME` does not
allow `orgs/NAME`, and a connection without targets reaches it. The rows are checked against the targets by their
repository, and a row of another owner is dropped. The user level is read through `/user/actions`, which acts on the
account behind the token, so Qatlas first reads `GET /user` and refuses another user in `owner`.
`exclude_pull_requests`, `workflow_id`, and `run_id` work on a repository only, `sort` and `order` not on an
organization. Forgejo has no owner level; `owner` is refused as `invalid-request` before the secret.

## Reading

- `workflow-runs.list` reads one batch of runs with the filters each product documents; on Gitea `workflow_id` (a
  workflow file such as `build.yml`) reads the per-workflow path. A filter of the other product is refused as
  `invalid-request`.
- `workflow-jobs.list` answers all jobs of a run at once on Forgejo and needs `run_id`; Gitea pages through the jobs
  of a run, a repository, an organization, or the user and filters by `status`.
- `workflow-run-attempts.get` and `workflow-run-attempts.list-jobs` read an attempt of a run on Gitea.
- `actions-tasks.list` reads the tasks of the repository; Forgejo filters by `status`. The rows are the
  `workflow_runs` of the API, answered as `tasks`.
- `workflow-artifacts.list` reads the artifact metadata of the repository or, with `run_id`, of one run, and filters
  by `name`. Forgejo answers an array and pages it; Gitea wraps the list with a total, documents no paging, and
  refuses `limit` and `cursor`. `run_id` is filled on both products, on Gitea from `workflow_run`.
  `workflow-artifacts.get` reads one artifact and never its content.
- `workflows.list` and `workflows.get` read the workflow files of the repository on Gitea; the identifier is the file
  name. Forgejo has no read API for workflows.
- Runs carry the members both products have under common names next to the members each product documents. Titles,
  branches, names, and steps are untrusted data. The webhook payload of a Forgejo run (`event_payload`) is not
  shown, since it holds the pushed commits and the inputs of a dispatch.

## Logs

A job log may carry whatever a workflow printed and is sensitive, untrusted text. `workflow-job-logs.get` returns
the end of the log and `truncated`; a log above the size limit is refused. Terminal sequences and control characters
are removed and every secret the process has resolved, such as the connection's token, is masked before the end is
cut. A secret a workflow printed that Qatlas does not know cannot be masked, so the log stays classified as
sensitive. The log is never stored.

`workflow-run-logs.get` streams the ZIP archive of all job logs to a file in a directory released under
`files.write`, up to a size limit. The path is checked before the credential is resolved, an existing file is
replaced only with confirmation, a failed or refused download leaves no file, and a redirect to another address is
not followed. The archive is neither unpacked nor masked.

`workflow-artifacts.download` streams the ZIP archive of one artifact the same way. The API documents a redirect to
the blob download for Gitea, which answers with a redirect to a signed address of the instance itself. Qatlas follows
that one redirect only when it keeps the instance's origin, names the same repository and artifact below
`/zip/raw`, and carries `sig` and `expires` alone; the second request carries no token and may not redirect again.
Any other redirect, such as to external storage, is refused, and Forgejo's is never followed. The artifact is not
read first, so only the size limit and the instance's refusal
bound the transfer. The content is untrusted and may hold secrets.

## Operating

`workflow-runs.cancel`, `workflow-runs.force-cancel`, `workflow-runs.rerun`, `workflow-runs.rerun-failed`,
`workflow-runs.approve`, `workflow-jobs.rerun`, and `workflows.dispatch` have the effect `execute` and need confirmation. Each sends one
request and never repeats it; when the outcome is open, the message says so. A re-run starts another attempt and is not
idempotent. `workflow-runs.approve` lets a run that waits for approval, such as one from a fork, run with the
repository's secrets. `workflow-jobs.rerun` names the job on Forgejo and the run and the job on Gitea. Gitea answers
the run or job, which the tool returns beside `accepted`.

`workflows.dispatch` starts a `workflow_dispatch` run of a workflow file on a branch or tag. `workflow` is a file name
without a path, `ref` a Git name, and `inputs` an object of strings, so a boolean is written as `"true"`; the bounds are
in the input schema. It is not idempotent: each call starts a run. `return_run`, true when left out, asks the instance
for the started run, which the tool returns as `run` with a `run_id` on both products; Forgejo sets `return_run_info`
in the body, Gitea `return_run_details` in the query. An instance that answers without a body is not an error, and
`run` is then absent.

`workflow-runs.delete` and `workflow-artifacts.delete` remove a run with its jobs and logs, or an artifact, for good.
They are offered only by a connection whose `tools` list names them and are in no setup profile.

## Setup profiles

`actions-observer` selects every reading Actions tool of the product, the log and artifact downloads included, and
changes nothing on the instance. `actions-operator` adds the execute tools the product has: cancel, force-cancel,
re-run, approve, the job re-run, and dispatch. Neither is recommended.

## Tools

| Tool | Effect | Provider |
| --- | --- | --- |
| `workflow-runs.list`, `workflow-runs.get`, `workflow-jobs.list`, `workflow-jobs.get`, `workflow-job-logs.get`, `workflow-run-logs.get`, `actions-tasks.list`, `workflow-artifacts.list`, `workflow-artifacts.get`, `workflow-artifacts.download` | read | both |
| `workflow-run-attempts.get`, `workflow-run-attempts.list-jobs`, `workflows.list`, `workflows.get` | read | Gitea |
| `workflow-runs.cancel`, `workflow-runs.rerun`, `workflow-jobs.rerun`, `workflows.dispatch` | execute | both |
| `workflow-runs.force-cancel`, `workflow-runs.rerun-failed`, `workflow-runs.approve` | execute | Gitea |
| `workflow-runs.delete`, `workflow-artifacts.delete` | delete, listed only | both |
