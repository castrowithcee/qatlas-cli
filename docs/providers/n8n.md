---
description: >
  Describes the n8n provider: Public API key setup, the project and workflow allow-lists and their live
  project-membership check, the workflow and execution reads, pagination and cursor contracts, the bounded
  failed-execution error, the confirmed workflow and execution changes and their retry contract, and the
  data table listing, reading, creation, renaming, and deletion, column management, the
  project listing, creation, renaming, and deletion with the deletion sequence n8n runs, and the version and
  plan boundaries of n8n's Projects feature and its deprecated activate/deactivate endpoints.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-04
---

# n8n

n8n is a provider for the n8n Public API (n8n Cloud or self-hosted, `/api/v1`). It lists and reads workflows
and executions, including a bounded view of a failed execution's error, creates and replaces workflows,
activates and deactivates them, and retries and stops executions. It also lists, creates, renames, and
deletes projects, see "Projects" below, lists, reads, creates, renames, and deletes data tables and manages their columns and rows, see
"Data tables", "Data table columns", and "Data table rows" below, and lists, creates, updates, and deletes variables,
see "Variables" below, manages the instance-wide tags, see "Tags" below, lists, reads, invites, re-roles, and deletes the instance's users, owner-only, see "Users" below, and lists, reads, and tests credentials
as metadata only, reads credential type schemas, and creates, updates, moves, and deletes credentials, with
secret values only by reference to a released forward credential, see "Credentials" below. It also generates
the instance's security audit, see "Security audit" below, and previews, pulls, and pushes source control
changes, see "Source control" below, all instance-wide.

**There is no tool to start a workflow**: the Public API documents no endpoint for it. There is also no
tool to delete a workflow or an execution, and no archive, unarchive, publish, unpublish, transfer, or
test-run action, no credential value as an argument, no users or roles beyond the "Users" tools, no tags on workflows, and no way to clear all rows of a data table; those are
a later milestone.

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

`workflows.create` applies the same boundary to a workflow that does not exist yet, see below.

## Tools

