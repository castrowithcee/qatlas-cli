---
description: >
  Describes SeaTable Base row change history and the base operation log, their sensitivity, and boundaries.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Change history

`seatable.base.rows.activities` reads the change history of one row and `seatable.base.base.operations` reads the
operation log of the whole base. Both are read-only and in no profile, because old and new values and the
people behind changes are sensitive. Their sensitivity class is `seatable-base-history`; values of this class
never enter the audit trail or the invoke log. The shared rules are in
[the README](README.md#shared-boundaries).

- `rows.activities` needs the table inside the connection allow-list and the row in it: Qatlas reads the row
  in the selected table before it asks for the history, so a row of another table, or a deleted row, yields
  no history. The token exchange and the row read are the only provider access that can precede a refusal.
- Activities may hold values of linked rows; For a connection with an allow-list, a link
  value whose linked table is outside it, or cannot be determined from the base metadata, is reduced to the
  `row_id` of its entries, and the key of a lookup, findmin or findmax column under the same condition is
  removed with its name listed in `omitted_fields` of the enclosing object. A wildcard connection keeps the
  values. Text that SeaTable composes into a single string cannot be inspected and is reported as SeaTable
  sends it.
- Only entries of the requested row are reported: an entry with a `row_id` other than the requested one, or
  with a `table_id` or `table_name` other than the selected table, is left out; entries without these fields
  stay, because the request names the row. Reading the table identifiers of the metadata is then the only
  additional provider access.
- `base.operations` covers the whole base and is refused before the credential is read for every connection
  that is not bound to `*`.
- Entries are reported in `items` as SeaTable sends them, as untrusted data. Nesting beyond 8 levels is
  dropped and one call reports at most 100 entries. `has_more` is true when an `activities` page is full, or
  when `operations` had more entries than one call reports.

```sh
qatlas invoke seatable.base.rows.activities --connection sales-all-tables --arg table=id:0000 \
  --arg row_id=Qtf7xPmoRaiFyQPO1aENTj
qatlas invoke seatable.base.base.operations --connection sales-all-tables --arg page=1
```
