---
description: >
  Describes the Twenty CRM tools that read and change views and their columns, sorts, filters, and groups.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Twenty CRM: views

## Views

`views.list` and `views.get` (`read`) are workspace-wide and read a view's columns, filters, filter groups,
sorts, and groups. Only views visible to the whole workspace are read; `get` answers a personal or widget view
like an unknown one. Filter and group values are untrusted workspace data of their own data class.

`views.create`, `update`, `delete`, `view-fields.*`, `view-sorts.*`, `view-filters.*`, `view-groups.update`, and
`delete` are confirmed, need a `tools` entry and the Twenty right "Views" (also for other users' personal views;
use a key of its own); no profile ticks them. Each first reads its view and refuses what `get` would not read and
standard and index views. `update` changes name, icon, and the grouping field of a TABLE or KANBAN view (active
SELECT only; Twenty recreates all groups, losing visibility and order); `view-groups.update` changes visibility
and position. New views are workspace-wide and empty; `delete` is permanent. Column, sort, and filter `create`
refuse a foreign, inactive, or system field, column and sort also an unsortable or used one. Filters are simple
(no group, relation, or sub field) and checked by field type per the tool help; their `update` keeps the field.
