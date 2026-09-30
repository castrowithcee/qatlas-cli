---
description: >
  Describes SeaTable schema discovery, table allow-lists, wildcard scope, row operations, permissions, and token limits.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-09-30
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
echo '{"table":"id:0000","filters":[{"column":"Name","op":"like","value":"Bike%"}],"sort":[{"column":"_ctime","direction":"desc"}],"limit":25}' \
  | qatlas invoke seatable.rows.search --connection sales-all-tables
```

## Row search

`seatable.rows.search` filters, sorts, and pages the rows of an allowed table with structured arguments.
It is the way to reach rows beyond the 10000-row window of `seatable.rows.list`: `start` accepts offsets up
to 100000. Free SQL is not accepted. Qatlas builds exactly one statement shape from validated parts and
sends every value in `parameters`, never inside the statement text:

```sql
SELECT * FROM `Table` WHERE `col` = ? AND `col2` IN (?, ?) ORDER BY `col` DESC LIMIT 25 OFFSET 0
```

- `filters` (up to 10, combined with `AND`): `column`, `op`, and `value`. The operators are `eq`, `ne`, `lt`,
  `lte`, `gt`, `gte`, `like`, `is_null`, and `in`. `like` needs a text value, `is_null` takes no value, and
  `in` takes `values` with 1 to 50 entries. A value is a string of up to 1024 characters, a number, or a
  boolean.
- `sort` (up to 3): `column` and `direction` `asc` (default) or `desc`. Without a sort the order of rows
  is not defined, so paging with `start` should sort by a stable column such as `_id` or `_ctime`.
- `limit` is 1 to 100 (25 by default). A page is full when it holds `limit` rows; `has_more` and
  `next_start` follow the same rule as `seatable.rows.list`.
- Columns are checked against the table metadata before the query is sent. The system columns `_id`,
  `_ctime`, and `_mtime` are accepted. Names containing a backtick or backslash, unknown columns, and link
  columns are refused.
- The search covers the whole table. A view on the connection target, such as `Kunden/Aktive`, narrows
  `seatable.rows.list` but is not applied to a search.
- The table must be inside the connection allow-list; anything else is refused before the credential is
  read and before SeaTable is contacted.
- Link values in the result follow the same boundary as above. Link columns are recognised by their type in
  the base metadata. For a table outside the allow-list, a link value keeps only the `row_id` of its
  entries; a link value that does not have the expected form of entries with a `row_id` is left out of the
  row.

A client reads the base metadata once and reuses it for column checks, link masking, and searches.

Link columns in row output follow the table boundary. When the linked table is outside the connection
allow-list, or cannot be determined from the base metadata, each link entry is reduced to its `row_id` and
carries no `display_value`. A wildcard connection keeps display values. The same applies to `seatable.rows.list`
and `seatable.rows.get`, whichever way the table is addressed. Lookup and formula columns are returned as the
provider computes them.

The connection can list and read rows (`read`) and create, update, or delete rows with the matching
permission. Mutations require confirmation, reject system-column names, and are bounded to 1 MiB. They can
never select a table outside the connection allow-list. A view narrows listing but does not redirect a
mutation to another table.

The credential provides `api-token`. Use provider permission `r` for read-only connections and `rw` when
writes are intended. The local `permissions` list is a separate ceiling and cannot turn an `r` token into a
writer. An optional `tools` list narrows a connection further to named tools, for example
`[seatable.rows.list]`; it never admits an effect `permissions` excludes and never widens the table scope.
Qatlas exchanges the API token for a short-lived base token in memory. The terminal editor starts a new
connection on the setup profile `read`, which ticks `[read]` and `[seatable.tables.list,
seatable.columns.list, seatable.rows.list, seatable.rows.search, seatable.rows.get]`. A profile is a visible
starting selection, not a role: only the ticked `permissions` and `tools` are saved, every tick can be changed before saving, and a
saved connection never follows a profile.