| Tool | Effect | Does |
| --- | --- | --- |
| `n8n.workflows.list` | read | lists the workflows of the bound instance, filtered to the allow-lists, page by page |
| `n8n.workflows.get` | read | reads one workflow, including its nodes, their connections, and credential references (id/name only) |
| `n8n.workflows.create` | create | creates one workflow from its name, nodes, connections, and settings |
| `n8n.workflows.update` | update | replaces one workflow's name, nodes, connections, and settings with a full PUT |
| `n8n.workflows.activate` | update | activates one workflow, turning its triggers live |
| `n8n.workflows.deactivate` | update | deactivates one workflow, turning its triggers off |
| `n8n.executions.list` | read | lists the executions of the bound instance, filtered to the allow-lists, page by page |
| `n8n.executions.get` | read | reads one execution's status, timestamps, and, for a failed one, a bounded error |
| `n8n.executions.retry` | execute | retries one execution, starting a new execution from it |
| `n8n.executions.stop` | execute | stops one running or waiting execution |
| `n8n.projects.list` | read | lists the projects of the bound instance, filtered to the project allow-list, page by page |
| `n8n.projects.create` | create | creates one team project from its name |
| `n8n.projects.update` | update | renames one project |
| `n8n.projects.delete` | delete | deletes one team project and everything it owns; only offered by a tools list |
| `n8n.projectmembers.list` | read | lists the members (id, email, name, role) of one allow-listed project |
| `n8n.projectmembers.add` | update | gives one existing user a role in one allow-listed project |
| `n8n.projectmembers.setrole` | update | changes one member's role in one allow-listed project |
| `n8n.projectmembers.remove` | delete | removes one member from one allow-listed project; only offered by a tools list |
| `n8n.datatables.list` | read | lists data tables (never rows), filtered to the project allow-list, page by page |
| `n8n.datatables.get` | read | reads one data table's name, project, and column definitions |
| `n8n.datatables.create` | create | creates one data table without columns, in one project |
| `n8n.datatables.rename` | update | renames one data table |
| `n8n.datatables.delete` | delete | deletes one data table and all its rows; only offered by a tools list |
| `n8n.datacolumns.list` | read | lists the columns of one data table (never rows) |
| `n8n.datacolumns.add` | create | adds one column (name, type, optional position) to a data table |
| `n8n.datacolumns.update` | update | renames and/or moves one column; the type cannot change |
| `n8n.datacolumns.delete` | delete | deletes one column and its data in every row; only offered by a tools list |
| `n8n.datarows.list` | read | lists rows of one data table, optionally by a structured filter, page by page |
| `n8n.datarows.insert` | create | inserts 1 to 50 rows into a data table |
| `n8n.datarows.update` | update | sets column values on all rows matching a filter; only offered by a tools list |
| `n8n.datarows.upsert` | update | updates the rows matching a filter or inserts one; only offered by a tools list |
| `n8n.datarows.delete` | delete | deletes all rows matching a filter; only offered by a tools list |
| `n8n.variables.list` | read | lists variables (key, plain-text value), project-bound ones apart from global ones, page by page |
| `n8n.variables.create` | create | creates one project or global variable from key and value |
| `n8n.variables.update` | update | replaces key and value of one variable, keeping its project or global status |
| `n8n.variables.delete` | delete | deletes one variable; only offered by a tools list |
| `n8n.tags.list` | read | lists tags, page by page |
| `n8n.tags.get` | read | reads one tag by ID |
| `n8n.tags.create` | create | creates one tag from a name |
| `n8n.tags.update` | update | renames one tag |
| `n8n.tags.delete` | delete | deletes one tag; only offered by a tools list |
| `n8n.users.list` | read | lists the instance's users, page by page |
| `n8n.users.get` | read | reads one user by ID (not by email) |
| `n8n.users.invite` | create | invites 1 to 10 users by email with one instance role |
| `n8n.users.setrole` | update | sets one user's instance role |
| `n8n.users.delete` | delete | deletes one user; only offered by a tools list |
| `n8n.audit.generate` | read | generates the security audit report, capped |
| `n8n.sourcecontrol.status` | read | previews pending source control changes, `push` or `pull` |
| `n8n.sourcecontrol.pull` | execute | imports the connected Git branch; only offered by a tools list |
| `n8n.sourcecontrol.push` | execute | commits and pushes named objects; only offered by a tools list |
| `n8n.credentials.list` | read | lists credential metadata (id, name, type, times, never values), page by page |
| `n8n.credentials.get` | read | reads one credential's metadata by ID |
| `n8n.credentials.test` | execute | asks n8n to test one credential's connection; needs confirmation |
| `n8n.credentials.schema` | read | reads the field schema of one credential type, in a bounded view |
| `n8n.credentials.create` | create | creates one credential from plain fields and a `secret_ref`; only offered by a tools list |
| `n8n.credentials.update` | update | renames a credential or replaces its whole data with plain fields and a `secret_ref`; only offered by a tools list |
| `n8n.credentials.transfer` | update | moves a credential between projects of the allow-list; only offered by a tools list |
| `n8n.credentials.delete` | delete | deletes one credential; workflows that use it fail afterwards; only offered by a tools list |

