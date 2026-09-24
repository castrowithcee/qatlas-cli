---
description: >
  Describes the Todoist provider: setup with a personal API token, project and account scope, the task, project, section, label, comment, reminder, completed-task, and saved-filter reads, the confirmed task, comment, reminder, project, section, and label changes with their retry contract and scope rules, structured and expression filters, the cursor contract, plan-dependent errors, and the boundary to workspace and account administration.
type: knowledge
edit: shared
created: 2026-09-23
updated: 2026-09-23
---

# Todoist

Todoist is a provider for one personal Todoist account through the official Todoist API v1. It reads active
projects, sections, personal labels, tasks, completed tasks, task and project comments, reminders, and saved
filters. It creates, updates, moves, closes, reopens, and deletes tasks, creates, updates, and deletes task
and project comments and time-based reminders, and creates, updates, reorders, moves, archives, and deletes
personal projects, sections, and personal labels, each within the connection's scope; every change needs its
own confirmation. Saved filters are only read.

## Configuration

A service is always the official API root `https://api.todoist.com/api/v1`, which the terminal editor fills
in. Qatlas refuses any other URL before a secret is read, because a personal token belongs to that one
service.

The credential provides `token`, the personal API token from Todoist under Settings, Integrations,
Developer. That token reaches the whole account, so the connection target is what limits Qatlas. A
successful `qatlas connection test` reads each configured project once, or one project of the account for a
wildcard connection; it proves that the token sees the scope, not that the account's plan includes every
feature.

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

A connection names its projects explicitly, or deliberately the whole account:

| Target | Binds |
| --- | --- |
| `target: PROJECT_ID` | one project |
| `targets: [PROJECT_ID, PROJECT_ID, ...]` | exactly these projects |
| `target: "*"` | the whole account, including saved filters |

A project ID is the ID `todoist.projects.list` returns (letters and digits). Subprojects are not included by
their parent: list every project the connection may read. An empty target never means the whole account,
and `*` must be the only target. The terminal editor warns whenever `*` is entered.

No argument names or widens the scope. A `project_id` argument only selects one of the connection's
projects and is refused before any request when it lies outside them. Todoist answers are checked again:
a task, section, comment, or reminder of another project is never returned, even when a filter expression
or Todoist itself answers more broadly, and reading one by its ID is refused without its content. The
comments and reminders of a task are requested only after the task was read and found inside the scope.
A connection bound to exactly one project narrows every task and section request to that project.

A change on a project connection first reads the task or comment it concerns and every section, parent task,
or task it names, and is refused before anything is sent when one of them lies outside the connection: no
task is created in, moved into, or attached below another project, a section must belong to one of the
connection's projects, and a comment is changed only when its task or project belongs to them. A `project_id`
argument is checked against the connection before any request. A `*` connection holds every project, so it
reads nothing first and leaves the combination to Todoist.

The structure changes follow the same boundary:

- A project connection updates, archives, unarchives, and deletes only its own projects, and creates, changes,
  reorders, moves, and deletes sections only inside them; a section moves only to another project of the
  connection.
- A new project would lie outside every project list, so `todoist.projects.create` is offered only by a `*`
  connection; a project connection refuses it as an unsupported capability before a secret is read. The new
  project does not join any connection's target.
- Todoist archives and deletes a project together with all its subprojects. On a project connection Qatlas
  therefore reads the project tree first (the active projects, and for a delete the archived ones too) and
  refuses the change when a subproject at any depth lies outside the connection. A tree of more than 25 pages
  of 200 projects is refused unchecked. Unarchiving restores the project alone, as a top-level project.
- Only personal projects are changed. Every project change reads the project first, on every connection,
  and refuses a workspace project; a new project is never created in a workspace or below a workspace project.
- Personal labels belong to the account, not to a project, and a label change reaches every task that carries
  the label, so the label changes are offered only by a `*` connection.
- A reminder belongs to a task. On a project connection a new reminder's task, or an existing reminder and
  then its task, is read first and must belong to the connection.

Personal labels belong to the account, not to a project; every connection may list their names and colors,
never tasks through them. Saved filters span every project, so only a `*` connection offers
`todoist.filters.list`; a project connection refuses it as an unsupported capability before a secret is
read. A project connection reads reminders only for one named task.

