---
description: >
  Describes SeaTable Base row link operations and their table boundary.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Row links

`seatable.base.links.list`, `seatable.base.links.create`, `seatable.base.links.update`, and `seatable.base.links.delete` read
and change the links between rows of two tables through one link column. The shared rules are in
[the README](README.md#shared-boundaries).

- The agent passes `table` and `column` (name or key of a column of type `link`). Qatlas takes the link
  identifier and the joined table from the base metadata; neither is an argument.
- Both tables must be inside the connection allow-list. A wildcard connection allows every table; a
  counterpart table reached by name or by `id:` is checked the same way. A link column into a table outside
  the allow-list is refused without a link request, and the message does not name that table. The
  connection's own table is checked before the credential is read and before SeaTable is contacted. To find
  the counterpart table, Qatlas first exchanges the token and reads the base metadata; that is the only
  provider access that can precede a refusal.
- An unknown column, or a column that is not a link column, is refused before the link request.
- `seatable.base.links.list` pages per source row; `has_more` is true when a source row returned a full page.
  It is part of the `read` profile. A `display_value` is reported when SeaTable sends one.
- `seatable.base.links.update` replaces all links of that row in the column; an empty list is refused before
  the credential is read, because removing links is what `seatable.base.links.delete` is for.
  `seatable.base.links.delete` is offered only by a connection whose `tools` list names it.
- Row identifiers are 22 characters of letters, digits, `-`, or `_`, and must not repeat in one call.
- Read the links before repeating an uncertain change.

```sh
qatlas invoke seatable.base.links.list --connection sales-all-tables --arg table=id:0000 --arg column=Tickets \
  --arg row_ids='["Qtf7xPmoRaiFyQPO1aENTj"]'
qatlas invoke seatable.base.links.create --connection sales-rw --confirm --arg table=id:0000 --arg column=Tickets \
  --arg row_id=Qtf7xPmoRaiFyQPO1aENTj --arg other_row_ids='["Ab12Cd34Ef56Gh78Ij90Kl"]'
```
