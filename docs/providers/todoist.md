---
description: >
  Describes the Todoist provider: setup with a personal API token, project and account scope, the task, project, section, label, comment, reminder, completed-task, and saved-filter reads, the confirmed task, comment, reminder, project, section, and label changes with their retry contract and scope rules, structured and expression filters, the cursor contract, plan-dependent errors, and the boundary to workspace and account administration.
type: knowledge
edit: shared
created: 2026-09-23
updated: 2026-10-22
---

# Todoist

Todoist is a provider for one personal Todoist account through the official Todoist API v1. It reads the
account's projects, sections, labels, tasks, comments, reminders, and saved filters, and changes tasks,
comments, time-based reminders, personal projects, sections, and personal labels, each within the
connection's scope; every change needs its own confirmation. Saved filters are only read.

## Configuration

A service is always the official API root `https://api.todoist.com/api/v1`, which the terminal editor fills
in and which applies when `base_url` is left out. Qatlas refuses any other URL before a secret is read,
because a personal token belongs to that one service.

The credential provides `token`, the personal API token from Todoist under Settings, Integrations,
Developer. That token reaches the whole account, so the connection target is what limits Qatlas. A
successful `qatlas connection test` reads each configured project and section once, or one project of the
account for a wildcard connection; it proves that the token sees the scope, not that the account's plan
includes every feature.

```yaml
services:
  todoist:
    provider: todoist
    base_url: https://api.todoist.com/api/v1

credentials:
  todoist-reader:
    provider: todoist
    type: keyring
```

## Scope

A connection names its targets explicitly, or deliberately the whole account. Several targets form an
allow-list: the connection reaches what any of them reaches.

| Target | Binds |
| --- | --- |
| `PROJECT_ID` | exactly this project, without its subprojects |
| `project/PROJECT_ID` | this project and all its subprojects at any depth, also those created later |
| `section/SECTION_ID` | the tasks of this section, with their comments and reminders |
| `*` | the whole account, including saved filters; stands alone |

An ID is the ID the matching list tool returns (letters and digits). Subprojects are resolved on every
call from the project list, so no subproject is remembered across calls. An empty target never means the
whole account. The terminal editor warns whenever `*` is entered.

A binding reaches a tool only where the tool can act inside it; the others are not offered, and the call
check stays in force:

| Tools | `*` | `PROJECT_ID`, `project/PROJECT_ID` | `section/SECTION_ID` only |
| --- | --- | --- | --- |
| task, completed-task, comment, and reminder tools; `todoist.labels.list` | yes | yes | yes, within the section |
| `todoist.sections.get`, `todoist.sections.update` | yes | yes | yes, the bound section |
| project tools except create; section list, create, reorder, move, delete | yes | yes | no |
| `todoist.projects.create` | yes | only below a `project/PROJECT_ID` project | no |
| label changes, `todoist.filters.list` | yes | no | no |

No argument names or widens the scope. A `project_id`, `section_id`, or `parent_id` argument only selects
inside it and is refused when it lies outside. Every object ID is proven against the scope before it is
read, changed, or returned, and no refusal names the object or a target. A task belongs to a section
binding when its `section_id` is the bound one. The comments and reminders of a task are requested only
after the task was read and found inside. Todoist answers are checked again: a row outside the scope is never
returned, even when Todoist answers more broadly than asked. A connection bound to exactly one project or
one section narrows every task request to it at Todoist; a project tree is narrowed only by a named
`project_id`.

A change reads what it concerns first and is refused before anything is sent when it lies outside the
connection: no task is created in, moved into, or attached below another place, a section must belong to the
scope, and a comment is changed only when its task or project belongs to it. A new task without a place goes
to the connection's only target. A `*` connection reads nothing first.

Structure changes follow the same boundary:

- A new project would lie outside every list unless it joins a tree, so `todoist.projects.create` needs
  `parent_id` inside a `project/PROJECT_ID` project on any binding but `*`; it joins that tree at once.
- Todoist archives and deletes a project with all its subprojects. Qatlas reads the project tree first (for a
  delete the archived projects too) and refuses the change when a subproject at any depth lies outside the
  connection. A tree of more than 25 pages of 200 projects is refused unresolved. Unarchiving restores the
  project alone, as a top-level project.
- Only personal projects are changed: every project change reads the project first and refuses a workspace
  project, and no project is created in or below one.
