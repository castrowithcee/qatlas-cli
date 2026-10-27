---
description: >
  Describes the Baserow provider: credential and base URL, table and database targets, the tool overview, bounds, errors, and API checks; indexes the pages for rows, files, and links.
type: knowledge
edit: shared
created: 2026-09-30
updated: 2026-10-27
---

# Baserow

This provider reads tables, fields, and rows of a Baserow instance, Baserow Cloud or self-hosted, with a database
token, and creates, changes, deletes, and moves single rows of the tables a connection allows. It also uploads files
to and downloads files from the file fields of those rows. [Set up Baserow](setup.md) leads through creating a token
and lists the rights it needs.

## Credential and base URL

The credential provides `database-token`: a Baserow database token. Creating it and the rights each tool group needs
are described in [Set up Baserow](setup.md). The token is sent as `Authorization: Token ...` and is registered with
the redactor.

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

A connection lists what it may read as targets: `table/TABLE_ID` entries, `database/DATABASE_ID` entries, or the
single entry `*` for every table the token reads. Entries are positive integers without a sign or leading zero. A
database covers all its tables, also those created later; tables and databases together form the allow-list. A listed
table does not open its database.

```yaml
connections:
  customer-a-crm:
    service: baserow-customer-a
    credential: baserow-customer-a-token
    targets: [table/11, database/3]
```

A `table_id` outside the targets is refused as an invalid request without naming the table. A listed table is checked
before the credential is resolved and before any request. A table that is not listed itself is proven by one read of
the token's table list, which names the table's database; it is refused without any request on the table when its
database is not listed. That read is made at most once per call and is shared with the link checks ([Links](links.md)).
`baserow.tables.list` shows only the tables of the targets when the connection has an allow-list. The target limits
what Qatlas shows; it does not narrow what the token itself may read; see [Set up Baserow](setup.md). Every tool acts
on one table or lists tables, so a database target reaches all of them.

## Choosing targets

The selection lists the databases of the token and the tables of one database, without the connection's targets. A
database token reads the token's table list, which names no database; databases are therefore derived from that list
and shown by their ID, not by name.

A URL of the web interface of the form `/database/DATABASE_ID/table/TABLE_ID/...` names a database and a table without
a request. For Baserow Cloud the web host is `baserow.io`, the API host `api.baserow.io`; for a self-hosted instance
only the host of the connection's base URL counts, because the web address of an installation may differ from its API
address. Any other host names nothing.

## Tools

| Tool | Effect | Confirmation | Does |
| --- | --- | --- | --- |
| `baserow.tables.list` | read | none | lists the tables of the targets, also those of a listed database |
| `baserow.fields.list` | read | none | lists one table's fields with their type-specific schema properties |
| `baserow.rows.list` | read | none | lists one table's rows page by page |
| `baserow.rows.get` | read | none | reads one row |
| `baserow.rows.search` | read | none | searches, filters, and sorts one table's rows page by page |
| `baserow.rows.create` | create | required | creates one row from cell values keyed by field name |
| `baserow.rows.update` | update | required | changes the named cells of one row |
| `baserow.rows.delete` | delete | required, tool allow-list | deletes one row |
| `baserow.rows.move` | update | required | moves one row within its table |
| `baserow.rows.batch-create` | create | required | creates 1 to 200 rows in one request |
| `baserow.rows.batch-update` | update | required | changes the named cells of 1 to 200 rows in one request |
| `baserow.rows.batch-delete` | delete | required, tool allow-list | deletes 1 to 200 rows in one request |
| `baserow.files.upload` | update | required | uploads a file and appends it to a file field of one row |
| `baserow.files.get` | read | none | downloads one file of a file field, inline or to a local path |

The read profile offers the five read tools.

The tools are described on the pages [Rows](rows.md), [Files](files.md), and [Links](links.md).

## Not offered: row history and comments

