---
description: >
  Describes the Baserow provider: credential and base URL, table and database targets, the read tools, row changes, file transfer, link masking and target binding of schema properties, bounds, errors, and API checks.
type: knowledge
edit: shared
created: 2026-09-30
updated: 2026-10-26
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
database is not listed. That read is made at most once per call and is shared with the link checks below.
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

`rows.list` and `rows.search` select columns with `include` and `exclude`. `rows.list` sends the names unchecked;
`rows.search` checks them against the schema it reads anyway. Link masking applies to the selected columns. Names with
a comma, with `__`, or with a leading `-` or `+` cannot be selected: such names would need the quoting of Baserow's
parameters, and Qatlas sends no quoted names. `{link_row_field}__join` is not offered because it reads values of other
tables through the link.

`rows.get` with `view_id` first reads the table's view list and rejects a view that is not in it; without it the call
sends one request. `include_metadata` adds `metadata` at no extra request.

## Not offered: row history and comments

Row history (`GET /api/database/rows/table/{table_id}/{row_id}/history/`) and row comments (`GET
/api/row_comments/{table_id}/{row_id}/`, a premium feature) are not tools. The official API schema (Baserow 2.4.0,
`/api/schema.json`) lists only JWT (user session) authentication for both endpoints, while the row endpoints list JWT
and database token. Qatlas does not send a user session, so both stay out until the documentation lists the database
token for them.

## Searching rows

A filter or sort entry of `baserow.rows.search` names its field by `field` or by `field_id`, exactly one; the ID is
translated through the table's schema, and an unknown ID is rejected. Resolved names may not contain `__` or `,` or
begin with `-` or `+`. Filter types come from a fixed list; for most of them Baserow decides whether a value is
needed. Values travel only as URL-encoded query parameters, never as a passthrough. The API documents no format for
filter groups and a flat tree does no more than the offered filters, so the JSON filter tree is not offered.

Lookup, formula, rollup, and count fields are filterable and sortable. A link field whose other table is outside the
targets (or cannot be determined) allows only `empty`, `not_empty`, `link_row_has`, and `link_row_has_not`, and no
sorting. Display values of linked rows outside the targets are masked; a text filter, a sort, or a full-text search on
them would disclose them. Filters by row ID or emptiness show only what the output shows anyway. For the same reason
`search` is refused on a table with such a link field unless the connection targets `*`; with `*`, or when the other
table is inside the targets, nothing is refused.

Order of checks: the table (before the credential), argument shape, limits, column keys, and filter types (before any
request), then one read of the table's fields, after which an unknown or unsuitable field, a repeated field and type,
an unknown `include` or `exclude` name, or a refused search is rejected. Only then, with `view_id`, the table's view
list is read; a view that is not in it is rejected. The rows request follows last. The answer has the shape of
`rows.list`, keyed by field name, link-masked and bounded the same way.

## Row changes

Each change takes `table_id` (inside the targets), needs `confirm`, and sends exactly one request. A change is never
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
texts) and only when its other table is inside the targets; with `*` any table is allowed, and a link field
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

## Files

Files are addressed only by table (inside the targets), row, and the name of a field of type `file`; the field must
exist in the table's schema (one field read), and an upload also needs it to be writable. No message quotes a field
name.

`files.upload` takes `table_id`, `row_id`, `field`, and exactly one source:

- `local_path`: a file inside a directory the connection releases for reading (`files.read`); the stored name
  is the name of the local file, and `name` is not allowed.
- `content_base64`: inline content up to 4 MiB, with `name`.
- `url`: an `https` URL that Baserow fetches itself (`upload-via-url`); `name` is optional (Baserow derives one
  from the URL).

It needs `confirm`. The cell is read first; then one upload request (`POST /api/user-files/upload-file/`, or
`upload-via-url`) and one row change (`PATCH`) follow that keep the files the cell held and append the new one.
Neither request is repeated. Baserow has no atomic append, so a change to the same cell between the read and the row
change is overwritten. A failure of the upload, or an unclear result of either request, says that the file may have
been uploaded or attached, and a refused row change says that the file was uploaded but is not attached (an unattached
user file stays on the instance). The answer names the stored file (`id`, `name`, `size`, `sha256`) and never the file
address. Baserow names a stored file `<unique>_<sha256>.<extension>`; for a path or inline upload the hash must equal
the SHA-256 Qatlas computed, otherwise the file is not attached. The size must equal the size sent.