- A reminder belongs to a task, which is read first and must belong to the connection.

## Choosing targets

A person chooses from the projects of the account, subprojects below their project with the parent chain as
path, and then from the sections of one project. A chosen project becomes `project/PROJECT_ID`, a section
`section/SECTION_ID`; an exact `PROJECT_ID` is entered by hand. The connection's targets do not narrow what is
offered, and choosing only reads, through no tool and for no agent. Projects and sections are searched by name
at Todoist, a project search at any depth; names are untrusted and are shortened and cleaned.

| URL | Target |
| --- | --- |
| `app.todoist.com/app/project/SLUG-ID` | `project/ID` |
| `app.todoist.com/app/task/SLUG-ID` | the task's `project/ID` and, if set, its `section/ID`, after one read of the task |

The ID follows the last `-` of the last segment. Any other form or host resolves to nothing.

## Tools

| Tool | Reads |
| --- | --- |
| `todoist.projects.list` | active projects, optionally by `search` text |
| `todoist.projects.get` | one project |
| `todoist.sections.list` | sections, optionally of one `project_id` or by `search` text |
| `todoist.sections.get` | one section |
| `todoist.labels.list` | personal labels, optionally by `search` text |
| `todoist.tasks.list` | active tasks with structured filters |
| `todoist.tasks.filter` | active tasks that match a Todoist filter expression |
| `todoist.tasks.get` | one active task |
| `todoist.completed-tasks.list` | tasks completed between `since` and `until` |
| `todoist.comments.list` | the comments of exactly one `task_id` or one `project_id` |
| `todoist.reminders.list` | time-based reminders; of one `task_id` unless `*` |
| `todoist.reminders.get` | one time-based reminder of a task of the connection |
| `todoist.filters.list` | saved filters with their queries; `*` connections only |

The changes:

| Tool | Effect | Repeating it | Changes |
| --- | --- | --- | --- |
| `todoist.tasks.create` | `create` | creates a second task | one task in a project, section, or below a parent task |
| `todoist.tasks.update` | `update` | same state | content, description, labels, priority, assignee, due date, duration |
| `todoist.tasks.move` | `update` | same state | the project, section, or parent task of one task and its subtasks |
| `todoist.tasks.close` | `update` | skips an occurrence of a recurring task | completes one task with its subtasks |
| `todoist.tasks.reopen` | `update` | same state | makes one completed task active again |
| `todoist.tasks.delete` | `delete` | same state | deletes one task with its subtasks and comments permanently |
| `todoist.comments.create` | `create` | adds a second comment | one text comment on one `task_id` or one `project_id` |
| `todoist.comments.update` | `update` | same state | the text of one comment |
| `todoist.comments.delete` | `delete` | same state | deletes one comment permanently |
| `todoist.reminders.create` | `create` | adds a second reminder | one time-based reminder of one task |
| `todoist.reminders.update` | `update` | same state | the time or urgency of one reminder |
| `todoist.reminders.delete` | `delete` | same state | deletes one reminder |

The structure changes:

| Tool | Effect | Repeating it | Connection | Changes |
| --- | --- | --- | --- | --- |
| `todoist.projects.create` | `create` | creates a second project | `*`, or below a tree | one personal project, optionally below a personal parent |
| `todoist.projects.update` | `update` | same state | any | name, description, color, favorite mark, view style |
| `todoist.projects.archive` | `update` | same state | any | archives one project with all its subprojects |
| `todoist.projects.unarchive` | `update` | same state | any | restores one archived project alone, as a top-level project |
| `todoist.projects.delete` | `delete`, listed only | same state | any | deletes one project with its subprojects, sections, tasks, and comments |
| `todoist.sections.create` | `create` | creates a second section | any | one section in a project |
| `todoist.sections.update` | `update` | same state | any | name and description of one section |
| `todoist.sections.reorder` | `update` | same state | any | the position of one section in its project |
| `todoist.sections.move` | `update` | same state | any | moves one section with its tasks to another project |
| `todoist.sections.delete` | `delete`, listed only | same state | any | deletes one section with all its tasks |
| `todoist.labels.create` | `create` | creates a second label or fails | `*` only | one personal label |
| `todoist.labels.update` | `update` | same state | `*` only | name, color, favorite mark; a new name reaches every task |
| `todoist.labels.reorder` | `update` | same state | `*` only | the position of one label in the label list |
| `todoist.labels.delete` | `delete`, listed only | same state | `*` only | deletes one label and removes it from every task |

