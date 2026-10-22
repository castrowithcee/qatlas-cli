---
description: >
  Describes the n8n provider as a whole: Public API key setup, the project and workflow allow-lists and their
  live project-membership check, target choosing, the rules every tool group shares (profiles, tool-list-only
  tools), version and plan boundaries, error classes, untrusted data, and what the provider does not do.
  Tool groups have their own pages.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-22
---

# n8n

n8n is a provider for the n8n Public API (n8n Cloud or self-hosted, `/api/v1`). Its tools are grouped, each
group on its own page; this page holds what applies to all of them.

| Page | Covers |
| --- | --- |
| [workflows.md](workflows.md) | workflows and executions, the retry contract of every change, pagination, failed-execution errors |
| [projects.md](projects.md) | projects and project members |
| [data-tables.md](data-tables.md) | data tables, their columns and rows |
| [variables-and-tags.md](variables-and-tags.md) | variables and instance-wide tags |
| [users-and-audit.md](users-and-audit.md) | instance users and the security audit |
| [source-control.md](source-control.md) | source control preview, pull, and push |
| [credentials.md](credentials.md) | credential metadata, tests, schemas, and changes with secrets by reference |

## Configuration

`base_url` is the instance's own origin: `https://NAME.app.n8n.cloud` for n8n Cloud, or the self-hosted
origin, optionally below an installation path (n8n's own `N8N_PATH` setting). It must be `https`, without
user, query, or fragment; Qatlas refuses any other URL before a secret is read. Unlike this codebase's
generic configuration validator, which leaves `http` open so a local test server can be configured, this
provider requires `https` unconditionally, the same rule GitHub Enterprise Server, Nextcloud, SeaTable, and
Twenty apply to their own self-hosted origin: n8n documents no local-only `http` exception of its own.

The credential provides `api-key`, a Public API key created under Settings, n8n API, Create an API key. On an
Enterprise plan that key may itself be scoped to a subset of resources and actions; that scope is a further
ceiling this connection's own targets and effects narrow, never widen. The n8n Public API is not
available on a free trial; upgrading to a paid plan is a prerequisite this provider cannot detect ahead of a
failed request.

```yaml
services:
  n8n-customer-a:
    provider: n8n
    base_url: https://customer-a.app.n8n.cloud

credentials:
  n8n-customer-a-reader:
    provider: n8n
    type: keyring
```

## Scope

A connection binds one instance, through its base URL and API key, and, independently, two optional
allow-lists:

| Target | Binds |
| --- | --- |
| `project/PROJECT_ID` | one project (n8n's Enterprise Projects feature) of the bound instance; optional, repeatable |
| `workflow/WORKFLOW_ID` | one workflow of the bound instance; optional, repeatable |

```yaml
connections:
  customer-a-team-x:
    service: n8n-customer-a
    credential: n8n-customer-a-reader
    targets: [project/VmwOO9HeTEj20kxM]
```

Neither allow-list is required. Named connections already separate customer instances by `base_url` and API
key; unlike the Infomaniak kDrive and kChat providers, one n8n API key cannot reach a second instance. The
allow-lists exist for the narrower case the same instance still spans more than one connection should show:
n8n's Projects feature can hold several customers' or teams' workflows under one instance and one
administrative key, which is exactly the situation a service provider holding several customers' credentials
needs to keep apart even when, for once, they share one n8n installation.

A `workflow_id` argument outside a configured workflow allow-list is refused locally, as an invalid request,
before any request is sent. A configured project allow-list is checked live, against the instance's own
answer, never against local configuration alone:

- `n8n.workflows.list` reads a workflow's project membership from the same `shared` array n8n's response to
  the list itself already carries (`role`, `projectId`, `project`, part of `workflowPublicDto`), so filtering
  the list to the allowed projects costs no extra request.
- `n8n.workflows.get` re-checks the same field of the one workflow it just read.
- `n8n.executions.get`'s Execution resource carries only a `workflowId`, no project field of its own (per the
  Public API's `getExecution`/`getExecutions` schemas), so it fetches that workflow once more, in one extra
  request, before any execution content is returned.
- `n8n.executions.list` applies the same reasoning at the list level: since a listed execution carries no
  project either, a project-restricted connection can only verify one workflow at a time; it therefore
  requires an explicit `workflow_id` argument, checks that workflow's project once for the whole page, and
  refuses the call outright when `workflow_id` is missing, rather than showing every execution of the
  instance's other projects unchecked.

An instance or Public API version that never sends `shared` at all, an older release, or a non-Enterprise
instance without the Projects feature, cannot prove a workflow's project. A project-restricted connection
then fails the check closed instead of guessing, and the refusal says so.

`workflows.create` applies the same boundary to a workflow that does not exist yet, see
[workflows.md](workflows.md#workflowscreate-under-a-project-or-workflow-restriction).

## Choosing targets

A person chooses the targets of a connection level by level: the projects of the instance, then the workflows of
a project. Projects page by n8n's own cursor; workflows are listed with the server filters `projectId` and
`name`, so they can be searched at n8n. The connection's own targets neither filter nor narrow what is offered.
Choosing only reads, through no tool and for no agent. Names are untrusted data and are shortened.

A workflow is offered only below a project. On an instance without the Projects feature no project can be
listed, so such a workflow is chosen by its ID or by a pasted URL. The chosen targets keep their meaning:
`project` and `workflow` stay independent allow-lists.

A URL of the instance's own editor, below its base URL including a path prefix, names a target without a
request: `/workflow/WORKFLOW_ID` a workflow, `/projects/PROJECT_ID/...` a project. A URL of another origin
names nothing.

Which tools a binding reaches follows what the call refuses. Any project or workflow target reaches no
instance-wide tool (`n8n.tags.*`, `n8n.users.*`, `n8n.audit.generate`, `n8n.source-control.*`) and no
`n8n.projects.create`; no target form reaches them. A workflow target reaches no credential, variable, data
table, column, row or project-member tool, no `n8n.projects.update` or `.delete` and no `n8n.workflows.create`;
a project target alone reaches them.

## Profiles and tool lists

Each tool group has its own profiles, listed on the group's page, so a group can be released without
widening another. The per-call confirmation every changing tool needs is separate from, and required in
addition to, a connection's own setup profile.

A tool that no profile contains carries `requires_tool_allow_list`: a connection offers it only when its
`tools` list names it explicitly, in addition to its effect and the per-call confirmation. Each group page
names its such tools.

## Version and plan boundaries

`workflows.activate` and `workflows.deactivate` use n8n's own `POST /workflows/{id}/activate` and
`POST /workflows/{id}/deactivate` endpoints. n8n's own Public API spec marks both **deprecated** in favour
of `POST /workflows/{id}/publish` and `/unpublish`, a newer, versioned publishing model; this provider
intentionally keeps using the deprecated pair, since publish/unpublish and the version history behind them
are out of this milestone's scope. `activateWorkflow` additionally accepts an optional `versionId`, `name`,
and `description` to activate a specific saved version; this provider does not offer them and always
activates the latest version, the same effect as omitting them.

The Projects feature, and therefore a project allow-list's live membership check, needs an Enterprise plan;
see "Scope" above for what happens when an instance or Public API version never reports it.

`n8n.workflows.get` never returns a credential's value: n8n's own Public API does not put one in a workflow
response either, only a referenced credential's `id` and `name` per node, which is what a person editing the
workflow in the n8n editor already sees, and what this provider passes on unmodified. A node's `parameters`
are the workflow's own content and are passed through as-is, bounded only by the overall response size limit.
`staticData` and `pinData`, which can hold accumulated runtime state or pinned example payloads with real
data, are deliberately not read in this milestone.

## Errors

Errors keep stable classes and never carry the API key or a raw provider response body:

| Class | Cause |
| --- | --- |
| `auth` | n8n rejected the API key |
| `permission` | this API key may not perform the operation; check its scopes under Settings, n8n API. For a project tool: the Projects license, the API key's project scope, or the owner's role |
| `not-found` | n8n does not hold the resource, does not show it to this key, or this instance's Public API version does not have the endpoint |
| `rate-limited` | n8n rate-limited the request; n8n documents no fixed budget of its own, so Qatlas applies no proactive spacing and instead holds its own limiter for whatever `Retry-After` n8n names |
| `timeout` | n8n did not answer in time |
| `unreachable` | n8n is unavailable, in maintenance, or could not be reached |
| `invalid-provider-response` | the answer was unreadable, too large, or named a different resource than the one requested |
| `provider-error` | every other rejection, including a redirect on an endpoint that must not answer with one, and a create or update n8n placed outside this connection's allowed projects after its one changing request already reached n8n |

A `workflow_id` or `project_id` outside the connection's allow-list, and a workflow the live project check
finds outside an allowed project (including one whose instance or Public API version reports no project at
all while the connection restricts by project), are invalid requests, never provider errors, so a scope
refusal is never mistaken for a missing workflow. The one exception is a `workflows.create` or
`workflows.update` whose one changing request has already reached n8n before the mismatch is found; see
[workflows.md](workflows.md#workflowscreate-under-a-project-or-workflow-restriction).

How a changing request that may have reached n8n is reported is the retry contract in
[workflows.md](workflows.md#changes-and-their-retry-contract), valid for every group.

## Untrusted data

Workflow and project names, tag names, node parameters, and every other value a listing or a read answers
with come from the instance and are untrusted data. Qatlas normalises them into a stable envelope and never
renders them, follows a link inside them, or executes anything derived from them. A create or an update
carries a caller's own node parameters, connections, and settings back to n8n unmodified, but never
interprets or executes any of it itself.

## Boundary

This provider reads, creates, and replaces workflows, and activates, deactivates, retries, and stops them
and their executions, and lists, creates, renames, and deletes projects, and manages their members, and lists, reads, creates,
renames, and deletes data tables and manages their columns and rows, and lists, creates, updates, and deletes
variables, and lists, reads, creates, renames, and deletes tags and lists, reads, re-roles, and deletes users on connections without targets, and lists, reads, and tests
credentials as metadata, generates the security audit, previews, pulls, and pushes source control changes on connections without targets, reads credential type schemas, and creates, updates, moves between projects, and deletes credentials, secret values only by reference.

It does not, and has no tool to, start a workflow (the Public API documents no endpoint for it:
`POST /workflows/{id}/activate` only flips the `active` flag, and running a workflow on demand is an editor
and webhook/trigger action), delete a workflow or an execution, stop many executions at once, archive,
unarchive, publish, unpublish, transfer, or test-run a workflow, move
a project's content elsewhere before deleting it, manage folders, manage roles or passwords, MFA, or single sign-on of users, accept any credential value as an argument, change a credential's type, update a credential's data in part (an update with data replaces all of it),
move a credential to a project outside the allow-list, set up or change the source control connection, manage environments or promotions, request audit parameters beyond the five categories, or manage tags on workflows, or clear all rows of a data table; those are deliberately out of
scope.
