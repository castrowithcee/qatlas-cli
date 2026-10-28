---
description: >
  Describes how Todoist changes are confirmed, placed, retried, and audited.
type: knowledge
edit: shared
created: 2026-10-28
updated: 2026-10-28
---

# Todoist changes

Configuration, scope, targets, error classes, and cross-group limits are in [README.md](README.md).

Every change needs `--confirm` (or `confirm` over MCP) in its own request, on a connection that
offers it, by its `effects` or by naming it in its `tools` list; otherwise it is refused before
a secret is read or Todoist is contacted. Moving, closing, reopening, and deleting are tools of their own, so
an update never moves or completes a task, and a close never reopens one.

`todoist.tasks.create` takes `content` (one line, at most 500 characters) and places the task by
`project_id`, `section_id`, or `parent_id`; `section_id` and `parent_id` exclude each other, and a named
project must be the one of the section or parent. Without any of them the task goes to the connection's only
target, to the Inbox on a `*` connection without exclusions, and is refused on any other connection
with several targets.
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

## Exclusions

A change whose object, target, or destination lies in an excluded project, tree, or section is refused before
anything is sent, as for a project outside the targets: tasks, comments, and reminders, a project created below
an excluded parent, a section created in or moved into an excluded project, and `todoist.sections.reorder`.
`todoist.projects.archive` and `todoist.projects.delete` are refused when an excluded project or section lies
below the project at any depth. A task closed, moved, or deleted with its subtasks is checked on the task itself.

A new name for a label and a label delete reach every task that carries the label. On a connection with
exclusions, `todoist.labels.update` with `name` and `todoist.labels.delete` first read the label and its active
tasks (at most 25 pages of 200) and are refused when one lies in an excluded part or the list cannot be read
to its end. Completed tasks are not checked. A color or favorite change and `todoist.labels.create` and
`todoist.labels.reorder` are unchanged.

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