"Any" means every binding that reaches the tool, see Scope. A tool marked listed only takes more
than itself with it, so it is offered only by a connection whose `tools` list names it, whatever its
`effects`; a connection that selects by `effects` never offers it.

Every tool that returns a task returns it with the same fields, whatever the list or change. A comment
attachment or thumbnail address is shown only as an absolute `http` or `https` address without user information
on a host other than `todoist.com` and its subdomains; otherwise it is left out, and a thumbnail keeps its size.
Location reminders are not read.

`todoist.tasks.list`, `tasks.filter`, `tasks.get`, `completed-tasks.list`, `tasks.create`, `tasks.update`, and
`tasks.move` carry contract version 2, because the task no longer has a comment count.

The terminal editor starts a new connection on the setup profile `read`, every read tool except
`todoist.filters.list`. `account-read` adds the saved filters for a `*` connection, `tasks` the task,
comment, and reminder changes except the deletes, and `organize`, for a `*` connection, also the creates,
updates, reorders, and moves of projects, sections, and labels. No profile selects an archive, an unarchive,
or a delete: select those tools deliberately. A profile is a visible starting selection, not a role: only
the ticked `tools` are saved.

## Changes

Every change needs `--confirm` (or `confirm` over MCP) in its own request, on a connection that
offers it, by its `effects` or by naming it in its `tools` list; otherwise it is refused before
a secret is read or Todoist is contacted. Moving, closing, reopening, and deleting are tools of their own, so
an update never moves or completes a task, and a close never reopens one.

`todoist.tasks.create` takes `content` (one line, at most 500 characters) and places the task by
`project_id`, `section_id`, or `parent_id`; `section_id` and `parent_id` exclude each other, and a named
project must be the one of the section or parent. Without any of them the task goes to the connection's only
target, to the Inbox on a `*` connection, and is refused on a connection with several targets.
`todoist.tasks.update` changes only the values it names, and removes a due date, a duration, or the assignee
only through its own `clear_due`, `clear_duration`, or `unassign`. Qatlas interprets no natural language
itself: `due_string` is passed to Todoist unchanged.

`todoist.tasks.move` takes exactly one destination: `project_id`, `section_id`, or `parent_id`, each of the
connection. Comments are read and changed only by the comment tools, for one task, project, or comment named
in the request; no task tool reads or changes them. A comment is text only: attachments are not offered,
because Todoist takes them only as an uploaded file or a link to one.

`todoist.reminders.create` takes `task_id` and exactly one time, relative to the due time of the task or
absolute. `todoist.reminders.update` changes the time or the urgency; Todoist decides which time fits the
reminder's type. Location reminders are not offered: their coordinates and triggers are outside this
provider's contract. The delivery channel of a reminder is left to the account.

A section create without `project_id` goes to the connection's only project or project tree. Reordering is a
tool of its own: `todoist.sections.reorder` and `todoist.labels.reorder` set one `order` position and change
nothing else.

`todoist.sections.move` is the one change API v1 offers only through its Sync endpoint. Qatlas sends exactly
one `section_move` command with the section and the destination project and reads that command's own result;
no other command, no command a caller names, and no Sync passthrough exist.

Each change is one request that Qatlas never repeats on its own. It carries a fresh `X-Request-Id`, which
Todoist uses to recognise a duplicate delivery. When the outcome is unclear, because the connection broke,
Todoist did not answer in time, answered with a server error, or sent an unreadable answer, the error says
that the change may have been applied: read the resource before repeating it. This matters most for the
non-idempotent changes (a repeated create makes a second resource, a repeated close of a recurring task skips
an occurrence) and for the destructive ones. A change Todoist refused as invalid, forbidden, or rate-limited
was not applied. The reads a scope check needs are made before the change is sent.

A change returns the resource in the shape of its read, or the `id` and the `result` for a close, reopen,
archive, unarchive, section move, or delete. The audit event of a confirmed change names the tool, the
connection, the confirmation, the result, and the time, never its arguments, the token, or a Todoist answer.

## Filters

