---
description: >
  Describes SeaTable schema discovery, table allow-lists, wildcard scope, row, link, view, table change, and history operations, permissions, and token limits.
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

## Column options

`seatable.columns.optionsadd`, `seatable.columns.optionsupdate`, and `seatable.columns.optionsdelete` maintain
the options of single-select and multiple-select columns of an allowed table. They are in no profile.
`optionsdelete` is offered only by a connection whose `tools` list names it; all three need the matching
permission and `confirm`.

- The agent passes `table` (as for rows), `column` (name or key, resolved in the base metadata), and
  `options` (up to 50; `optionsadd`: `name`, optional `color` and `text_color` as `#RRGGBB`; `optionsupdate`:
  existing `name` plus `new_name`, `color`, or `text_color`) or, for `optionsdelete`, `names` (up to 50).
  Names have up to 255 printable characters and may not repeat within a call.
- Any other column type, an unknown column, an option that already exists on add, or an option that does
  not exist on update or delete is refused before the request. A table outside the allow-list, a selection
  narrowed to one view, or a malformed request is refused before the credential is read and before SeaTable
  is contacted; the token exchange and the metadata read are the only provider access that can precede
  another refusal. The message never names another table.
- Deleting an option clears the cells that hold it.
- Each change sends exactly one request and is never repeated. A timeout, a dropped connection, a 5xx answer,
  or an unreadable answer is reported as uncertain: list the columns before repeating the change. An error
  SeaTable reports is reported without the provider text.

```sh
qatlas invoke seatable.columns.optionsadd --connection sales-rw --confirm --arg table=id:0000 \
  --arg column=Status --arg options='[{"name":"Offen","color":"#FFE9A8"}]'
```

## Columns

`seatable.columns.create`, `seatable.columns.update`, and `seatable.columns.delete` create, change, and remove
the columns of an allowed table. They are in no profile. `columns.delete` is offered only by a connection whose
`tools` list names it; all three need the matching permission and `confirm`.

- `columns.create` takes `table`, `name`, `type`, optional `data`, and optional `after` (name or key of the
  column the new one follows; at the end by default). A name has 1 to 255 printable characters, no `.`, `{`,
  `}`, or backtick, no surrounding blanks, is not a system column name, and does not exist yet.
- `type` is one of `text`, `long-text`, `number`, `date`, `duration`, `single-select`, `multiple-select`,
  `collaborator`, `image`, `file`, `email`, `checkbox`, `rate`, `creator`, `ctime`, `last-modifier`, `mtime`,
  and `link`. Formula, button, auto-number, geolocation, and URL columns are not offered.
- `data` has one bounded form per type and nothing else is accepted: `number` takes `format`, `decimal`,
  `thousands`; `date` takes `format`; `duration` takes `duration_format`; `rate` takes `rate_max_number`,
  `rate_style_color`, `rate_style_type`; the select types take `options` (up to 50: `name`, optional `color`
  and `text_color` as `#RRGGBB`); `link` takes `link_table`. Omitted settings take fixed defaults.
- `columns.update` takes `table`, `column` (name or key), and exactly one of `name` (rename), `type` with
  `data` (change the type; existing cell values may be lost), `width` (50 to 1000), or `target` (name or key of
  the column whose position it takes). One call sends one change.
- A link column is created or set only when the joined table is inside the connection's allow-list. An
  existing link column whose joined table is outside it can be neither changed nor deleted. The message never
  names the other table.
- System columns, unknown columns, a name that already exists, an unsupported type, or data that does not fit
  the type are refused before the request. A table outside the allow-list, a selection narrowed to one view,
  or a malformed request is refused before the credential is read and before SeaTable is contacted; the token
  exchange and the metadata read are the only provider access that can precede another refusal.
- `columns.delete` removes the column with its cell values.
- Each change sends exactly one request and is never repeated. A timeout, a dropped connection, a 5xx answer,
  or an unreadable answer is reported as uncertain: list the columns before repeating the change. An error
  SeaTable reports is reported without the provider text.

```sh
qatlas invoke seatable.columns.create --connection sales-rw --confirm --arg table=id:0000 \
  --arg name=Status --arg type=single-select --arg data='{"options":[{"name":"Offen"}]}'
qatlas invoke seatable.columns.update --connection sales-rw --confirm --arg table=id:0000 \
  --arg column=Status --arg name=Stand
```

