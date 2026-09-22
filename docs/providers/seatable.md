---
description: >
  Describes SeaTable schema discovery, table allow-lists, wildcard scope, row operations, permissions, and token limits.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-09-13
---

# SeaTable

A credential is one API token for one base. A connection binds it to one table through `target`, to an
explicit table allow-list through `targets`, or to every table through the explicit `target: "*"` scope.
An empty target never means the whole base. A fixed table may include a view after `/`; identifiers use
`id:TABLEID` and `id:TABLEID/id:VIEWID`.

`seatable.tables.list` discovers the tables visible through the connection and returns the stable reference
accepted by other operations. `seatable.columns.list` returns one bounded page of column keys, names, and
types. A single-table connection needs no table argument. An allow-list or wildcard connection requires the
`table` argument returned by table discovery for column and row operations.

```sh
qatlas invoke seatable.tables.list --connection sales-all-tables
qatlas invoke seatable.columns.list --connection sales-all-tables --arg table=id:0000
qatlas invoke seatable.rows.list --connection sales-all-tables --arg table=id:0000 --arg limit=25
```

The connection can list and read rows (`read`) and create, update, or delete rows with the matching
permission. Mutations require confirmation, reject system-column names, and are bounded to 1 MiB. They can
never select a table outside the connection allow-list. A view narrows listing but does not redirect a
mutation to another table.

The credential provides `api-token`. Use provider permission `r` for read-only connections and `rw` when
writes are intended. The local `permissions` list is a separate ceiling and cannot turn an `r` token into a
writer. Qatlas exchanges the API token for a short-lived base token in memory.