`todoist.tasks.list` accepts structured filters only. The ID, project, section, parent, and label filters are
passed to Todoist; the due window (inclusive dates compared with the date part of a task's due date) or
`without_due` is applied by Qatlas. A task without a due date never matches a due window.

The filters name tasks, never reach: `ids`, `parent_id`, and `workspace_id` widen nothing, and every returned
task is held to the connection's targets, so a task named in `ids` that lies outside them is dropped.

A Todoist filter expression, such as `today | overdue` or the query of a saved filter, is accepted only by
`todoist.tasks.filter` as `query`, and for completed tasks by `todoist.completed-tasks.list` as `query`. Their
results are held to the connection's projects like every other list, so an expression that names another
project returns nothing of it. The language of an expression is named by `lang` or, for completed tasks, by
`filter_lang`, which is refused without `query`.

`todoist.completed-tasks.list` reads the tasks completed from `since` (inclusive) until `until` (exclusive),
both RFC 3339 and at most three months apart. Its narrowing is passed to Todoist on every page, and the cursor
is bound to it.

## Cursor contract

Every list except the saved filters takes `limit` (1 to 200, default 50) and an opaque `cursor`, and
answers `has_more` plus `next_cursor` whenever Todoist announced a further page. Each request reads exactly
one Todoist page; Qatlas never loads a following page on its own. Because scope and due window are applied
to that page, a batch may be short or even empty while `has_more` stays true: read on until `has_more` is
false. A full batch never means the end.

The cursor wraps Todoist's own cursor unchanged. It is bound to the tool, the connection's scope, and the
arguments that produced it, and it keeps the batch size of its first batch; a cursor from other arguments,
another tool, or another connection is an invalid request. Todoist cursors are short-lived: when Todoist no
longer accepts one, start the list again without a cursor. Tasks that change while a caller pages can
appear twice or move past the cursor.

`todoist.filters.list` answers every saved filter at once through one read-only Sync request that asks for
filters only and carries no command. Todoist counts it against its stricter Sync request budget.

## Errors and plans

Errors keep stable classes and never carry the token, a provider body, or a URL with its query:

| Class | Cause |
| --- | --- |
| `auth` | Todoist rejected the token |
| `permission` | the account's plan does not include the feature (reminders, filters, more labels, or older completed tasks), or the token may not read or change the resource; the message tells them apart |
| `rate-limited` | Todoist asked to wait; the next request of the same token waits as asked, at most one minute |
| `not-found` | Todoist does not hold the resource or does not show it to this token, or it was deleted |
| `provider-error` | Todoist rejected the request, no longer accepts the cursor, or answered a change with a server error |
| `invalid-provider-response` | the answer was unreadable, too large, or lacked identifiers |

A resource outside the connection's projects is an invalid request, never a provider error, so a scope
refusal cannot be mistaken for a missing resource. A change whose outcome is unclear says so in its message,
whatever its class.

## Untrusted data

Names, contents, descriptions, comments, and filter queries come from the account and are untrusted data.
Qatlas passes them on as JSON strings and never renders Markdown or HTML, follows a link, or runs a filter
query unless a caller passes it to `todoist.tasks.filter`.

## Boundary

The provider reads a personal account and changes its tasks, comments, reminders, personal projects,
sections, and personal labels. It does not change saved filters, shared labels, location reminders, or
workspace projects, uploads no files, offers no bulk changes, does not administer workspaces, folders,
collaborators, invitations, users, billing, account, view, or notification settings, or OAuth apps, does not
read live notifications, activity, backups, or archived projects, offers no generic Sync or API passthrough,
and keeps no local copy of the account. Tasks and sections of workspace projects the account has joined are
read and changed like any other project when the connection names them; the workspace project itself is not.

Todoist marks `note_count` as deprecated and always answers 0, so a task has no comment count; its comments
come from `todoist.comments.list`. `is_deleted` is not returned: Qatlas drops every deleted entry, so the value
would always be false. Attachment addresses hosted by Todoist are not returned: its documentation does not show
that they require a sign-in, so they count as an access value, and no Todoist tool returns one. External
addresses appear as untrusted text and are never fetched. The parameter `public_key` of `GET /tasks/{task_id}`,
`GET /tasks/completed/by_completion_date`, `GET /sections`, `GET /sections/{section_id}`, and `GET /comments` is
not offered: it reads publicly shared projects of others, outside the connection's targets. A project's own
`public_key` grants access without a sign-in; as an access value it is not returned.
