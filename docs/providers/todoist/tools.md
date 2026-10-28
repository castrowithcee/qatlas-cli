---
description: >
  Describes the Todoist read tools, the confirmed change tools with their effects and retry behaviour, and the setup profiles.
type: knowledge
edit: shared
created: 2026-10-28
updated: 2026-10-28
---

# Todoist tools

Configuration, scope, targets, error classes, and cross-group limits are in [README.md](README.md).

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

Labels belong to the account, not to a project: `todoist.labels.list` lists them whatever the connection
excludes. Renaming or deleting a label is checked against the exclusions as described under
[changes](changes.md#exclusions).

"Any" means every binding that reaches the tool, see [Scope](README.md#scope). A tool marked listed only takes more
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

