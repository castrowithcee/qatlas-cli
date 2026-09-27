---
description: >
  Describes the n8n provider: Public API key setup, the project and workflow allow-lists and their live
  project-membership check, the workflow and execution reads, pagination and cursor contracts, the bounded
  failed-execution error, and the version and plan boundaries of n8n's Projects feature.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-09-27
---

# n8n

n8n is a read-only provider for the n8n Public API (n8n Cloud or self-hosted, `/api/v1`). It lists and reads
workflows, and lists and reads executions, including a bounded view of a failed execution's error. It
creates, changes, activates, deactivates, runs, retries, stops, or deletes nothing; those, and credential,
user, project, tag, variable, and data table management, are a later milestone.

## Configuration

`base_url` is the instance's own origin: `https://NAME.app.n8n.cloud` for n8n Cloud, or the self-hosted
origin, optionally below an installation path (n8n's own `N8N_PATH` setting). It must be `https`, without
user, query, or fragment; Qatlas refuses any other URL before a secret is read. Unlike this codebase's
generic configuration validator, which leaves `http` open so a local test server can be configured, this
provider requires `https` unconditionally, the same rule GitHub Enterprise Server, Nextcloud, SeaTable, and
Twenty apply to their own self-hosted origin: n8n documents no local-only `http` exception of its own.

The credential provides `api-key`, a Public API key created under Settings, n8n API, Create an API key. On an
Enterprise plan that key may itself be scoped to a subset of resources and actions; that scope is a further
ceiling this connection's own targets and permissions narrow, never widen. The n8n Public API is not
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

## Tools

| Tool | Reads |
| --- | --- |
| `n8n.workflows.list` | the workflows of the bound instance, filtered to the allow-lists, page by page |
| `n8n.workflows.get` | one workflow, including its nodes, their connections, and credential references (id/name only) |
| `n8n.executions.list` | the executions of the bound instance, filtered to the allow-lists, page by page |
| `n8n.executions.get` | one execution's status, timestamps, and, for a failed one, a bounded error |

All four are `read`, safe, and need no confirmation. The terminal editor starts a new connection on the setup
profile `read`, which ticks `[read]` and every tool above; it is the only profile this provider offers.

`n8n.workflows.get` never returns a credential's value: n8n's own Public API does not put one in a workflow
response either, only a referenced credential's `id` and `name` per node, which is what a person editing the
workflow in the n8n editor already sees, and what this provider passes on unmodified. A node's `parameters`
are the workflow's own content and are passed through as-is, bounded only by the overall response size limit.
`staticData` and `pinData`, which can hold accumulated runtime state or pinned example payloads with real
data, are deliberately not read in this milestone.

## Pagination and cursors

Both list tools take `limit` (1 to 250, default 100, matching n8n's own default) and an opaque `cursor`, and
answer `has_more` and a `cursor` for the next page whenever n8n's own `nextCursor` was not empty. Each call
reads exactly one n8n page; Qatlas never follows `has_more` on its own. The cursor is n8n's own opaque value,
passed back unchanged.

`n8n.workflows.list` also accepts `active`, `name`, `tags`, and `project_id`, forwarded to n8n's own filters
of the same kind (`project_id` must be inside this connection's project allow-list when it has one).
`n8n.executions.list` accepts `workflow_id` and `status` the same way. Every returned page is still
defensively re-filtered against this connection's own allow-lists after n8n answers, whether or not a
matching filter argument was given.

## Executions and their errors

`n8n.executions.get` reads the base execution (`includeData=false`): its status, mode, timestamps, and retry
references, never its run data. Only when that status is `error` or `crashed` does it send one further,
bounded request with `includeData=true`, solely to read `data.resultData.error` (the message and the node it
names) and nothing else from that answer; every node's actual input or output, and the workflow snapshot n8n
attaches to an execution (`workflowData`), are read only as far as necessary to reach that one field and are
never part of the result. That second request is capped at 16 MiB; an instance that answers larger than that
is refused rather than parsed partially, and the execution's base status and timestamps are still returned,
without an `error` field, since the base read already succeeded on its own. n8n's `ignoreDataSizeLimit` and
`redactExecutionData` options are not used by this provider.

## Errors

Errors keep stable classes and never carry the API key or a raw provider response body:

| Class | Cause |
| --- | --- |
| `auth` | n8n rejected the API key |
| `permission` | this API key may not perform the operation; check its scopes under Settings, n8n API |
| `not-found` | n8n does not hold the resource, does not show it to this key, or this instance's Public API version does not have the endpoint |
| `rate-limited` | n8n rate-limited the request; n8n documents no fixed budget of its own, so Qatlas applies no proactive spacing and instead holds its own limiter for whatever `Retry-After` n8n names |
| `timeout` | n8n did not answer in time |
| `unreachable` | n8n is unavailable, in maintenance, or could not be reached |
| `invalid-provider-response` | the answer was unreadable, too large, or named a different resource than the one requested |
| `provider-error` | every other rejection, including a redirect on an endpoint that must not answer with one |

A `workflow_id` or `project_id` outside the connection's allow-list, and a workflow the live project check
finds outside an allowed project (including one whose instance or Public API version reports no project at
all while the connection restricts by project), are invalid requests, never provider errors, so a scope
refusal is never mistaken for a missing workflow.

## Untrusted data

Workflow and project names, tag names, node parameters, and every other value a listing or a read answers
with come from the instance and are untrusted data. Qatlas normalises them into a stable envelope and never
renders them, follows a link inside them, or executes anything derived from them.

## Boundary

This provider only reads workflows and executions. Everything that changes n8n's state, running a workflow,
retrying or stopping an execution, activating or deactivating a workflow, and every credential, user, tag,
variable, project, and data table management operation, is deliberately out of this milestone.
