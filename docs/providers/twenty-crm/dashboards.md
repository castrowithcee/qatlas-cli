---
description: >
  Describes the Twenty CRM tools that read dashboard layouts and change their tabs and widgets.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Twenty CRM: dashboards

## Dashboards

`page-layouts.list` and `page-layouts.get` (`read`) are workspace-wide and read dashboard layouts with tabs and
widgets. Other layout types and trashed layouts are unknown to both. A widget shows only type, title, position,
object, and chart type (`unknown` if unknown); an embedded page shows only its origin. Titles are untrusted data
of their own data class. On the object `dashboard`, `records.create` and `records.batch-create` without a layout
identifier also create a layout with one tab; `records.destroy` also destroys its layouts, `delete` leaves them.

The tab and widget write tools are confirmed, need a `tools` entry and the right "Layouts"; no profile ticks
them. Each reads first and refuses what `get` would not read. Tabs are empty grid tabs; a widget is Markdown, a
custom workspace TABLE view (no index, standard, or personal view), or a chart (aggregate, bar, line, pie), which
also reads the object schema ([Data model](data-model.md)); its fields are active non-system fields fit for their role, filters
work as on views. `update` replaces a chart's whole configuration, the object stays. `delete` is permanent and
takes a tab's widgets; no tool deletes a layout.
