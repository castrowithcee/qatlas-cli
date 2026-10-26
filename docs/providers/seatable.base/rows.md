---
description: >
  Describes SeaTable Base structured row search, batch row changes, and snapshots.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Rows

The link boundary, the table boundary, and the change rules are in [the README](README.md#shared-boundaries).

## Row search

`seatable.base.rows.search` filters, sorts, and pages the rows of an allowed table with structured arguments.
It is the way to reach rows beyond the 10000-row window of `seatable.base.rows.list`: `start` accepts offsets up
to 100000. Free SQL is not accepted. Qatlas builds exactly one statement shape from validated parts and
sends every value in `parameters`, never inside the statement text:

```sql
SELECT * FROM `Table` WHERE `col` = ? AND `col2` IN (?, ?) ORDER BY `col` DESC LIMIT 25 OFFSET 0
```

- Filters are combined with `AND`. `like` needs a text value, `is_null` takes no value, and `in` takes
  `values`.
- Without a sort the order of rows is not defined, so paging with `start` should sort by a stable column
  such as `_id` or `_ctime`.
- A page is full when it holds `limit` rows; `has_more` and `next_start` follow the same rule as
  `seatable.base.rows.list`.
- Columns are checked against the table metadata before the query is sent. The system columns `_id`,
  `_ctime`, and `_mtime` are accepted. Names containing a backtick or backslash, unknown columns, and link
  columns are refused.
- Link columns are recognised by their type in the base metadata. A link value that does not have the
  expected form of entries with a `row_id` is left out of the row; the general masking of the README then
  applies in addition.
- The search covers the whole table. A view on the connection target, such as `Kunden/Aktive`, narrows
  `seatable.base.rows.list` but is not applied to a search.

```sh
echo '{"table":"id:0000","filters":[{"column":"Name","op":"like","value":"Bike%"}],"sort":[{"column":"_ctime","direction":"desc"}],"limit":25}' \
  | qatlas invoke seatable.base.rows.search --connection sales-all-tables
```

## Batch rows and snapshots

`seatable.base.rows.batch-create`, `seatable.base.rows.batch-update`, `seatable.base.rows.batch-delete`, and
`seatable.base.snapshots.create` change several rows in one confirmed call and create a restore point before a
mass change. None of them is in a profile. `batch-delete` is offered only by a connection whose `tools` list
names it.

- One call holds 1 to 100 rows and at most 1 MiB. Both limits, the row identifiers (22 characters), and
  duplicate identifiers are checked before the credential is read and before SeaTable is contacted.
- Every column of every row is checked against the table metadata, because SeaTable silently ignores unknown
  columns. System and empty column names and link columns are refused; link columns are changed with the
  [link tools](links.md). The metadata read is the only provider access that can precede such a refusal.
- SeaTable reports no failure per row, so the answer names `requested` (rows in the call) and, when SeaTable
  gives one, `reported` (rows it reported as created or deleted). An update has no reported count. Compare
  the counts and read the rows when they differ.
- `snapshots.create` asks SeaTable for a snapshot of the whole base of the credential, not of one table, and
  needs `create` and `confirm`. SeaTable creates a snapshot only when the base changed since the last one
  and at least 10 minutes have passed; otherwise Qatlas reports that rule in its own words, and SeaTable's
  answer goes along as the original statement. A 5xx answer or a timeout is reported as uncertain: check
  the snapshots of the base before repeating it.

```sh
qatlas invoke seatable.base.snapshots.create --connection sales-rw --confirm
echo '{"table":"id:0000","rows":[{"row_id":"Qtf7xPmoRaiFyQPO1aENTj","values":{"Status":"closed"}}]}' \
  | qatlas invoke seatable.base.rows.batch-update --connection sales-rw --confirm
```
