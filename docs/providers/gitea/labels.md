---
description: >
  Describes the Gitea and Forgejo label tools: the labels of a repository and of an organization, their token
  scopes, target and ID binding, differences between the products, and limits.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Labels

`labels.*` act on the labels of one repository, `org-labels.*` on those of one organization. Both read, create, and
change labels; deleting is offered only by a connection whose `tools` list names the tool and is in no setup
profile. Names and descriptions are untrusted data.

## Scopes

Presumably `read:issue` or `write:issue` for `labels.*` and `read:organization` or `write:organization` for
`org-labels.*`, following the route groups of the API; none was checked against a live instance. A change also reads
the label first.

## Targets and IDs

- `labels.*` take `repository` (`OWNER/REPO`) like every repository tool.
- `org-labels.*` take `owner` as `orgs/NAME` and are never filled in from a user. A connection with targets must name
  that organization as an `orgs/NAME` target; a `repos/OWNER/REPO` target alone, or a `users/NAME` target, does not
  allow organization labels. `owner` may be left out only when the targets name exactly one owner and it is an
  organization. The refusal comes before the token is read and does not name the value. Without targets any
  organization the token reaches is allowed.
- A label ID is bound by the repository or organization path: the instance answers the ID of a label of another
  repository or organization as not found there. `labels.get` and `org-labels.get` check that the answer carries the
  requested ID; `update` and `delete` read the label on that path first and send the change only when the read
  succeeded.
- A label object carries no repository or organization, so list rows cannot be checked against the targets by
  content; the path decides. The label list of a repository of an organization may also hold the labels of that
  organization.

## Fields

- `color` takes 3 or 6 hexadecimal digits with or without a leading `#` and is sent as given; `description` has at most
  200 characters, `name` 1 to 255. These bounds are Qatlas's, stricter than the Swagger of either product, which
  documents none.
- `exclusive` makes the labels of one scope (`scope/item`) mutually exclusive on an issue. `is_archived` archives a
  label.
- `update` changes only the members passed; an empty `description` removes it.
- Only Forgejo documents `sort` (`mostissues`, `leastissues`, `reversealphabetically`) for both lists; Gitea refuses it
  before the token is read. Otherwise both products document the same label object and options.

## Tools

| Tool | Effect | Provider |
| --- | --- | --- |
| `labels.list`, `labels.get`, `org-labels.list`, `org-labels.get` | read | both |
| `labels.create`, `org-labels.create` | create | both |
| `labels.update`, `org-labels.update` | update | both |
| `labels.delete`, `org-labels.delete` | delete, listed only | both |
