---
description: >
  Describes the n8n workflow and execution tools: their profiles, the retry contract every changing tool
  follows, what a workflow update replaces and accepts, workflow creation under a project or workflow
  restriction, pagination and cursors, and the bounded error of a failed execution.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-22
---

# n8n workflows and executions

Configuration, targets, error classes, and cross-group limits are in [README.md](README.md).

## Profiles

The tools `n8n.workflows.list`, `n8n.workflows.get`, `n8n.executions.list`, and `n8n.executions.get` only
read, are safe, and need no confirmation. `n8n.workflows.create`, `.update`, `.activate`, `.deactivate` and
`n8n.executions.retry`, `.stop` each change the bound instance: every one needs its own confirmation
(`confirm: true` on the invoke request), sends exactly one changing request, and is never retried by Qatlas
itself; see below. That confirmation is required on every call regardless of the profile a connection was set
up with: the terminal editor starts a new connection on the setup profile `read`, which offers only the four
read tools; `manage` additionally offers the six change tools.

## Changes and their retry contract

Every change tool of every group needs its own confirmation and sends exactly one request that can change
n8n's state. A failure of that request that could still mean it reached n8n, a timeout, a connection reset, a
5xx, or an unreadable answer, is reported with "this change may have taken effect, read the current state
before repeating it" instead of being retried; Qatlas never repeats a changing request by itself. A 401 or 403
before that request is sent is classified `auth` or `permission` the same way every read is.

`workflows.update` is a full `PUT` replacement of `name`, `nodes`, `connections`, and `settings`, exactly as
n8n's own `updateWorkflow` endpoint is: a field left out is not kept, it is cleared. It never changes the
`active` state; `workflows.activate` and `workflows.deactivate` own that instead. When the workflow being
updated is currently active, n8n republishes the new content live unless `publish_if_active` is set to
`false`, in which case the change is saved as a draft on the still-live version; either way the `active`
flag itself is unaffected.

A node's `credentials` only ever carry the `id`/`name` reference pair n8n itself resolves and validates, the
same shape a workflow read already reports; this provider never accepts anything else of a credential.
`settings` accepts the subset of n8n's own settings object a caller can reasonably set programmatically
(`save_execution_progress`, `save_manual_executions`, `save_data_error_execution`,
`save_data_success_execution`, `execution_timeout`, `error_workflow`, `timezone`, `execution_order`,
`caller_policy`, `caller_ids`, `time_saved_mode`, `time_saved_per_execution`, `redaction_policy`,
`available_in_mcp`); it deliberately excludes `binaryMode` and `credentialResolverId`, which n8n's own spec
documents as derived, internal settings whose value is ignored on write, and `customTelemetryTags` and the
top-level `nodeGroups`, which are canvas and telemetry decoration with no execution effect. A node likewise
only accepts `id`, `name`, `type`, `type_version`, `position`, `disabled`, `parameters`, and `credentials`;
n8n's own create/update schemas additionally accept execution-behaviour fields such as `notes`, `onError`,
`retryOnFail`, `maxTries`, and `webhookId`, which this milestone does not offer. `error_workflow` is passed
through as an opaque workflow ID and is not checked against this connection's own allow-lists: n8n enforces
its own access to that workflow when the error trigger it names would actually run.

### `workflows.create` under a project or workflow restriction

`workflows.create` refuses outright, before any request, on a connection restricted by a **workflow**
allow-list: a workflow that does not exist yet can never already be on that list. On a connection restricted
by a **project** allow-list, n8n's own `createWorkflow` schema does let a caller steer the target project
through `project_id` (`projectId` on the wire), so this provider requires it explicitly, checks it against
the allow-list locally, and forwards it; a connection without a project restriction may still name any
`project_id` its API key can reach, or leave it out, which n8n then places in the API key owner's personal
project. After the one changing `POST /workflows` request, this provider re-reads the new workflow and
re-applies the project allow-list to what n8n actually reports: if n8n did not honor `project_id` (an older
Public API version, for example), the result is reported as a **provider error**, not an invalid request,
because the request has already reached n8n and this milestone offers no delete tool to remove the
misplaced workflow; the caller is told to remove it directly in n8n. `workflows.update` cannot move a
workflow between projects at all (its own schema has no `projectId` field), so no equivalent risk exists
there; the same re-check still runs defensively after its one `PUT`.

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
