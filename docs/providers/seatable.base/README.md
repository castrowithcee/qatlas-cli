---
description: >
  Describes SeaTable Base credential, table binding, target selection, shared boundaries, permissions, tool selection, and the setup profile; indexes the area pages.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-22
---

# SeaTable Base

A credential is one API token for one base. A connection binds it to one table through `target`, to an
explicit table allow-list through `targets`, or to every table through the explicit `target: "*"` scope.
An empty target never means the whole base. A fixed table may include a view after `/`; identifiers use
`id:TABLEID` and `id:TABLEID/id:VIEWID`.

```sh
qatlas invoke seatable.base.tables.list --connection sales-all-tables
qatlas invoke seatable.base.columns.list --connection sales-all-tables --arg table=id:0000
qatlas invoke seatable.base.rows.list --connection sales-all-tables --arg table=id:0000 --arg limit=25
```

## Areas

| Page | Tools |
| --- | --- |
| [Rows](rows.md) | `rows.search`, `rows.batch-*`, `snapshots.create` |
| [Links](links.md) | `links.*` |
| [Views](views.md) | `views.*` |
| [Schema](schema.md) | `tables.*`, `columns.*`, column options |
| [History](history.md) | `rows.activities`, `base.operations` |
| [Comments and collaborators](comments.md) | `comments.*`, `collaborators.list` |
| [Cell files](files.md) | `files.*` |

## Choosing targets

A target is a table (`TABLE`, `id:TABLEID`, or `*` for the whole base) or a view of a table (`TABLE/VIEW`,
`id:TABLEID/id:VIEWID`). The selection lists the tables of the base and the views of one table from the base
metadata, without the connection's targets, and writes the `id:` forms because names can be renamed. Names are
shown as SeaTable reports them.

The address of a base in the web interface, `/workspace/WORKSPACE_ID/dtable/BASE_NAME/?tid=TABLE_ID&vid=VIEW_ID`,
names a table and a view when its host is the host of the connection's base URL and workspace and base name are
those of the connection's base; the table and view must exist in it. Any other address names nothing.

Tools that change a table's structure (rename, delete, columns, options, create view) need a connection with a
table target or `*`; a connection with view targets only does not reach them. The operation log and creating or
duplicating a table need `*`.

## Shared boundaries

These rules hold for every tool; the area pages refer here.

- Every tool that takes `table` accepts a name or `id:TABLEID` exactly as the connection allows it. A table
  outside the allow-list, a selection narrowed to one view where the tool does not accept it, or a malformed
  request is refused before the credential is read and before SeaTable is contacted. The refusal never names
  the rejected table, row, or view. Where a tool must resolve the table, a view, a column, or a row first,
  the token exchange and that one read are the only provider access that can precede a further refusal.
- Link columns in row output follow the table boundary. A link entry into a table outside the connection
  allow-list, or into a table that cannot be determined from the base metadata, is reduced to its `row_id`
  and carries no `display_value`. A wildcard connection keeps the values. This applies to
  `seatable.base.rows.list` and `seatable.base.rows.get`, whichever way the table is addressed. Lookup and
  formula columns are returned as the provider computes them. The base metadata is read once and reused.
  The area pages state their own forms of this rule.
- A change needs the matching effect (`create`, `update`, `delete`) and `confirm`. Each change sends exactly one
  request (a multi-step tool, one per step) and is never repeated. A timeout, a dropped connection, a 5xx
  answer, or an unreadable answer is reported as uncertain: read the affected objects before repeating the
  change.
- Errors never carry provider text. An error SeaTable reports is reported in Qatlas's own words.
- Answers are limited to 1 MiB; strings in untrusted entries are shortened to 4096 bytes. Text from the base
  (names, filter terms, comments, history) is untrusted data.
- Mutations reject system-column names and never select a table outside the allow-list. A view narrows
  listing but does not redirect a mutation to another table.

## Permissions and tools

The credential provides `api-token`. Use provider permission `r` for read-only connections and `rw` when
writes are intended. The local selection is a separate ceiling and cannot turn an `r` token into a
writer. A connection selects by `effects` or by a `tools` list naming exactly the tools it offers, for example
`[seatable.base.rows.list]`; neither widens the table scope.
Qatlas exchanges the API token for a short-lived base token in memory. The terminal editor starts a new
connection on the setup profile `read`, which selects the tools `[seatable.base.tables.list,
seatable.base.columns.list, seatable.base.rows.list, seatable.base.rows.search, seatable.base.rows.get, seatable.base.links.list, seatable.base.views.list, seatable.base.views.get]`. A profile is a visible
starting selection, not a role: only the ticked `tools` are saved, every tick can be changed before saving, and a
saved connection never follows a profile.
