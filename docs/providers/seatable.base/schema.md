---
description: >
  Describes SeaTable Base schema discovery and table, column, and column option changes.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Schema

`seatable.base.tables.list` discovers the tables visible through the connection and returns the stable reference
accepted by other operations. `seatable.base.columns.list` returns one bounded page of columns. The `table`
argument may be omitted only when the connection has exactly one table, set by `target` or by a one-entry
allow-list. Otherwise, and always for a wildcard connection, it is required and comes from table discovery.
The shared rules are in [the README](README.md#shared-boundaries). None of the change tools below is in a
profile.

## Tables

`seatable.base.tables.create`, `seatable.base.tables.rename`, `seatable.base.tables.duplicate`, and
`seatable.base.tables.delete` manage the tables of the base. `tables.delete` is offered only by a connection whose `tools` list names it.

- A change never widens the table boundary of a connection. A new or duplicated table is not in an
  allow-list, so `tables.create` and `tables.duplicate` work only on a connection with `target: "*"`; any
  other connection refuses them before the credential is read and before SeaTable is contacted.
- `tables.duplicate` copies the rows only when `with_rows` is true. The copy is named by SeaTable;
  `tables.list` shows it.
- `tables.rename` is refused when the table is a connection target by name, such as `Kunden`; a table bound
  as `id:TABLEID` can be renamed. The new name may not be the name of an allow-list entry. Qatlas reads the
  base metadata to see whether the table is also bound by name; that read is the only provider access that
  can precede such a refusal.
- `tables.delete` removes the table with its rows and views. It works on a table in the allow-list, or on any
  table with `*`.
- A new name has 1 to 255 printable characters, no `/`, no surrounding blanks, no `id:` prefix, and is not
  `*`.
- List the tables before repeating an uncertain change.

```sh
qatlas invoke seatable.base.tables.create --connection sales-all-tables --confirm --arg name=Angebote
qatlas invoke seatable.base.tables.rename --connection sales-rw --confirm --arg table=id:0001 --arg name=Faelle
```

## Columns

`seatable.base.columns.create`, `seatable.base.columns.update`, and `seatable.base.columns.delete` create, change, and remove
the columns of an allowed table. `columns.delete` is offered only by a connection whose `tools` list names it.

- A column name has 1 to 255 printable characters, no `.`, `{`, `}`, or backtick, no surrounding blanks, is not
  a system column name, and does not exist yet.
- Formula, button, auto-number, geolocation, and URL columns are not offered.
- `data` has one bounded form per column type and nothing else is accepted. Omitted settings take fixed
  defaults.
- `columns.update` makes exactly one change per call; changing the type may lose existing cell values.
- A link column is created or set only when the joined table is inside the connection's allow-list. An
  existing link column whose joined table is outside it can be neither changed nor deleted. The message never
  names the other table.
- System columns, unknown columns, a name that already exists, an unsupported type, or data that does not fit
  the type are refused before the request. A selection narrowed to one view is refused before the credential
  is read.
- `columns.delete` removes the column with its cell values.
- List the columns before repeating an uncertain change.

```sh
qatlas invoke seatable.base.columns.create --connection sales-rw --confirm --arg table=id:0000 \
  --arg name=Status --arg type=single-select --arg data='{"options":[{"name":"Offen"}]}'
qatlas invoke seatable.base.columns.update --connection sales-rw --confirm --arg table=id:0000 \
  --arg column=Status --arg name=Stand
```

## Column options

`seatable.base.columns.options-add`, `seatable.base.columns.options-update`, and `seatable.base.columns.options-delete` maintain
the options of single-select and multiple-select columns of an allowed table. `options-delete` is offered only by a connection whose `tools` list names it.

- The column is resolved in the base metadata by name or key. Option names have up to 255 printable
  characters and may not repeat within a call.
- Any other column type, an unknown column, an option that already exists on add, or an option that does
  not exist on update or delete is refused before the request. A selection narrowed to one view is refused
  before the credential is read.
- Deleting an option clears the cells that hold it.
- List the columns before repeating an uncertain change.

```sh
qatlas invoke seatable.base.columns.options-add --connection sales-rw --confirm --arg table=id:0000 \
  --arg column=Status --arg options='[{"name":"Offen","color":"#FFE9A8"}]'
```