Row history (`GET /api/database/rows/table/{table_id}/{row_id}/history/`) and row comments (`GET
/api/row_comments/{table_id}/{row_id}/`, a premium feature) are not tools. The official API schema (Baserow 2.4.0,
`/api/schema.json`) lists only JWT (user session) authentication for both endpoints, while the row endpoints list JWT
and database token. Qatlas does not send a user session, so both stay out until the documentation lists the database
token for them.

## Bounds

Inline file content is limited to 4 MiB in both directions (10 GiB for a local path), a file name to 255 bytes, and a
cell to 200 files. A response is read up to 4 MiB; a larger one is an `invalid-provider-response`. A row change is
refused above 1 MiB. Names are cut at 512 bytes. A cell value is bounded: strings at 2048 bytes, arrays and objects at
100 entries, nesting at 6 levels (deeper values become `null`). A row has at most 1000 cells, `tables.list` at most
1000 tables, `fields.list` at most 500 fields with at most 100 options each. Requests to one token share a rate limit.

## Errors

Qatlas' message of an error carries a stable class and never the provider's text or the token: `auth`,
`permission`, `not-found`, `rate-limited`, `timeout`, `unreachable`, `invalid-provider-response`, and
`provider-error`. Baserow's answer goes separately as the original in the `provider` field of the error detail
(untrusted, redacted, limited): its error code, its text, and each validation error with its field name. A file
download from the media address keeps only the HTTP status. Causes and remedies by symptom are in
[Fixing errors](setup.md#fixing-errors). The invoke log contains no arguments and no results. Names, options, and
cell values are untrusted data of the provider; Qatlas never renders, follows, or executes them.

## API forms

Checked against the official documentation (baserow.io/user-docs/database-api), not against a live instance:

- Database token authentication with `Authorization: Token ...` for listing tables, listing fields, and row reads.
- `GET /api/database/tables/all-tables/` and `GET /api/database/fields/table/{table_id}/`: checked field by field
  against the official OpenAPI schema (Baserow 2.4.1, `/api/schema.json`), without deprecated members.
- `GET /api/database/rows/table/{table_id}/` with `page`, `size` (at most 200), and `user_field_names`,
  answering `count`, `next`, `previous`, and `results`; `GET .../{row_id}/` with `user_field_names`.
- `POST /api/database/rows/table/{table_id}/`, `PATCH`, `DELETE`, and `PATCH .../move/` on a row.
- The official API schema (Baserow 2.4.0) lists the database token, next to JWT, for
  `POST /api/user-files/upload-file/` (multipart field `file`) and `POST /api/user-files/upload-via-url/`
  (body `{"url": ...}`); both answer a user file. A file cell is an array of objects with `name`, `visible_name`,
  `url`, `size`; a row change references a file by `name`. The stored-name form, the `/media/user_files/` path, and
  the address guard of `upload-via-url` come from the Baserow source (tag 2.4.0), not from a live instance; which
  token right an upload needs is not documented, and the media path is the default, not that of every installation.

The list-rows parameters `search`, `search_mode`, `filter__{field}__{type}` with its filter types, `filter_type`,
`order_by`, `include`, `exclude`, and `view_id` follow the same OpenAPI schema;
`GET /api/database/views/table/{table_id}/` answering a list of views with `id` is assumed.

Assumed, not verified: `user_field_names=true` and a JSON body keyed by field name for create and update, the row
object as the answer of create, update, and move (move's answer is not read), an empty 204 answer for delete,
`before_id` as the query parameter of move, `403` for a missing token right, link values as arrays of row IDs or
texts, link entries as objects with `id` and `value`, `link_row_table_id` being null when the other table is not
available, and that the table list of a token also contains the tables it may not read, which is why the target
filter is applied to it.

Batch endpoints, assumed from the documented API shape: `POST .../table/{table_id}/batch/` with
`{"items": [cells]}` and `PATCH .../batch/` with `{"items": [{"id": N, ...cells}]}`, both with
`user_field_names=true` and answering `{"items": [rows]}` in request order; `POST .../batch-delete/` with
`{"items": [ids]}` and an empty 204 answer; a limit of 200 rows per call (`BATCH_ROWS_SIZE_LIMIT`).
