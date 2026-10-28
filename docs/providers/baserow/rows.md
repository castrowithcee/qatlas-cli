---
description: >
  Describes Baserow row reads with column selection, structured row search, single-row changes, and batch row changes.
type: knowledge
edit: shared
created: 2026-10-27
updated: 2026-10-28
---

# Rows

The table boundary is in [the README](README.md#scope), the bounds in [Bounds](README.md#bounds), and link
masking in [Links](links.md).

## Reading rows

`rows.list` and `rows.search` select columns with `include` and `exclude`. `rows.list` sends the names unchecked;
`rows.search` checks them against the schema it reads anyway. Link masking applies to the selected columns. Names with
a comma, with `__`, or with a leading `-` or `+` cannot be selected: such names would need the quoting of Baserow's
parameters, and Qatlas sends no quoted names. `{link_row_field}__join` is not offered because it reads values of other
tables through the link.

`rows.get` with `view_id` first reads the table's view list and rejects a view that is not in it; without it the call
sends one request. `include_metadata` adds `metadata` at no extra request.

## Searching rows

A filter or sort entry of `baserow.rows.search` names its field by `field` or by `field_id`, exactly one; the ID is
translated through the table's schema, and an unknown ID is rejected. Resolved names may not contain `__` or `,` or
begin with `-` or `+`. Filter types come from a fixed list; for most of them Baserow decides whether a value is
needed. Values travel only as URL-encoded query parameters, never as a passthrough. The API documents no format for
filter groups and a flat tree does no more than the offered filters, so the JSON filter tree is not offered.

Formula, rollup, and count fields are filterable and sortable. A lookup field through a link into a table that is not
reachable (outside the targets or excluded, or one that cannot be determined) is left out of the output and refuses any
filter and sorting, as does a full-text `search` on a table with one. A link field whose other table is not reachable
(or cannot be determined) allows only `empty`, `not_empty`, `link_row_has`, and `link_row_has_not`, and no
sorting. Display values of linked rows in such a table are masked; a text filter, a sort, or a full-text search on
them would disclose them. Filters by row ID or emptiness show only what the output shows anyway. For the same reason
`search` is refused on a table with such a link or lookup field unless the connection targets `*` without exclusions; with
that, or when the other table is reachable, nothing is refused.

Order of checks: the table (before the credential), argument shape, limits, column keys, and filter types (before any
request), then one read of the table's fields, after which an unknown or unsuitable field, a repeated field and type,
an unknown `include` or `exclude` name, or a refused search is rejected. Only then, with `view_id`, the table's view
list is read; a view that is not in it is rejected. The rows request follows last. The answer has the shape of
`rows.list`, keyed by field name, link-masked and bounded the same way.

## Row changes

Each change takes `table_id` (reachable), needs `confirm`, and sends exactly one request. A change is never
repeated by Qatlas: after a timeout, a connection reset, an unknown transport failure, an HTTP 5xx, or an unreadable
answer the error says the change may have taken effect, and the row should be read before it is tried again.
`rows.delete` runs only for a connection whose `tools` list names it.

- `rows.create` takes `fields`, an object of cell values keyed by field name (`{}` creates an empty row), and
  answers the new row like `rows.get`, with links masked.
- `rows.update` takes `row_id` and `fields` with at least one cell and changes only those; it answers the row.
- `rows.delete` takes `row_id` and answers `{"deleted": true, "row_id": N}`.
- `rows.move` takes `row_id` and optionally `before_id`, a row of the same table to place it before (the end
  of the table when omitted), and answers `{"moved": true, "row_id": N}`. Both rows belong to the table in
  the path; another table cannot be reached.

`rows.create` and `rows.update` read the table's fields first, the only provider request before a refusal
based on the schema. A field name that does not exist, a field flagged read-only, and the computed types
(formula, lookup, count, rollup, created on, last modified, created by, last modified by, autonumber, button,
AI) are refused without quoting the name. A link field takes only an array of row IDs (or primary-field
texts) and only when its other table is reachable; with `*` any table is allowed unless excluded, and a link field
whose other table cannot be determined is refused. The body is limited to 1 MiB and 500 cells. A missing
token right is reported as a `permission` error naming the right to grant (`create`, `update`, or `delete`).

## Batch changes

The batch tools take `table_id` and apply the rules of the row changes: confirmation, the table boundary before
the credential, exactly one change request, no retry, and the uncertainty message. `batch-delete` runs only for a
connection whose `tools` list names it.

- `rows.batch-create` takes `rows`, 1 to 200 objects of cell values keyed by field name, and answers
  `{"rows": [...], "count": N}` with the new rows in request order, links masked.
- `rows.batch-update` takes `rows`, 1 to 200 objects with `row_id` and `fields` (at least one cell), and answers
  like `batch-create`. Row IDs must be distinct; a field named `id` is refused because the batch body uses `id`
  for the row.
- `rows.batch-delete` takes `row_ids`, 1 to 200 distinct IDs, and answers `{"deleted": true, "count": N}`.

More than 200 rows, none, a repeated or non-positive row ID, and a body above 1 MiB are refused before any request.
Create and update read the table's fields once and check every row like the single-row tools; one refused row refuses
the whole batch without a change request. An answer whose item count differs from the request is an
`invalid-provider-response` with the uncertainty message. Baserow applies a batch atomically as far as the
documentation says; Qatlas does not rely on it and reports the uncertainty after an unclear result.