## Tools

| Tool | Reads |
| --- | --- |
| `todoist.projects.list` | active projects, optionally by `search` text |
| `todoist.projects.get` | one project with its description |
| `todoist.sections.list` | sections, optionally of one `project_id` or by `search` text |
| `todoist.sections.get` | one section |
| `todoist.labels.list` | personal labels, optionally by `search` text |
| `todoist.tasks.list` | compact active tasks with structured filters |
| `todoist.tasks.filter` | compact active tasks that match a Todoist filter expression |
| `todoist.tasks.get` | one active task with its description |
| `todoist.completedtasks.list` | tasks completed between `since` and `until` |
| `todoist.comments.list` | the comments of exactly one `task_id` or one `project_id` |
| `todoist.reminders.list` | time-based reminders; of one `task_id` on a project connection |
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
| `todoist.projects.create` | `create` | creates a second project | `*` only | one personal project, optionally below a personal parent |
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

"Any" means a `*` connection or a project connection within its projects. A tool marked listed only takes more
than itself with it, so it is offered only by a connection whose `tools` list names it, whatever its
`permissions`; a connection without a `tools` list never offers it.

Lists return compact tasks: ID, content, project, section, parent, labels, priority (1 normal to 4 urgent),
due date, deadline, and comment count. Only `todoist.tasks.get` adds the description and timestamps, and
only `todoist.comments.list` returns comments. A comment attachment is described by file name and type,
never by its address. Location reminders are not read.

The terminal editor starts a new connection on the setup profile `read`, which ticks `[read]` and every
read tool except `todoist.filters.list`. The profile `account-read` adds the saved filters for a `*`
connection. The profile `tasks` adds the task, comment, and reminder changes except the deletes and ticks
`[read, create, update]`. The profile `organize`, for a `*` connection, adds to `account-read` and `tasks`
the creates, updates, reorders, and moves of projects, sections, and labels. No profile ticks an archive, an
unarchive, or a delete: tick `delete` and those tools deliberately. A profile is a visible starting
selection, not a role: only the ticked `permissions` and `tools` are saved.

```sh
qatlas invoke todoist.tasks.list --connection todoist-work
echo '{"due_from":"2026-09-21","due_to":"2026-09-27","label":"waiting"}' |
  qatlas invoke todoist.tasks.list --connection todoist-work
qatlas invoke todoist.tasks.get --connection todoist-work --arg task_id=6X7rM8997g3RQmvh
echo '{"content":"Send the invoice","due_string":"tomorrow 9am"}' |
  qatlas invoke todoist.tasks.create --connection todoist-tasks --confirm
echo '{"task_id":"6X7rM8997g3RQmvh","minute_offset":30}' |
  qatlas invoke todoist.reminders.create --connection todoist-tasks --confirm
echo '{"name":"Waiting","project_id":"6XGgm6PHrGgMpCFX"}' |
  qatlas invoke todoist.sections.create --connection todoist-organize --confirm
```

## Changes

Every change needs `--confirm` (or `confirm` over MCP) in its own request, on a connection whose
`permissions` hold its effect and whose `tools` list, when present, names it; otherwise it is refused before
a secret is read or Todoist is contacted. Moving, closing, reopening, and deleting are tools of their own, so
an update never moves or completes a task, and a close never reopens one.

`todoist.tasks.create` takes `content` (one line, at most 500 characters) and places the task by
`project_id`, `section_id`, or `parent_id`; `section_id` and `parent_id` exclude each other, and a named
project must be the one of the section or parent. Without any of them the task goes to the connection's only
project, to the Inbox on a `*` connection, and is refused on a connection with several projects.
`todoist.tasks.update` changes only the values it names. Both take:

- `description` in Markdown, at most 16383 characters,
- `labels` as personal label names; an update replaces every label, `[]` removes them all,
- `priority` from 1 (normal) through 4 (urgent),
- `assignee_id`, a collaborator's user ID in a shared project,
- one due date: `due_string` as Todoist reads it (such as `tomorrow 9am` or `every monday`, optionally with
  the two-letter `due_lang`), `due_date` as `YYYY-MM-DD`, or `due_datetime` as RFC 3339,
