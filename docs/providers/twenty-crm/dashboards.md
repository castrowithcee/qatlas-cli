---
description: >
  Describes the Twenty CRM tools that read dashboard layouts and change their tabs and widgets.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-26
---

# Twenty CRM: dashboards

## Dashboards

`page-layouts.list` and `page-layouts.get` (`read`) are workspace-wide and read dashboard layouts. Other layout
types and trashed layouts are unknown to both. Titles and widget settings are untrusted data of their own data
class. Widget `configuration`, `conditionalDisplay`, and `conditionalAvailabilityExpression` are free-form
objects: each is limited to 8 levels and 16 KiB serialized, and one beyond the limit is left out and named in
`omitted_fields`, never shortened. Identifiers in them are not resolved.

The address of an embedded page (`configuration.url` of an IFRAME widget) can grant access without signing in,
so it counts as an access value and is not output; only its origin appears. The same holds for every other
address with a query, fragment, or credentials in a setting: the value holding it is removed. Deprecated fields
(`isOverridden`, `gridPosition`) are not output.

On the object `dashboard`, `records.create` and `records.batch-create` without a layout identifier also create a
layout with one tab; `records.destroy` also destroys its layouts, `delete` leaves them.

The tab and widget write tools are confirmed and need a `tools` entry; no profile ticks them. Rights:
[Dashboards](setup.md#dashboards). Each reads first and refuses what `get` would not read. Tabs are empty grid
tabs; a widget is Markdown, a custom workspace TABLE view (no index, standard, or personal view), or a chart
(aggregate, bar, line, pie), which also reads the object schema ([Data model](data-model.md)); its fields are
active non-system fields fit for their role, filters work as on views. `update` replaces a chart's whole
configuration, the object stays. `delete` is permanent and takes a tab's widgets; no tool deletes a layout.