`files.get` takes `table_id`, `row_id`, `field`, and optionally `name` (the shown name or the stored name) or `index`;
a cell with exactly one file needs neither, an ambiguous name is refused. The file is chosen from the cell as it is
now, never from an address in the arguments. Without `local_path` the content comes back as `content_base64` up to 4
MiB; with `local_path`, inside a directory released for writing (`files.write`), it is streamed to the file and only
`id`, `name`, `size`, and `sha256` are returned. An existing file is replaced only with confirmation. The download is
checked against the announced length and against the SHA-256 in the stored name.

Transfers of file content have a timeout of 30 minutes; all other requests keep 30 seconds.

Because both tools declare local file access, a connection offers them only when it lists `files` entries for their
direction (`read` for the upload, `write` for the download), also for inline and `url` transfers.

**File addresses are untrusted.** The address in a cell is used only when it is `https`, has no user info or fragment,
is on exactly the host (and port) of the connection's base URL, and its path is exactly `/media/user_files/<stored
name>` (below the installation path, if any) for the file that was picked. The download sends no token; Baserow serves
stored files under unguessable names, and a signed query, where the instance uses one, is kept and never reported. No
redirect is followed. A file that the instance serves from another host, such as an object store or a CDN, cannot be
shown to belong to the instance and is refused; such files cannot be downloaded with this tool.

**`url` uploads.** Only `https` URLs on a public DNS host name are accepted: no user info, no port, no fragment, no
numeric address (decimal, hexadecimal, or dotted, IPv4 or IPv6), ASCII labels only (internationalised names in `xn--`
form), at least two labels with an alphabetic top-level label, and none of the names reserved for internal use
(`localhost`, `local`, `internal`, `intranet`, `lan`, `home`, `corp`, `private`, `test`, `example`, `invalid`,
`onion`, `arpa`, `alt`, `localdomain`). The URL never appears in a message. This list is syntactic: Qatlas does not
resolve the name, because Baserow, not Qatlas, fetches the URL. A public name that resolves to a private address is
stopped by Baserow's own address guard (the `advocate` library in Baserow 2.4.0, which also checks redirects), and
this holds only as long as the instance does not relax it. Do not release `url` uploads on a self-hosted instance
whose network an untrusted agent must not reach.

## Links into other tables

A link field (`link_row`) whose other table is outside the targets gives only the row IDs, as `[{"id": 4}]`, without
display values. The same applies when the other table cannot be determined (for example, it was deleted) and to a
link-shaped value whose field is not found. A connection with `*` keeps the display values. To decide, `rows.list` and
`rows.get` read the table's fields in one extra request, and only when a row holds a link-shaped value.

`fields.list` follows the same boundary without an extra request. A property that points to a table outside the
targets is shown only for a connection with `*` or a table inside the targets:

- A link field's mirror field, limiting view, and primary field name appear only with `link_table_id`.
- A lookup or rollup shows its target field only when the table of its link field (`through_field_id`, read from the
  same answer) is inside the targets.
- A formula text can name fields of linked tables. `formula` and `error` of formula, count, rollup, lookup, and AI
  fields are left out as soon as the table has a link field whose table is outside the targets or undeterminable.

Deprecated members are never shown. A free value (AI prompt, field constraints) beyond the size limits of a cell is
left out and marked as omitted, not cut.

**Limit:** lookup and formula fields can carry values of other tables, and a lookup of a table outside the
targets is not masked; such values are passed through as Baserow reports them, and a filter or sort on them discloses
nothing beyond that. Keep the token's reach and the connection's targets aligned, or avoid such fields in tables the
connection exposes. In `rows.list`, `rows.get`, and `rows.search`, file fields carry file names and URLs as data;
Qatlas fetches a file only with `files.get`, under the rules above.

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
