---
description: >
  Describes the Baserow provider: the database token and Baserow Cloud or self-hosted base URL, the table
  allow-list target, the four read tools and the four row changes, schema checks, link-field masking and
  link values outside the allow-list, bounds, errors, uncertain results, and the documented limits of lookup
  and formula fields.
type: knowledge
edit: shared
created: 2026-09-30
updated: 2026-09-30
---

# Baserow

This provider reads tables, fields, and rows of a Baserow instance, Baserow Cloud or self-hosted, with a
database token, and creates, changes, deletes, and moves single rows of the tables a connection allows.

## Credential and base URL

The credential provides `database-token`: a Baserow database token, created in the account settings under
database tokens. A token belongs to one workspace and carries the rights `create`, `read`, `update`, and
`delete` per workspace, database, or table; reading needs `read`, and each row change needs its own right
(`create`, `update`, or `delete`; a move needs `update`). The token is sent as
`Authorization: Token ...` and is registered with the redactor.

`base_url` is `https://api.baserow.io` by default (Baserow Cloud). A self-hosted instance uses its own `https`
URL, optionally below an installation path; a URL with `http`, user info, a query, or a fragment is refused
before any secret is read. No redirect is followed, so the token never travels to another host.

```yaml
services:
  baserow-customer-a:
    provider: baserow
    base_url: https://api.baserow.io

credentials:
  baserow-customer-a-token:
    provider: baserow
    type: keyring
```

## Scope

A connection lists the tables it may read as targets: one or more `table/TABLE_ID` entries, or the single
entry `*` for every table the token reads. Entries are positive integers without a sign or leading zero.

```yaml
connections:
  customer-a-crm:
    service: baserow-customer-a
    credential: baserow-customer-a-token
    targets: [table/11, table/12]
```

A `table_id` outside the targets is refused as an invalid request before the credential is resolved and
before any request is sent; the refusal does not name the table. `baserow.tables.list` shows only the tables
of the targets when the connection has an allow-list. The target limits what Qatlas shows; it does not
narrow what the token itself may read, so grant the token only the rights the connection needs.

## Tools

| Tool | Effect | Confirmation | Does |
| --- | --- | --- | --- |
| `baserow.tables.list` | read | none | lists the tables of the targets with `id`, `name`, `database_id` |
| `baserow.fields.list` | read | none | lists one table's fields: `id`, `name`, `type`, `primary`, `read_only`, select options |
| `baserow.rows.list` | read | none | lists one table's rows page by page |
| `baserow.rows.get` | read | none | reads one row |
| `baserow.rows.search` | read | none | searches, filters, and sorts one table's rows page by page |

| `baserow.rows.create` | create | required | creates one row from cell values keyed by field name |
| `baserow.rows.update` | update | required | changes the named cells of one row |
| `baserow.rows.delete` | delete | required, tool allow-list | deletes one row |
| `baserow.rows.move` | update | required | moves one row within its table |

The read profile offers the five read tools. `rows.list` takes `table_id`, `page` (from 1; 1 when omitted), `size`
(1 to 200; 50 when omitted), and `user_field_names` (true when omitted: values keyed by field name; false:
keyed `field_ID`). It answers `rows` (`id`, `order`, `fields`), `count` (the table's total row count as
Baserow reports it), `page`, `size`, and `has_more`, which is true when Baserow reports a next page. It has
no filters, search, or sorting; `rows.search` does. `rows.get` takes `table_id`, `row_id`, and `user_field_names`.

## Searching rows

`baserow.rows.search` takes `table_id` (inside the targets), `page`, `size` (as `rows.list`), and:

- `search` (up to 256 characters) and `search_mode` (`full-text`, `full-text-with-count`, or `compat`; needs `search`).
- `filters` (up to 10): objects with `field` (a field name), `type`, and `value` (a string up to 256 characters,
  omitted for `empty` and `not_empty`, required for the others). Types: `equal`, `not_equal`, `contains`,
  `contains_not`, `higher_than`, `higher_than_or_equal`, `lower_than`, `lower_than_or_equal`, `empty`,
  `not_empty`. `filter_type` is `AND` (default) or `OR`. The same field and type may not repeat.
- `order_by` (up to 5): objects with `field` and `direction` (`asc` default, `desc`).
- `view_id`: a view of this table.

There is no free filter passthrough: values travel only as URL-encoded query parameters built from these
fields. Field names must exist in the table's schema, be of a plain type (text, long text, number, boolean,
date, rating, single select, email, URL, phone number, autonumber, UUID, created on, last modified), and not
contain `__` or `,` or begin with `-` or `+`. Link, lookup, formula, rollup, and count fields cannot be filtered
or sorted: such a filter would show whether a value exists in another table. For the same reason `search` is
refused on a table that has such a field unless the connection targets `*`.