- `duration` from 1 through 10000 together with `duration_unit` `minute` or `day`.

An update removes a due date with `clear_due`, a duration with `clear_duration`, and the assignee with
`unassign`. Qatlas interprets no natural language itself: `due_string` is passed to Todoist unchanged.

`todoist.tasks.move` takes exactly one destination: `project_id`, `section_id`, or `parent_id`, each of the
connection. Comments are read and changed only by the comment tools, for one task, project, or comment named
in the request; no task tool reads or changes them. A comment is text only: attachments are not offered,
because Todoist takes them only as an uploaded file or a link to one.

`todoist.reminders.create` takes `task_id` and exactly one time: `minute_offset` (0 through 43200 minutes
before the task is due; the task needs a due time) makes a relative reminder, `due_string` (optionally with
`due_lang`) or `due_datetime` (RFC 3339) an absolute one. `is_urgent` marks an urgent reminder.
`todoist.reminders.update` changes the time or the urgency; Todoist decides which time fits the reminder's
type. Location reminders are not offered: their coordinates and triggers are outside this provider's
contract. The delivery channel of a reminder is left to the account.

`todoist.projects.create` and `todoist.projects.update` take `name` (one line, at most 120 characters),
`description`, `color` (a Todoist color name such as `blue` or `berry_red`), `is_favorite`, and `view_style`
(`list`, `board`, or `calendar`); a create takes `parent_id` of a personal project as well. Sections take
`name` (one line, at most 2048 characters) and `description`; a create without `project_id` goes to the
connection's only project. Labels take `name` (one line, at most 128 characters), `color`, and
`is_favorite`. Reordering is a tool of its own: `todoist.sections.reorder` and `todoist.labels.reorder` set
one `order` position and change nothing else.

`todoist.sections.move` is the one change API v1 offers only through its Sync endpoint. Qatlas sends exactly
one `section_move` command with the section and the destination project and reads that command's own result;
no other command, no command a caller names, and no Sync passthrough exist.

Each change is one request that Qatlas never repeats on its own. It carries a fresh `X-Request-Id`, which
Todoist uses to recognise a duplicate delivery of the same request. When the outcome is unclear, because
the connection broke, Todoist did not answer in time, answered with a server error, or sent an unreadable
answer, the error says that the change may have been applied: read the task or the comments before repeating
it. This matters most for the non-idempotent changes: a repeated create makes a second task, comment, reminder,
project, section, or label, and a repeated close of a recurring task skips an occurrence. It matters as much
for the destructive ones: after an unclear archive or delete, read the project, section, label, or reminder
before deciding. A change Todoist refused as invalid, forbidden, or rate-limited was not applied. The reads a
scope check needs are no part of the change and are made before it is sent.

Tasks, comments, projects, sections, labels, and reminders return in the same shape as their reads; a close,
reopen, archive, unarchive, section move, or delete returns the `id` and the `result` (`closed`,
`reopened`, `archived`, `unarchived`, `moved`, or `deleted`). The audit event of
a confirmed change names the tool, the connection, the confirmation, the result, and the time, never its
arguments, the token, or a Todoist answer.

## Filters

`todoist.tasks.list` accepts structured filters only: `project_id`, `section_id`, `parent_id`, and `label`
are passed to Todoist, and the due window `due_from` and `due_to` (inclusive `YYYY-MM-DD` dates compared with
the date part of a task's due date) or `without_due` is applied by Qatlas. A task without a due date never
matches a due window.

A Todoist filter expression, such as `today | overdue` or the query of a saved filter, is accepted only by
`todoist.tasks.filter` as `query` (at most 1024 characters, no control characters), and for completed tasks
by `todoist.completedtasks.list` as `query`. Their results are held to the connection's projects like every
other list, so an expression that names another project returns nothing of it.

`todoist.completedtasks.list` reads the tasks completed from `since` (inclusive) until `until` (exclusive),
both RFC 3339 and at most three months apart, optionally narrowed by `project_id`, `section_id`, and a
`query` that Todoist supports for completed tasks. The narrowing is passed to Todoist on every page, and the
cursor is bound to it.

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
