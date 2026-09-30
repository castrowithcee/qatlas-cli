---
description: >
  Describes the Baserow provider: the database token and Baserow Cloud or self-hosted base URL, the table
  allow-list target, the four read tools, link-field masking outside the allow-list, bounds, errors, and the
  documented limits of lookup and formula fields.
type: knowledge
edit: shared
created: 2026-09-30
updated: 2026-09-30
---

# Baserow

This provider reads tables, fields, and rows of a Baserow instance, Baserow Cloud or self-hosted, with a
database token. It only reads; it changes nothing in Baserow.

## Credential and base URL

The credential provides `database-token`: a Baserow database token, created in the account settings under
database tokens. A token belongs to one workspace and carries the rights `create`, `read`, `update`, and
`delete` per workspace, database, or table; this provider needs only `read`. The token is sent as
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

The read profile offers all four. `rows.list` takes `table_id`, `page` (from 1; 1 when omitted), `size`
(1 to 200; 50 when omitted), and `user_field_names` (true when omitted: values keyed by field name; false:
keyed `field_ID`). It answers `rows` (`id`, `order`, `fields`), `count` (the table's total row count as
Baserow reports it), `page`, `size`, and `has_more`, which is true when Baserow reports a next page. It has
no filters, search, or sorting. `rows.get` takes `table_id`, `row_id`, and `user_field_names`.

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

A response is read up to 4 MiB; a larger one is an `invalid-provider-response`. Names are cut at 512 bytes.
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

Assumed, not verified here: the object shapes of a table and a field as listed above, link entries as objects
with `id` and `value`, `link_row_table_id` being null when the other table is not available, and that the
table list of a token also contains the tables its token may not read, which is why the target filter is
applied to it.
