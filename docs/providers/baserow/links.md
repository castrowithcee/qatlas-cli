---
description: >
  Describes Baserow link masking for tables outside the targets or excluded in row and field answers.
type: knowledge
edit: shared
created: 2026-10-27
updated: 2026-10-28
---

# Links

The table boundary is in [the README](README.md#scope); file downloads are in [Files](files.md).

## Links into other tables

A table that is not reachable is outside the targets or excluded ([Scope](README.md#scope)); both are treated alike. A
link field (`link_row`) whose other table is not reachable gives only the row IDs, as `[{"id": 4}]`, without display
values, in every read answer and every answer of a row change, also under `*`. The same applies when the other table
cannot be determined (for example, it was deleted) and to a link-shaped value whose field is not found. A connection
with `*` and no exclusion keeps the display values. To decide, `rows.list` and `rows.get` read the table's fields in one
extra request, and only when a row holds a link-shaped value; with an excluded database the table list is read too.

`fields.list` follows the same boundary. A property that points to a table that is not reachable is shown only for a
connection with `*` and no exclusion or a reachable table:

- A link field's mirror field, limiting view, and primary field name appear only with `link_table_id`.
- A lookup or rollup shows its target field only when the table of its link field (`through_field_id`, read from the
  same answer) is reachable.
- A formula text can name fields of linked tables. `formula` and `error` of formula, count, rollup, lookup, and AI
  fields are left out as soon as the table has a link field whose table is not reachable or undeterminable.

Deprecated members are never shown. A free value (AI prompt, field constraints) beyond the size limits of a cell is
left out and marked as omitted, not cut.

A lookup field whose link field (`through_field_id`) leads into a table that is not reachable, or cannot be found or
determined, is left out of every row output (`rows.list`, `rows.get`, `rows.search`, and the answers of row changes) and
named in `omitted_fields`, and `rows.search` refuses filters, sorting, and full-text search on it. Rollup, count, and
formula fields (also with a `lookup()` function) cannot be assigned to a table and are passed through as Baserow reports
them; they can reflect data of linked tables. **Limit:** keep the token's reach and the connection's targets and exclusions aligned, or
avoid such fields in tables the connection exposes. In `rows.list`, `rows.get`, and `rows.search`, file fields carry
file names and URLs as data; Qatlas fetches a file only with `files.get`, under the rules in [Files](files.md).
