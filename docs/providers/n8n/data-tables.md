---
description: >
  Describes the n8n data table, column, and row tools: their profiles, project and table binding, the limits
  on names, types, filters, and values, the mass-effect row tools that only a tools list offers, and the
  non-idempotent creates.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# n8n data tables, columns, and rows

Configuration, targets, error classes, and cross-group limits are in [README.md](README.md); the retry
contract of every change is in [workflows.md](workflows.md#changes-and-their-retry-contract). A 403 of any
tool here is reported as a license, scope, or role error, without telling which.

## Profiles

| Profile | Tools |
| --- | --- |
| `datatables-read` | `n8n.data-tables.list`, `n8n.data-tables.get` |
| `datatables-manage` | `n8n.data-tables.list`, `n8n.data-tables.get`, `n8n.data-tables.create`, `n8n.data-tables.rename` |
| `datacolumns-read` | `n8n.data-columns.list` |
| `datacolumns-manage` | `n8n.data-columns.list`, `n8n.data-columns.add`, `n8n.data-columns.update` |
| `datarows-read` | `n8n.data-rows.list` |
| `datarows-manage` | `n8n.data-rows.list`, `n8n.data-rows.insert` |

The column and row tools have their own profiles, so that no existing data table profile widens. These are in
none of the other profiles. `n8n.data-tables.delete`, `n8n.data-columns.delete`, `n8n.data-rows.update`,
`n8n.data-rows.upsert`, and `n8n.data-rows.delete` are in no profile and offered only by a `tools` list.

## Data tables

The tools manage the tables only; `get` and `list` report column definitions, not rows.

- **Project binding.** `list` returns only tables of the project allow-list (a `project_id` argument must be
  on it; foreign tables are dropped from the page without being named, so a page may be empty while
  `has_more` is true). `get`, `rename`, and `delete` read the table first and refuse one whose project is
  outside the allow-list, or that reports no project, before any change request; the refusal names neither
  the table nor its project. `create` needs a `project_id` on the allow-list when the connection has one,
  checked locally before a secret is read; without an allow-list an omitted `project_id` means the key
  owner's personal project. The `name`, `ids`, and `sort_by` arguments of `list` only narrow and order what n8n
  returns; they never widen the allow-list, which still filters every page.
- **Workflow allow-list.** A connection with a `workflow/` target refuses every data table tool locally: a
  table cannot be tied to a workflow, so the narrower reading applies.
- **Create** sends the name, an empty column list, and the project; no CSV import field is offered. Names are
  1 to 128 characters without control characters.
- **Delete** removes the table and all its rows and cannot be undone.

## Data table columns

- **Table binding.** Every tool reads the table first and refuses one whose project is outside the
  allow-list, or that reports no project, before any change request; the refusal names neither the table nor
  its project. `update` and `delete` additionally require the `column_id` among the columns of that read; an
  unknown or foreign column is refused without a change request. A `workflow/` target refuses all four
  locally, as for tables.
- **Names and types.** A column name is 1 to 63 characters, a letter first, then letters, digits, or
  underscores; the type is `string`, `number`, `boolean`, or `date`. `add` appends unless `index` is given;
  `update` renames and/or moves (at least one of `name` and `index`) and never changes the type. `index` is
  limited to 0 to 199 (narrower than the API).
- **Delete** removes the column and its data in every row and cannot be undone. `add` is not idempotent: a
  repeated call may add a second column or be refused as a name conflict.

## Data table rows

There is no tool for `rows/clear`.

- **Table binding.** Every tool reads the table first and refuses one whose project is outside the
  allow-list before any request on its rows; column names in filters and data are checked against the columns
  of that read, and an unknown column or a value of the wrong type (string or date: text, number: number,
  boolean: boolean) is refused before any change. A `workflow/` target refuses all five locally.
- **Filter.** Only a structured filter is accepted: `{type: and|or, conditions: [{column, operator, value}]}`
  with 1 to 10 conditions, operator one of `eq`, `neq`, `like`, `ilike`, `gt`, `gte`, `lt`, `lte`, and a
  string, number, or boolean value. `like` and `ilike` apply to string columns, the orderings to number and
  date columns. Qatlas builds the provider filter itself; no filter text or object is passed through. `list`
  can additionally filter by the system columns `id` (an integer) and `createdAt` and `updatedAt` (RFC 3339
  times) with the comparison operators; `update`, `upsert`, and `delete` refuse system columns. They require a
  filter; matching all rows is never possible.
- **Data.** Rows contain only string (at most 1024 characters), number, and boolean values for known columns;
  `null`, nested values, and empty rows are refused. `insert` takes 1 to 50 rows and always asks n8n for the
  count only.
- **Reading.** `list` returns 50 rows per page by default, at most 100 (the API allows 250), with an opaque
  cursor. Row values are untrusted data; text is cut at 1024 characters, non-scalar values are dropped, and
  the answer is limited to 4 MiB. `sort_by` names a column of the table read or a system column; `search`
  is sent to n8n, which searches all text columns. A follow-up call with a cursor must pass the same filter,
  sort, and search values.
- **Mass effect.** n8n offers no row limit on `update`, `upsert`, and `delete`; one call can change or remove
  every row that matches. They are therefore in no profile and offered only by a tools list, and each needs
  the per-call confirmation. `upsert` updates all matching rows or inserts one row when none matches. The
  answer reports acceptance, not the number of rows. `dryRun` and `returnData` are not offered.
- **Insert** is not idempotent: a repeated call inserts the rows again.