**There is no tool to start a workflow.** n8n's Public API documents no endpoint for it (`POST
/workflows/{id}/activate` only flips the `active` flag; running a workflow on demand is an editor and
webhook/trigger action, not a Public API one). There is also no tool to delete a workflow or an execution,
and no `stopMany`, archive, unarchive, publish, unpublish, transfer, or test-run action.

The four read tools are `read`, safe, and need no confirmation. The six tools above them each change the
bound instance: every one of them needs its own confirmation (`confirm: true` on the invoke request), sends
exactly one changing request, and is never retried by Qatlas itself; see "Changes and their retry contract"
below. That per-call confirmation is separate from, and required in addition to, a connection's own setup:
the terminal editor starts a new connection on the setup profile `read`, which offers only the four read
tools; `manage` additionally offers the six change tools, each of which still needs its own confirmation on
every call regardless of the profile a connection was set up with.

The project tools have their own profiles, so they can be released separately from the workflow tools and
are in neither `read` nor `manage`:

| Profile | Tools |
| --- | --- |
| `projects-read` | `n8n.projects.list` |
| `projects-manage` | `n8n.projects.list`, `n8n.projects.create`, `n8n.projects.update` |

The member tools have their own profiles, in none of the others and recommended in none:

| Profile | Tools |
| --- | --- |
| `members-read` | `n8n.projectmembers.list` |
| `members-manage` | `n8n.projectmembers.list`, `n8n.projectmembers.add`, `n8n.projectmembers.setrole` |

The data table, column, row, variable, and tag tools have their own profiles, in none of the others:

| Profile | Tools |
| --- | --- |
| `datatables-read` | `n8n.datatables.list`, `n8n.datatables.get` |
| `datatables-manage` | `n8n.datatables.list`, `n8n.datatables.get`, `n8n.datatables.create`, `n8n.datatables.rename` |
| `datacolumns-read` | `n8n.datacolumns.list` |
| `datacolumns-manage` | `n8n.datacolumns.list`, `n8n.datacolumns.add`, `n8n.datacolumns.update` |
| `datarows-read` | `n8n.datarows.list` |
| `datarows-manage` | `n8n.datarows.list`, `n8n.datarows.insert` |
| `variables-read` | `n8n.variables.list` |
| `variables-manage` | `n8n.variables.list`, `n8n.variables.create`, `n8n.variables.update` |
| `tags-read` | `n8n.tags.list`, `n8n.tags.get` |
| `tags-manage` | `n8n.tags.list`, `n8n.tags.get`, `n8n.tags.create`, `n8n.tags.update` |
| `users-read` | `n8n.users.list`, `n8n.users.get` |
| `users-manage` | `n8n.users.list`, `n8n.users.get`, `n8n.users.invite`, `n8n.users.setrole` |
| `audit` | `n8n.audit.generate` |
| `sourcecontrol-read` | `n8n.sourcecontrol.status` |
| `credentials-read` | `n8n.credentials.list`, `n8n.credentials.get`, `n8n.credentials.schema` |
| `credentials-test` | `n8n.credentials.test` |

`n8n.projectmembers.remove`, `n8n.projects.delete`, `n8n.datatables.delete`, `n8n.datacolumns.delete`, `n8n.datarows.update`, `n8n.datarows.upsert`,
`n8n.datarows.delete`, `n8n.variables.delete`, `n8n.tags.delete`, `n8n.users.delete`, `n8n.credentials.create`,
`n8n.credentials.update`, `n8n.credentials.transfer`, `n8n.credentials.delete`, `n8n.sourcecontrol.pull`,
and `n8n.sourcecontrol.push` are in no profile. Each
carries `requires_tool_allow_list`: a connection offers it only when its `tools` list names it explicitly,
in addition to the `delete` permission and the per-call confirmation.

## Changes and their retry contract

Every change tool needs its own confirmation and sends exactly one request that can change n8n's state. A
failure of that request that could still mean it reached n8n, a timeout, a connection reset, a 5xx, or an
unreadable answer, is reported with "this change may have taken effect, read the current state before
repeating it" instead of being retried; Qatlas never repeats a changing request by itself. A 401 or 403
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

## Projects

The four tools call `GET /projects`, `POST /projects`, `PUT /projects/{id}`, and `DELETE /projects/{id}`, and
nothing else; folders are not offered. Members are covered under "Project members" below. The routes and
their scopes (`project:list`, `project:create`, `project:update`, `project:delete`) are those of
`packages/cli/src/public-api/v1/openapi.decorator-routes.generated.yml` and
`packages/cli/src/public-api/v1/controllers/projects.public.controller.ts` in n8n-io/n8n at commit
`191a22e`.

**Allow-list.** The project allow-list (`project/PROJECT_ID`) applies as follows:

- `projects.list` keeps only the projects of the allow-list; n8n itself answers with every project of the
  instance. With an empty project allow-list it lists all of them, the same reading the workflow and execution
  tools give an empty list; this is the only project tool for which an empty list admits everything. As
  with `workflows.list`, a page can be empty after filtering while `has_more` is true. Personal projects are included when no allow-list is set; their names can identify a user.
- `projects.update` and `projects.delete` accept only a `project_id` of the allow-list, and therefore need a
  configured project allow-list: without a `project/PROJECT_ID` target no project is on the list, and both
  are refused locally. A foreign ID is refused the same way. Both refusals are invalid requests, made before a
  secret is read and before any request is sent, and neither names a project.
- `projects.create` is refused on a connection with a project allow-list, since the new project cannot
  already be on it. The request carries only `name`; n8n's `id`, `icon`, `description`, and telemetry tags
  are not offered, so a caller cannot choose the ID of the new project.
- A connection with a **workflow** allow-list refuses `create`, `update`, and `delete` of projects: they reach
  workflows beyond that list. `list` still works.

`projects.update` sends `{"name": ...}`, the one field `updateProject` accepts (HTTP 204). The Public API has
no endpoint to read a single project, so the change is not re-read.

**What deleting a project does.** `DELETE /projects/{id}` runs `ProjectsPublicController.deleteProject`
(`packages/cli/src/public-api/v1/controllers/projects.public.controller.ts`), which calls
`ProjectService.deleteProject(req.user, projectId)` (`packages/cli/src/services/project.service.ee.ts`)
without a migration target, and its query schema `DeleteProjectQueryPublicDto` is the empty strict object, so
the Public API accepts no transfer parameter (the editor's internal endpoint has `transferId`; the public one
has none). Without a target, n8n:

1. refuses anything but a team project (403 for a personal project),
2. deletes every workflow the project owns (`workflowService.delete(user, id, true)`),
3. deletes every credential the project owns,
4. deletes the project's data tables (when the data-table module is active),
5. removes its external-secrets connections and agent data (when those modules are active),
6. removes the project; workflows and credentials only shared into it lose that share.

Nothing is moved to another project and the deletion cannot be undone. The steps run one after another, not
in a transaction: a rejection part-way, for example a 409 because an owned workflow is still published, can
leave the project partly emptied. Qatlas therefore reports every provider rejection of a delete other than
403 and 404, as well as every unclear transport result, as "this change may have taken effect, read the
current state before repeating it". Because the Public API offers no transfer, the contract chosen here is
the only one it allows: a delete always deletes, and there is no argument to move content first.

**License and role.** All project routes carry `@Licensed(PROJECT_ROLE_ADMIN)`; `create` also needs the
global `project:create` scope. n8n answers 403 for a missing license (the license middleware), a missing API
key scope, and a role that may not manage projects, and this provider does not read the response body, so
every 403 of a project tool is reported as class `permission` with a message that names all three possible
causes (license, API key scope, role).

## Project members

The four member tools call `GET /projects/{id}/users`, `POST /projects/{id}/users`,
`PATCH /projects/{id}/users/{userId}`, and `DELETE /projects/{id}/users/{userId}`, and nothing else; the
routes are those of n8n-io/n8n at commit `191a22e` (scopes `user:list` and `project:manageMembers`).

- **Binding.** Like `projects.update`, every member tool, `list` included, accepts only a `project_id` of the
  project allow-list. Without a `project/PROJECT_ID` target, with a project outside it, with a workflow
  allow-list, or with a malformed `project_id` or `user_id`, the connection refuses locally, before a secret
  is read and before any request is sent, without naming the project.
- **Add.** `add` sends one `POST` with a single relation `{"relations":[{"userId","role"}]}` for an existing
  user: nobody is created or invited, and there is no email argument. It is not idempotent.
- **Roles.** `role` is one of `project:admin`, `project:editor`, `project:viewer`. `project:personalOwner`
  and instance roles are refused locally. `setrole` sends `PATCH` with `{"role"}`.
- **Output.** `list` returns only `id`, `email`, `name` (first and last name), and `role`, each at most 256
  characters, with the data sensitivity `n8n-project-members` (personal data). It pages with `cursor` and
  `limit` (default 100); more entries than `limit` are cut and flagged `truncated`.
- **403.** n8n answers 403 for a missing Projects license, a missing API key scope, and a role that may not
  manage members, and the body is not read, so every 403 is the neutral message "license or role missing",
  class `permission`.
- **Changes.** Each change needs `confirm`, sends exactly one request, and reports an unclear outcome
  (5xx, timeout, reset, unreadable answer) as "may have taken effect" without retrying.

## Data tables

The data table tools call `GET /data-tables` (with n8n's `projectId` filter), `GET`, `POST`, `PATCH`, and
`DELETE /data-tables/{id}`. They manage the tables only; `get` reports column definitions, not rows. Columns and rows have their own
tools, see "Data table columns" and "Data table rows" below.

- **Project binding.** `list` returns only tables of the project allow-list (a `project_id` argument must be
  on it; foreign tables are dropped from the page without being named, so a page may be empty while
  `has_more` is true). `get`, `rename`, and `delete` read the table first and refuse one whose project is
  outside the allow-list, or that reports no project, before any change request; the refusal names neither
  the table nor its project. `create` needs a `project_id` on the allow-list when the connection has one,
  checked locally before a secret is read; without an allow-list an omitted `project_id` means the key
  owner's personal project.
- **Workflow allow-list.** A connection with a `workflow/` target refuses every data table tool locally: a
  table cannot be tied to a workflow, so the narrower reading applies.
- **Create** sends the name, an empty column list, and the project; no CSV import field is offered. Names are
  1 to 128 characters without control characters.
- **Delete** removes the table and all its rows and cannot be undone; it is in no profile.
- **Errors.** A 403 is reported as a license, scope, or role error, without telling which; a change whose
  outcome is unclear is reported as uncertain and never retried.

## Data table columns

The column tools are `n8n.datacolumns.list`, `add`, `update`, and `delete`; they call
`GET` and `POST /data-tables/{id}/columns` and `PATCH` and `DELETE /data-tables/{id}/columns/{columnId}`. The
object segment is `datacolumns` because tool IDs have exactly three segments.

- **Table binding.** Every tool reads the table first and refuses one whose project is outside the
  allow-list, or that reports no project, before any change request; the refusal names neither the table nor
  its project. `update` and `delete` additionally require the `column_id` among the columns of that read; an
  unknown or foreign column is refused without a change request. A `workflow/` target refuses all four
  locally, as for tables.
- **Names and types.** A column name is 1 to 63 characters, a letter first, then letters, digits, or
  underscores; the type is `string`, `number`, `boolean`, or `date`. `add` appends unless `index` is given;
  `update` renames and/or moves (at least one of `name` and `index`) and never changes the type. `index` is
  limited to 0 to 199 (narrower than the API).
- **Delete** removes the column and its data in every row and cannot be undone; it is in no profile and is
  offered only by a tools list. `add` is not idempotent: a repeated call may add a second column or be refused
  as a name conflict.
- **Profiles.** The column tools have their own profiles, so that no existing data table profile widens.
- **Errors.** A 403 is reported as a license, scope, or role error; a change whose outcome is unclear is
  reported as uncertain and never retried.

## Data table rows

The row tools are `n8n.datarows.list`, `insert`, `update`, `upsert`, and `delete`; they call
`GET` and `POST /data-tables/{id}/rows`, `PATCH /data-tables/{id}/rows/update`,
`POST /data-tables/{id}/rows/upsert`, and `DELETE /data-tables/{id}/rows/delete`. The object segment is
`datarows` because tool IDs have exactly three segments. There is no tool for `rows/clear`.

- **Table binding.** Every tool reads the table first and refuses one whose project is outside the
  allow-list before any request on its rows; column names in filters and data are checked against the columns
  of that read, and an unknown column or a value of the wrong type (string or date: text, number: number,
  boolean: boolean) is refused before any change. A `workflow/` target refuses all five locally.
- **Filter.** Only a structured filter is accepted: `{type: and|or, conditions: [{column, operator, value}]}`
  with 1 to 10 conditions, operator one of `eq`, `neq`, `like`, `ilike`, `gt`, `gte`, `lt`, `lte`, and a
  string, number, or boolean value. `like` and `ilike` apply to string columns, the orderings to number and
  date columns. Qatlas builds the provider filter itself; no filter text or object is passed through. Only the
  table's own columns can be filtered (narrower than the API, which also knows system columns). `update`,
  `upsert`, and `delete` require a filter; matching all rows is never possible.
- **Data.** Rows contain only string (at most 1024 characters), number, and boolean values for known columns;
  `null`, nested values, and empty rows are refused. `insert` takes 1 to 50 rows and always asks n8n for the
  count only.
- **Reading.** `list` returns 50 rows per page by default, at most 100 (the API allows 250), with an opaque
  cursor. Row values are untrusted data; text is cut at 1024 characters, non-scalar values are dropped, and
  the answer is limited to 4 MiB. Sorting, text search, and `id` lookups are not offered.
- **Mass effect.** n8n offers no row limit on `update`, `upsert`, and `delete`; one call can change or remove
  every row that matches. They are therefore in no profile and offered only by a tools list, and each needs
  the per-call confirmation. `upsert` updates all matching rows or inserts one row when none matches. The
  answer reports acceptance, not the number of rows. `dryRun` and `returnData` are not offered.
- **Errors.** A 403 is reported as a license, scope, or role error; a change whose outcome is unclear is
  reported as uncertain and never retried. `insert` is not idempotent: a repeated call inserts the rows again.

## Variables

The variable tools call `GET /variables` (with n8n's `projectId` filter), `POST /variables`, and `PUT` and
`DELETE /variables/{id}`. A variable is a key with a plain-text value, either bound to a project or global
(`project` is null).

- **Separation.** Output marks each variable `scope: project` (with `project_id`) or `scope: global`. With a
  project allow-list, `list` returns only variables of those projects and never a global one; a `project_id`
  argument must be on it. `create` then requires a `project_id` from the allow-list. A global variable can
  only be created, read, changed, or deleted on a connection without project and workflow targets; both are
  checked locally before a secret is read where possible.
- **Update and delete** find the variable first by paging `GET /variables` (the Public API has no
  `GET /variables/{id}`; at most 20 pages of 250) and refuse one that is global or of a foreign project on a
  bound connection, or that is not found, before any change request; the refusal names neither the variable
  nor its project. `update` sends `key`, `value`, and the variable's own `projectId` (null for a global one):
  a variable is never moved between projects.
- **Workflow allow-list.** A connection with a `workflow/` target refuses every variable tool locally (narrower
  reading, as for data tables).
- **Fields.** `key` is 1 to 50 characters of letters, digits, and `_`; a new key does not start with a digit.
  `value` is a string of at most 1000 characters without control characters other than newline, carriage
  return, and tab. Values are returned in clear (each capped), as untrusted data.
- **Create** answers with the key and scope only: n8n reports no ID; `list` finds it. A repeated call may be
  refused as a key conflict.
- **Delete** cannot be undone and is in no profile.
- **Errors.** A 403 is reported as a license (`feat:variables`), scope, or role error, without telling which; a
  change whose outcome is unclear is reported as uncertain and never retried.

## Tags

The tag tools call `GET /tags` (paged by `limit` and `cursor`), `POST /tags`, `GET /tags/{id}`, and `PUT` and
`DELETE /tags/{id}`. A tag is an instance-wide label with an `id`, a `name`, and creation and update times.

- **Instance-wide gate.** Tags belong to the instance, not to a project or a workflow, so every tag tool is
  refused locally on a connection with any `project/` or `workflow/` target, before a secret is read and
  before any request; the refusal names no target. The gate is one reusable check, `requireInstanceScope`,
  for any further instance-wide tool.
- **Fields.** `name` is 1 to 24 characters without control characters; `tag_id` is validated as a single path
  segment. Names are returned capped, as untrusted data. Tags on workflows are not managed here.
- **Delete** cannot be undone, removes the tag from every workflow that carries it, and is in no profile.
- **Errors.** A 403 is reported as an API key scope or role error, without telling which; a change whose
  outcome is unclear is reported as uncertain and never retried.

## Users

The user tools call `GET /users` (paged by `limit` and `cursor`, with `includeRole=true`),
`GET /users/{id}`, `POST /users`, `PATCH /users/{id}/role`, and `DELETE /users/{id}`. A user has an `id`, an
`email`, a first and last name, an instance `role`, a pending status, and creation and update times; these are
personal data (`data_sensitivity` `n8n-users-personal`) and returned capped, as untrusted data. MFA state is
not returned.

- **Owner-only and instance-wide.** n8n offers these endpoints to the instance owner only, so a 403 is
  reported as an owner, license, or API key scope error, without telling which. Every user tool is refused
  locally on a connection with any `project/` or `workflow/` target (`requireInstanceScope`), before a secret
  is read and before any request.
- **Read.** `n8n.users.get` takes the user ID only. n8n would also accept an email address there; Qatlas does
  not. `n8n.users.list` does not forward n8n's `projectId` filter.
- **Roles.** Only `global:admin`, `global:member`, and `global:chatUser` can be assigned, as `role` of
  `n8n.users.setrole` (sent as `newRoleName`) and of `n8n.users.invite` (default `global:member`).
  `global:owner` and free-form role names are never sent.
- **Invite.** `emails` holds 1 to 10 distinct plain addresses (at most 254 characters, no display name) and
  is sent as one `POST /users` array. n8n answers per address, so some may fail: each entry reports `ok`, and
  the error text is not shown. An invite link that n8n returns when it sends no email is never output.
- **Delete** cannot be undone and is in no profile. It needs exactly one choice for the user's workflows
  and credentials: `transfer_project_id` moves them into that project (`DELETE /users/{id}?transferId=...`),
  or `delete_owned_resources: true` deletes them permanently with the user (the request without
  `transferId`). Neither, both, or `delete_owned_resources: false` without `transfer_project_id` is refused
  locally, before a secret is read; there is no default. The result names the consequence (`transferred` or
  `deleted_with_user`).
- **Errors.** A change whose outcome is unclear is reported as uncertain and never retried.

## Security audit

`n8n.audit.generate` calls `POST /audit` and nothing else. It is instance-wide (refused locally on a
connection with any `project/` or `workflow/` target, before a secret is read) and needs the owner or admin
role; a 403 is reported as a scope, license, or role error without telling which. It only reads and
computes, so it is effect `read`, safe, and needs no confirmation (the `audit` profile grants it on its own).
Optional arguments: `days_abandoned_workflow` (1 to 3650) and `categories`, distinct values of
`credentials`, `database`, `nodes`, `filesystem`, `instance`; no other audit parameter exists. The result
holds at most 5 reports, each with at most 50 sections (title, description, recommendation, location count)
and at most 50 locations per section (kind, id, name, workflow, node, node type, package URL, file path);
`settings` and `nextVersions` are dropped. All of it is untrusted data, capped per value, and `truncated`
flags what was cut. The audit may be slow on a large instance; an error is never retried.

## Source control

The source control tools call `GET /source-control/status?direction=push|pull`,
`POST /source-control/pull`, and `POST /source-control/push`, and nothing else. They never set up or change
the connection to the repository, and do not touch environments or promotions. They are instance-wide
(refused locally on a connection with any `project/` or `workflow/` target, before a secret is read) and need
the Enterprise source control feature plus the matching API key scope; a 403 is reported as a license, scope,
or role error without telling which.

- **Status.** `direction` is required (`push` or `pull`, no default). The result lists at most 200 affected
  objects with `id`, `name`, `type`, `status`, `location`, and `conflict`; the repository file path is not
  returned. It changes nothing.
- **Pull** imports the connected Git branch into the instance and can overwrite workflows, credentials,
  variables, and other objects. It is effect `execute`, non-idempotent, always needs confirmation, and is in
  no profile: only a connection whose `tools` list names it offers it. `force` (default `false`) sends
  `force: true`, which discards local changes that would otherwise block the pull. `auto_publish` is `none`
  (default, nothing is published by the pull), `all` (publishes every imported workflow), or `published`
  (publishes those that were published locally before the import); both are always sent explicitly, and any
  other value is refused locally.
- **Push** commits and pushes objects to the repository, an external change. It has the same effect, risk,
  and tool-list rule as pull. `commit_message` is 1 to 1000 characters (UTF-16 units, as n8n counts them) and,
  narrower than n8n, contains no control character except a line feed. `files` holds 1 to 50 distinct
  `{id, type}` objects; `id` is a plain n8n identifier and `type` is one of `credential`, `workflow`, `tags`,
  `variables`, `folders`, `project`, `datatable` (the generic `file` type n8n also lists is not accepted).
  `force` (default `false`) pushes even objects with unresolved conflicts.
- **Conflict (HTTP 409).** n8n refuses a pull because of local changes or merge conflicts, and a push because
  of unresolved conflicts, and changes nothing. This is a result, not an error: `outcome` is `conflict`,
  `conflict` is `true`, and `files` lists the entries n8n reports. Qatlas does not retry and does not set
  `force` by itself; the caller decides.
- **Results.** `outcome` is `pulled` or `pushed` when n8n accepted the request. Each file may also carry n8n's
  publishing error, its reason, and a capped list of import policy findings; these texts are untrusted data,
  capped, and never part of an error message.
- **Errors.** A timeout, connection reset, 5xx, or unreadable answer is reported as uncertain ("this change
  may have taken effect, read the current state before repeating it") and never retried; the request is sent
  once.

## Credentials

The credential tools call `GET /credentials`, `GET /credentials/{id}`, `POST /credentials/{id}/test`, and
`GET /credentials/schema/{credentialTypeName}`. They expose metadata only: `id`, `name`, `type`, and
creation and update times. Responses are decoded into structures without a field for a credential's data, so a
stored value never reaches a result or an error, whatever n8n sends.

- **Binding.** With a project allow-list `list` keeps only credentials shared into those projects. `get` and
  `test` first find the credential in the paged list (at most 20 pages of 250), because the single read has no
  project information; a credential that is not found, is outside the allow-list, or whose search cannot
  finish is refused without any further request and without naming a project. Each `shared` entry of the list
  response carries the project as its `id` (with `name` and `role`); the single read has no `shared`. Only
  that `id` is read; an entry without one never matches. A connection with a workflow
  allow-list refuses every credential tool locally, as for data tables and variables.
- **Test.** `credentials.test` makes n8n use the stored secret against the third-party service, so it is
  effect `execute`, idempotent, open-world, and always needs confirmation. It sends exactly one request and is
  never retried; an unclear outcome is reported as uncertain. The result is `status` and a capped `message`,
  untrusted data, never part of an error. It has its own profile so it can be granted separately from reading.
- **Schema.** `credential_type` is letters, digits, `.`, `-`, and `_` (at most 100 characters, starting with a
  letter or digit) and is sent as one escaped path segment. The result lists at most 200 fields (name, type,
  required), sorted; defaults and descriptions are not passed on.
- **Create.** `credentials.create` calls `POST /credentials` with `name`, `type`, `data`, and, when given,
  `projectId`. `data` is built from the plain `data` argument (an object of at most 50 fields; names of
  letters, digits, `_`, `-`, `.`, starting with a letter; values strings of at most 1024 characters, numbers,
  or booleans; never null, objects, or arrays) plus the fields of the `secret_ref`. At least one of them is
  required. With a project allow-list `project_id` is required and must be one of its projects; without one it
  is optional and n8n defaults to the API key owner's personal project. A repeated call creates another
  credential.
- **Update.** `credentials.update` calls `PATCH /credentials/{id}` with `name` and/or `data`. The type cannot be
  changed. An update with `data` or `secret_ref` **replaces the whole stored data**: Qatlas sends the merged
  `data` object without `isPartialData`, which n8n treats as false and replaces the entire data object. A field
  that is in neither `data` nor the referenced forward credential, a stored secret included, is lost, so give
  every plain field in `data` and every secret field through `secret_ref`. A rename (only `name`) sends no
  `data` and leaves the stored data unchanged. At least one of `name`, `data`, `secret_ref` is required.
- **Secret values.** A value is never an argument. `secret_ref` names a forward credential that the
  connection releases in `forward_secrets`; the core checks it before the confirmation gate and refuses an
  unreleased or unknown name (`secret-ref-not-allowed`). The values are read only after the confirmation, for
  the one request, registered with the redactor, and set into `data` under the field names of the forward
  credential. A plain `data` field with the same name as a secret field is refused, never overwritten.
  Because of `secret_ref`, `create` and `update` need a tools list entry, the `create` or `update`
  permission, and their own confirmation. The plain `data` argument is not redacted: put no secret into it.
- **Transfer.** `credentials.transfer` calls `PUT /credentials/{id}/transfer` with `destinationProjectId`. It
  needs a project allow-list, the credential must be found in one of its projects (the same bounded search as
  `get`), and `destination_project_id` must be in the allow-list; otherwise it is refused, without a request
  where that can be decided locally. Workflows in other projects may lose access to the moved credential.
- **Delete.** `credentials.delete` calls `DELETE /credentials/{id}` after the same binding search. Workflows
  that use the credential fail until another one is assigned, and the stored secret is lost.
- **Changes.** Update, transfer, and delete additionally require the owning project (the `shared` entry with
  role `credential:owner`) to be in the allow-list; a credential only shared into an allowed project, or without
  a role, is refused before the changing request. Update, transfer, and delete find the credential through the binding search before the one
  changing request; the search only reads. Every change needs its own confirmation, is sent once, and is never
  retried; an unclear outcome is reported as uncertain. Results are the credential metadata, or `deleted` and
  `transferred` flags; never a stored value.
- **Errors.** A 403 is reported as a license, scope, or role error, without telling which.

## Version and plan boundaries

`workflows.activate` and `workflows.deactivate` use n8n's own `POST /workflows/{id}/activate` and
`POST /workflows/{id}/deactivate` endpoints. n8n's own Public API spec marks both **deprecated** in favour
of `POST /workflows/{id}/publish` and `/unpublish`, a newer, versioned publishing model; this provider
intentionally keeps using the deprecated pair, since publish/unpublish and the version history behind them
are out of this milestone's scope. `activateWorkflow` additionally accepts an optional `versionId`, `name`,
and `description` to activate a specific saved version; this provider does not offer them and always
activates the latest version, the same effect as omitting them.

The n8n Public API is not available on a free trial; upgrading to a paid plan is a prerequisite this
provider cannot detect ahead of a failed request. The Projects feature, and therefore a project allow-list's
live membership check, needs an Enterprise plan; see "Scope" above for what happens when an instance or
Public API version never reports it.

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
"`workflows.create` under a project or workflow restriction" above.

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
variables, and lists, reads, creates, renames, and deletes tags and lists, reads, invites, re-roles, and deletes users on connections without targets, and lists, reads, and tests
credentials as metadata, generates the security audit, previews, pulls, and pushes source control changes on connections without targets, reads credential type schemas, and creates, updates, moves between projects, and deletes credentials, secret values only by reference. It does
not, and has no tool to, start a workflow (no Public API endpoint exists for that), delete a workflow or an
execution, stop many executions at once, archive, unarchive, publish, unpublish, or transfer a workflow, move
a project's content elsewhere before deleting it, manage folders, manage roles or passwords, MFA, or single sign-on of users, accept any credential value as an argument, change a credential's type, update a credential's data in part (an update with data replaces all of it),
move a credential to a project outside the allow-list, set up or change the source control connection, manage environments or promotions, request audit parameters beyond the five categories, or manage tags on workflows, or clear all rows of a data table; those are deliberately out of
scope.
