---
description: >
  Describes SeaTable schema discovery, table allow-lists, wildcard scope, row, link, and view operations, permissions, and token limits.
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

## Row links

`seatable.links.list`, `seatable.links.create`, `seatable.links.update`, and `seatable.links.delete` read
and change the links between rows of two tables through one link column.

- The agent passes `table` and `column` (name or key of a column of type `link`). Qatlas takes the link
  identifier and the joined table from the base metadata; neither is an argument.
- Both tables must be inside the connection allow-list. A wildcard connection allows every table; a
  counterpart table reached by name or by `id:` is checked the same way. A link column into a table outside
  the allow-list is refused without a link request, and the message does not name that table. The
  connection's own table is checked before the credential is read and before SeaTable is contacted. To find
  the counterpart table, Qatlas first exchanges the token and reads the base metadata; that is the only
  provider access that can precede a refusal.
- An unknown column, or a column that is not a link column, is refused before the link request.
- `seatable.links.list` takes 1 to 10 `row_ids`, `start` (0 to 10000), and `limit` (1 to 100, 25 by default)
  per source row. Each entry carries the `row_id` and, when SeaTable sends one, a `display_value` of up to
  4096 bytes. `has_more` is true when a source row returned a full page. It is part of the `read` profile.
- `seatable.links.create` adds links from one `row_id` to 1 to 50 `other_row_ids`. `seatable.links.update`
  replaces all links of that row in the column with the given 1 to 50 rows; an empty list is refused before
  the credential is read, because removing links is what `seatable.links.delete` is for.
  `seatable.links.delete` removes the listed links and is offered only by a connection whose `tools` list
  names it.
- Row identifiers are 22 characters of letters, digits, `-`, or `_`, and must not repeat in one call.
- The three changes need the matching permission (`create`, `update`, `delete`) and `confirm`. Each sends
  exactly one request and is never repeated. A timeout, a dropped connection, a 5xx answer, or an unreadable
  answer is reported as uncertain: read the links before repeating the change. An error SeaTable reports,
  for example a duplicate link, is reported without the provider text.

```sh
qatlas invoke seatable.links.list --connection sales-all-tables --arg table=id:0000 --arg column=Tickets \
  --arg row_ids='["Qtf7xPmoRaiFyQPO1aENTj"]'
qatlas invoke seatable.links.create --connection sales-rw --confirm --arg table=id:0000 --arg column=Tickets \
  --arg row_id=Qtf7xPmoRaiFyQPO1aENTj --arg other_row_ids='["Ab12Cd34Ef56Gh78Ij90Kl"]'
```

## Views

`seatable.views.list`, `seatable.views.get`, `seatable.views.create`, `seatable.views.update`, and
`seatable.views.delete` manage the views of an allowed table. `views.list` and `views.get` are part of the
`read` profile; `views.delete` is in no profile and is offered only by a connection whose `tools` list
names it.

- The agent passes `table` (as for rows) and, for `get`, `update`, and `delete`, `view`: a view name or
  `id:VIEWID`, as returned by `views.list`. Qatlas resolves the table and the view in the base metadata and
  sends the table and view names to SeaTable; a view that the table does not hold, including a view of another
  table, is refused. The token exchange and the metadata read are the only provider access that can precede
  such a refusal; a table outside the allow-list, or a malformed request, is refused before the credential is
  read and before SeaTable is contacted.
- A connection target that names a view, such as `Kunden/Aktive`, narrows the views tools to that view:
  `views.list` lists only it, `views.get` refuses every other view, and `views.create` is refused. The
  message never names another table or view.
- A view that is configured as a connection target cannot be updated or deleted, whichever way the
  selection addresses it (name or `id:`).
- `views.list` returns up to 200 views with identifier, name, type, filter conjunction, filters, sorts, and
  hidden columns. Columns are referred to by key. Filter terms are data from the base and untrusted.
- `views.create` makes an empty view from a `name` (up to 255 printable characters, no `/`). Filters, sorts,
  and hidden columns are set afterwards with `views.update`.
- `views.update` renames a view (`name`) and replaces `filters` (up to 10: `column`, lowercase `predicate`,
  optional scalar `term`), `filter_conjunction` (`and` or `or`), `sorts` (up to 3: `column`, `direction`),
  and `hidden_columns` (up to 100). Only the given parts change. Every column is checked against the table
  metadata, may be named by name or key, and is sent as its key; an unknown column is refused before the
  request. An empty update is refused.
- The three changes need the matching permission (`create`, `update`, `delete`) and `confirm`. Each sends
  exactly one request and is never repeated. A timeout, a dropped connection, a 5xx answer, or an unreadable
  answer is reported as uncertain: read the view before repeating the change. An error SeaTable reports is
  reported without the provider text.

```sh
qatlas invoke seatable.views.list --connection sales-all-tables --arg table=id:0000
qatlas invoke seatable.views.update --connection sales-rw --confirm --arg table=id:0000 --arg view=Aktive \
  --arg hidden_columns='["Notiz"]'
```

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
seatable.columns.list, seatable.rows.list, seatable.rows.search, seatable.rows.get, seatable.links.list, seatable.views.list, seatable.views.get]`. A profile is a visible
starting selection, not a role: only the ticked `permissions` and `tools` are saved, every tick can be changed before saving, and a
saved connection never follows a profile.
