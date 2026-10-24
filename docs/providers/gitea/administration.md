---
description: >
  Describes the instance administration tools of the Gitea and Forgejo providers: the runners of the whole
  instance, the jobs waiting for a runner, the instance-wide lists of workflow runs, workflow jobs, and packages,
  the binding to connections without targets, the scope, the listed-only deletion of a runner, and the product
  differences.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-24
---

# Instance administration

These tools act as an instance administrator on data of the whole instance, not of an owner or repository. They
hold for Gitea and Forgejo alike unless a line names one product.

| Tool | Effect | Provider |
| --- | --- | --- |
| `admin-runners.list`, `admin-runners.get` | read | both |
| `admin-runner-jobs.search` | read | Forgejo |
| `admin-workflow-runs.list`, `admin-workflow-jobs.list` | read | Gitea |
| `admin-packages.list` | read, Gitea 28 and later | Gitea |
| `admin-runners.update` | update | Gitea |
| `admin-runners.delete` | delete, listed only | both |

## Scope and binding

The routes live below `/admin`, so the token needs the `read:admin` scope for a read and the `write:admin` scope
for a change, and its user must be an instance administrator. None was checked against a live instance; a token
without the right is refused by the instance, and a missing scope or right shows as `not-found`.

Every tool is instance-wide and therefore reachable only by a connection without targets. A connection with
targets refuses the call as `invalid-request` before the credential is resolved and before any request; the
refusal does not name a target. All arguments are fixed-path filters; no argument chooses a path, URL, header, or
body.

## Runners

`admin-runners.list` and `admin-runners.get` read the runners of the whole instance, whatever level owns them, with
every member the product answers; names, descriptions, and labels are untrusted data. Forgejo pages the list and
filters it with `visible`; Gitea answers it at once, cut at 100 runners (`has_more` then is true), and filters it
with `disabled`. `admin-runners.get` refuses an answer for another runner as `invalid-provider-response`. The
runners of a repository, an organization, or the user behind the token are described in
[Actions administration](actions-admin.md).

`admin-runners.update` exists on Gitea only and sends `disabled` alone: it pauses a runner or resumes it, and
nothing else of the runner changes. `admin-runners.delete` removes the registration of a runner for good. It is
offered only by a connection whose `tools` list names it and is in no setup profile. Registering a runner and
registration tokens are not offered.

`admin-runner-jobs.search` exists on Forgejo only and lists the jobs of the instance that wait for a runner whose
labels match `labels`, at most 100 at once and without paging.

## Workflow runs, workflow jobs, and packages

`admin-workflow-runs.list` and `admin-workflow-jobs.list` exist on Gitea only and list the runs and jobs of all
repositories, one batch at a time, with the filters the descriptor names. Titles, branches, job names, and labels
are untrusted data. Forgejo lists runs and jobs of a repository only (see [Actions](actions.md)).

`admin-packages.list` exists on Gitea only and lists the packages of all owners, filtered by `type` and by name.
Names and versions are untrusted data; an address with user info or a query is left out. Gitea 1.27 has no such
route, so the tool is refused there before its request and names the version it needs.

## Changes

`admin-runners.update` and `admin-runners.delete` need confirmation, send exactly one request, and are never
repeated; when the outcome is open, the message says that the change may have been applied. No tool of this group
is in a setup profile.