## Tables

`seatable.tables.create`, `seatable.tables.rename`, `seatable.tables.duplicate`, and `seatable.tables.delete`
manage the tables of the base. They are in no profile. `tables.delete` is offered only by a connection whose
`tools` list names it; all four need the matching permission and `confirm`.

- A change never widens the table boundary of a connection. A new or duplicated table is not in an
  allow-list, so `tables.create` and `tables.duplicate` work only on a connection with `target: "*"`; any
  other connection refuses them before the credential is read and before SeaTable is contacted.
- `tables.create` makes an empty table from a `name`. `tables.duplicate` copies the table given by `table`,
  with its rows only when `with_rows` is true. The copy is named by SeaTable; `tables.list` shows it.
- `tables.rename` takes `table` and the new `name`. It is refused when the table is a connection target by
  name, such as `Kunden`; a table bound as `id:TABLEID` can be renamed. The new name may not be the name of
  an allow-list entry. Qatlas reads the base metadata to see whether the table is also bound by name; that
  read is the only provider access that can precede such a refusal.
- `tables.delete` removes the table with its rows and views. It works on a table in the allow-list, or on any
  table with `*`.
- `table` is a name or `id:TABLEID` exactly as the connection allows it; a table outside the allow-list, or a
  selection narrowed to one view, is refused and never named in the message. A new name has 1 to 255
  printable characters, no `/`, no surrounding blanks, no `id:` prefix, and is not `*`.
- Each change sends exactly one request and is never repeated. A timeout, a dropped connection, a 5xx
  answer, or an unreadable answer is reported as uncertain: list the tables before repeating the change. An
  error SeaTable reports is reported without the provider text.

```sh
qatlas invoke seatable.tables.create --connection sales-all-tables --confirm --arg name=Angebote
qatlas invoke seatable.tables.rename --connection sales-rw --confirm --arg table=id:0001 --arg name=Faelle
```

## Change history

`seatable.rows.activities` reads the change history of one row and `seatable.base.operations` reads the
operation log of the whole base. Both are read-only and in no profile, because old and new values and the
people behind changes are sensitive. Their sensitivity class is `seatable-base-history`; values of this class
never enter the audit trail or the invoke log.

- `rows.activities` takes `row_id`, `table` (as for rows), `page` (1 to 1000, 1 by default), and `per_page`
  (1 to 100, 25 by default). The table must be inside the connection allow-list and the row must exist in
  it: Qatlas reads the row in the selected table before it asks for the history, so a row of another table,
  or a deleted row, yields no history. A table outside the allow-list or a malformed request is refused
  before the credential is read and before SeaTable is contacted. The token exchange and the row read are the
  only provider access that can precede a refusal.
- Activities may hold values of linked rows. For a connection with an allow-list, a link value whose linked
  table is outside it, or cannot be determined from the base metadata, is reduced to the `row_id` of its
  entries. A wildcard connection keeps the values. Text that SeaTable composes into a single string cannot be
  inspected and is reported as SeaTable sends it.
- Only entries of the requested row are reported: an entry with a `row_id` other than the requested one, or
  with a `table_id` or `table_name` other than the selected table, is left out; entries without these fields
  stay, because the request names the row. Reading the table identifiers of the metadata is then the only
  additional provider access.
- `base.operations` takes only `page` (1 to 1000). It covers the whole base and is refused before the
  credential is read for every connection that is not bound to `*`.
- Entries are reported in `items` as SeaTable sends them, as untrusted data. Strings are shortened to 4096
  bytes, nesting beyond 8 levels is dropped, one call reports at most 100 entries, and the answer is limited
  to 1 MiB. `has_more` is true when an `activities` page is full, or when `operations` had more entries than
  one call reports. Errors never carry provider text.

```sh
qatlas invoke seatable.rows.activities --connection sales-all-tables --arg table=id:0000 \
  --arg row_id=Qtf7xPmoRaiFyQPO1aENTj
qatlas invoke seatable.base.operations --connection sales-all-tables --arg page=1
```

## Comments and collaborators

`seatable.comments.list`, `seatable.comments.create`, `seatable.comments.delete`, and
`seatable.collaborators.list` work with the comments of one row and with the people of the base. None of
them is in a profile. `comments.delete` is offered only by a connection whose `tools` list names it.