Order of checks: the table (before the credential), argument shape and limits (before any request), then one
read of the table's fields, after which an unknown or unsuitable field, or a refused search, is rejected. Only
then, with `view_id`, the table's view list is read; a view that is not in it is rejected. The rows request
follows last. The answer is the shape of `rows.list` (rows keyed by field name), link-masked and bounded the same way.

## Row changes

Each change takes `table_id` (inside the targets), needs `confirm`, and sends exactly one request. A change is
never repeated by Qatlas: after a timeout, a connection reset, an unknown transport failure, an HTTP 5xx, or
an unreadable answer the error says the change may have taken effect, and the row should be read before it is
tried again. `rows.delete` runs only for a connection whose `tools` list names it.

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
texts) and only when its other table is inside the targets; with `*` any table is allowed, and a link field
whose other table cannot be determined is refused. The body is limited to 1 MiB and 500 cells. A missing
token right is reported as a `permission` error naming the right to grant (`create`, `update`, or `delete`);
Baserow's own text is never passed on.

## Links into other tables

A link field (`link_row`) whose other table is outside the targets gives only the row IDs, as
`[{"id": 4}]`, without display values. The same applies when the other table cannot be determined (for
example, it was deleted) and to a link-shaped value whose field is not found. A connection with `*` keeps the
display values. To decide, `rows.list` and `rows.get` read the table's fields in one extra request, and only
when a row holds a link-shaped value. `fields.list` names a link field's other table (`link_table_id`) only
when it is inside the targets.

**Limit:** lookup and formula fields can carry values of other tables, and a lookup of a table outside the
targets is not masked; such values are passed through as Baserow reports them. Keep the token's rights and
the connection's targets aligned, or avoid such fields in tables the connection exposes. File fields carry
file names and URLs; Qatlas does not fetch them.

## Bounds

A response is read up to 4 MiB; a larger one is an `invalid-provider-response`. A row change is refused above 1 MiB. Names are cut at 512 bytes.
A cell value is bounded: strings at 2048 bytes, arrays and objects at 100 entries, nesting at 6 levels (deeper
values become `null`). A row has at most 1000 cells, `tables.list` at most 1000 tables, `fields.list` at most
500 fields with at most 100 options each. Requests to one token share a rate limit.

## Errors

Errors carry a stable class and never the provider's text or the token: `auth`, `permission`, `not-found`,
`rate-limited`, `timeout`, `unreachable`, `invalid-provider-response`, and `provider-error`. The invoke log
contains no arguments and no results. Names, options, and cell values are untrusted data of the provider;
Qatlas never renders, follows, or executes them.

## API forms

Checked against the official documentation (baserow.io/user-docs/database-api), not against a live instance:

- Database token authentication with `Authorization: Token ...`, and that tokens work for listing tables,
  listing fields, and row reads.
- `GET /api/database/tables/all-tables/` (a list of tables with `id`, `name`, `database_id`).
- `GET /api/database/fields/table/{table_id}/` (fields with `id`, `name`, `type`, `primary`, `read_only`,
  `link_row_table_id`, `select_options`).
- `GET /api/database/rows/table/{table_id}/` with `page`, `size` (at most 200), and `user_field_names`,
  answering `count`, `next`, `previous`, and `results`.
- `GET /api/database/rows/table/{table_id}/{row_id}/` with `user_field_names`.
- `POST /api/database/rows/table/{table_id}/`, `PATCH .../{row_id}/`, `DELETE .../{row_id}/`, and
  `PATCH .../{row_id}/move/` as the four row endpoints (listed in the official documentation).

Not checked against the official documentation in this change, taken from the Baserow list-rows API as
known: the query parameters `search`, `search_mode`, `filter__{field}__{type}` with `user_field_names=true`,
`filter_type`, `order_by` (comma-separated, `-` prefix for descending), and `view_id`, the filter type names
listed above, and `GET /api/database/views/table/{table_id}/` answering a list of views with `id`.

Assumed, not verified here: `user_field_names=true` and a JSON body keyed by field name for create and update,
the row object as the answer of create, update, and move (move's answer is not read), an empty answer with
status 204 for delete, `before_id` as the query parameter of move, `403` for a missing token right,
the field flags and types listed above, link values as arrays of row IDs or texts, the object shapes of a table and a field as listed above, link
entries as objects with `id` and `value`, `link_row_table_id` being null when the other table is not available, and that the
table list of a token also contains the tables its token may not read, which is why the target filter is
applied to it.
