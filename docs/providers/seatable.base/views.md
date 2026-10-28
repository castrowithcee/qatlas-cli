---
description: >
  Describes SeaTable Base view tools, view targets, and view changes.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-28
---

# Views

`seatable.base.views.list`, `seatable.base.views.get`, `seatable.base.views.create`, `seatable.base.views.update`, and
`seatable.base.views.delete` manage the views of an allowed table. `views.list` and `views.get` are part of the
`read` profile; `views.delete` is in no profile and is offered only by a connection whose `tools` list
names it. The shared rules are in [the README](README.md#shared-boundaries).

- The agent passes `table` (as for rows) and, for `get`, `update`, and `delete`, `view`: a view name or
  `id:VIEWID`, as returned by `views.list`. Qatlas resolves the table and the view in the base metadata and
  sends the table and view names to SeaTable; a view that the table does not hold, including a view of another
  table, is refused. The token exchange and the metadata read are the only provider access that can precede
  such a refusal. An excluded view is treated like a view the table does not hold, and `views.list` omits it;
  an excluded table is refused. An exclusion claims the name of a view by name: `views.create` and
  `views.update` cannot give a view that name.
- A connection target that names a view, such as `Kunden/Aktive`, narrows the views tools to that view:
  `views.list` lists only it, `views.get` refuses every other view, and `views.create` is refused.
- A view that is configured as a connection target cannot be updated or deleted, whichever way the
  selection addresses it (name or `id:`).
- Columns are referred to by key in `views.list`. Filter terms are data from the base and untrusted.
- `views.list` and `views.get` report the view settings SeaTable documents for their endpoint, among them
  groupings, color rules, summaries, and the locked state. Settings such as `groupbys` or `summaries` pass
  through as the lists and objects SeaTable returns; Qatlas does not interpret them.
- `formula_rows` and `link_rows` can hold values of linked tables. They appear only for a wildcard
  connection and, with exclusions, only for a table without a link to a table the connection does not
  reach; otherwise they are absent because of the table binding, and their absence is not listed in
  `omitted_fields`.
- A view setting that exceeds the size bounds (4096 bytes serialized, 8 levels, or 1000 elements) is left out,
  never shortened, and named in `omitted_fields`.
- `views.create` makes an empty view; filters, sorts, and hidden columns are set afterwards with
  `views.update`.
- `views.update` changes only the given parts. Every column is checked against the table metadata, may be
  named by name or key, and is sent as its key; an unknown column is refused before the request. An empty
  update is refused.
- Read the view before repeating an uncertain change.

```sh
qatlas invoke seatable.base.views.list --connection sales-all-tables --arg table=id:0000
qatlas invoke seatable.base.views.update --connection sales-rw --confirm --arg table=id:0000 --arg view=Aktive \
  --arg hidden_columns='["Notiz"]'
```