- All three comment tools take `row_id` and `table` (as for rows). The table must be inside the connection
  allow-list and the row must exist in it: Qatlas reads the row in the selected table before any comment
  route is used, so a row of another table, or a deleted row, gives no access. A table outside the
  allow-list or a malformed request is refused before the credential is read and before SeaTable is
  contacted; the refusal does not name the rejected table or row.
- `comments.list` reports `id`, `author`, `comment`, `created_at`, `updated_at`, and `resolved` of the
  comments of the row as untrusted data, at most 100 per call with `has_more`; entries that name another row
  are left out. Comments and their authors are classified `seatable-base-people`.
- `comments.create` takes `comment` (1 to 4096 bytes of text, not blank) and sends exactly one request that
  adds it as the user of the API token. It needs `confirm`.
- `comments.delete` takes `comment_id`. Qatlas first lists the comments of the requested row and deletes only
  a comment found there; any other identifier is refused without a delete request. It needs `confirm`.
- A change is never repeated. After a timeout, a connection reset, a 5xx status, or an unreadable answer the
  error says the change may have taken effect; list the comments of the row before repeating it.
- `collaborators.list` takes no arguments and reports `name`, `email`, and `contact_email` of the people of
  the whole base, at most 500 with `has_more`; the avatar address is left out. It is not bound to the tables
  of the connection: every connection to the base may read all collaborators, so offer it only where that is
  intended. The sensitivity class `seatable-base-people` covers names and mail addresses; values of this
  class never enter the audit trail or the invoke log.
- Every answer is limited to 1 MiB, strings are shortened to 4096 bytes, and errors never carry provider
  text. Comments across the whole base are not offered.

```sh
qatlas invoke seatable.comments.list --connection sales-all-tables --arg table=id:0000 \
  --arg row_id=Qtf7xPmoRaiFyQPO1aENTj
qatlas invoke seatable.comments.create --connection sales-rw --confirm --arg table=id:0000 \
  --arg row_id=Qtf7xPmoRaiFyQPO1aENTj --arg comment="Please check this row"
qatlas invoke seatable.collaborators.list --connection sales-all-tables
```

## Batch rows and snapshots

`seatable.rows.batchcreate`, `seatable.rows.batchupdate`, `seatable.rows.batchdelete`, and
`seatable.snapshots.create` change several rows in one confirmed call and create a restore point before a
mass change. None of them is in a profile. `batchdelete` is offered only by a connection whose `tools` list
names it.

- One call holds 1 to 100 rows and at most 1 MiB. Both limits, the row identifiers (22 characters), and
  duplicate identifiers are checked before the credential is read and before SeaTable is contacted.
- `batchcreate` takes `rows`, a list of column-name-to-value objects. `batchupdate` takes `rows`, a list of
  `row_id` plus `values`. `batchdelete` takes `row_ids`. The `table` argument works as for single rows and
  cannot leave the connection allow-list; a table outside it is refused before the credential is read.
- Every column of every row is checked against the table metadata, because SeaTable silently ignores unknown
  columns. System and empty column names and link columns are refused; link columns are changed with the
  link tools. The metadata read is the only provider access that can precede such a refusal.
- Each change needs the matching permission (`create`, `update`, `delete`) and `confirm`, sends exactly one
  request, and is never repeated. A timeout, a dropped connection, a 5xx answer, or an unreadable answer is
  reported as uncertain: read the rows before repeating the change. SeaTable reports no failure per row, so
  the answer names `requested` (rows in the call) and, when SeaTable gives one, `reported` (rows it
  reported as created or deleted). An update has no reported count. Compare the counts and read the rows
  when they differ.
- `snapshots.create` asks SeaTable for a snapshot of the whole base of the credential, not of one table, and
  needs `create` and `confirm`. SeaTable creates a snapshot only when the base changed since the last one
  and at least 10 minutes have passed; otherwise Qatlas reports that rule in its own words and never the
  provider text. A 5xx answer or a timeout is reported as uncertain: check the snapshots of the base before
  repeating it.

```sh
qatlas invoke seatable.snapshots.create --connection sales-rw --confirm
echo '{"table":"id:0000","rows":[{"row_id":"Qtf7xPmoRaiFyQPO1aENTj","values":{"Status":"closed"}}]}' \
  | qatlas invoke seatable.rows.batchupdate --connection sales-rw --confirm
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
