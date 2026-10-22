---
description: >
  Describes SeaTable Base row comments and base collaborators, their sensitivity, and boundaries.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Comments and collaborators

`seatable.base.comments.list`, `seatable.base.comments.create`, `seatable.base.comments.delete`, and
`seatable.base.collaborators.list` work with the comments of one row and with the people of the base. None of
them is in a profile. `comments.delete` is offered only by a connection whose `tools` list names it. The shared rules are in [the README](README.md#shared-boundaries).

- All three comment tools need the table inside the connection allow-list and the row in it: Qatlas reads the
  row in the selected table before any comment route is used, so a row of another table, or a deleted row,
  gives no access.
- `comments.list` reports the comments of the row as untrusted data, with `has_more`; entries that name
  another row are left out. Comments and their authors are classified `seatable-base-people`.
- `comments.create` sends exactly one request that adds the comment as the user of the API token.
- `comments.delete` first lists the comments of the requested row and deletes only a comment found there;
  any other identifier is refused without a delete request.
- After an uncertain change, list the comments of the row before repeating it.
- `collaborators.list` reports the people of the whole base, with `has_more`; the avatar address is left out.
  It is not bound to the tables of the connection: every connection to the base may read all collaborators,
  so offer it only where that is intended. The sensitivity class `seatable-base-people` covers names and
  mail addresses; values of this class never enter the audit trail or the invoke log.
- Comments across the whole base are not offered.

```sh
qatlas invoke seatable.base.comments.list --connection sales-all-tables --arg table=id:0000 \
  --arg row_id=Qtf7xPmoRaiFyQPO1aENTj
qatlas invoke seatable.base.comments.create --connection sales-rw --confirm --arg table=id:0000 \
  --arg row_id=Qtf7xPmoRaiFyQPO1aENTj --arg comment="Please check this row"
qatlas invoke seatable.base.collaborators.list --connection sales-all-tables
```
