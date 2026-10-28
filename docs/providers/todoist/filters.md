---
description: >
  Describes the structured and expression filters of the Todoist task and completed-task reads.
type: knowledge
edit: shared
created: 2026-10-28
updated: 2026-10-28
---

# Todoist filters

Configuration, scope, targets, error classes, and cross-group limits are in [README.md](README.md).

`todoist.tasks.list` accepts structured filters only. The ID, project, section, parent, and label filters are
passed to Todoist; the due window (inclusive dates compared with the date part of a task's due date) or
`without_due` is applied by Qatlas. A task without a due date never matches a due window.

The filters name tasks, never reach: `ids`, `parent_id`, and `workspace_id` widen nothing, and every returned
task is held to the connection's targets, so a task named in `ids` that lies outside them is dropped.

`todoist.projects.list` takes `workspace_id` and `folder_id`, not with `search`; `todoist.sections.list` takes
`workspace_id`, also with `search`, not with `project_id` or on a one-project connection. They widen nothing.

A Todoist filter expression, such as `today | overdue` or the query of a saved filter, is accepted only by
`todoist.tasks.filter` as `query`, and for completed tasks by `todoist.completed-tasks.list` as `query`. Their
results are held to the connection's projects like every other list, so an expression that names another
project returns nothing of it. The language of an expression is named by `lang` or, for completed tasks, by
`filter_lang`, which is refused without `query`.

`todoist.completed-tasks.list` reads the tasks completed from `since` (inclusive) until `until` (exclusive),
both RFC 3339 and at most three months apart. Its narrowing is passed to Todoist on every page, and the cursor
is bound to it.

