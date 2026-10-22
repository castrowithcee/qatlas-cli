---
description: >
  Describes the n8n variable and tag tools: their profiles, the separation of project-bound and global
  variables, how update and delete locate a variable, the instance-wide gate of tags, and what deleting
  either removes.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# n8n variables and tags

Configuration, targets, error classes, and cross-group limits are in [README.md](README.md); the retry
contract of every change is in [workflows.md](workflows.md#changes-and-their-retry-contract).

## Profiles

| Profile | Tools |
| --- | --- |
| `variables-read` | `n8n.variables.list` |
| `variables-manage` | `n8n.variables.list`, `n8n.variables.create`, `n8n.variables.update` |
| `tags-read` | `n8n.tags.list`, `n8n.tags.get` |
| `tags-manage` | `n8n.tags.list`, `n8n.tags.get`, `n8n.tags.create`, `n8n.tags.update` |

These are in none of the other profiles. `n8n.variables.delete` and `n8n.tags.delete` are in no profile and
offered only by a `tools` list.

## Variables

A variable is a key with a plain-text value, either bound to a project or global (`project` is null).

- **Separation.** Output marks each variable as project-bound or global. With a project allow-list, `list`
  returns only variables of those projects and never a global one; a `project_id` argument must be on it.
  `create` then requires a `project_id` from the allow-list. A global variable can only be created, read,
  changed, or deleted on a connection without project and workflow targets; both are checked locally before
  a secret is read where possible.
- **Update and delete** find the variable first by paging `GET /variables` (the Public API has no
  `GET /variables/{id}`; at most 20 pages of 250) and refuse one that is global or of a foreign project on a
  bound connection, or that is not found, before any change request; the refusal names neither the variable
  nor its project. `update` sends the variable's own project (null for a global one): a variable is never
  moved between projects.
- **Workflow allow-list.** A connection with a `workflow/` target refuses every variable tool locally (narrower
  reading, as for data tables).
- **Fields.** `key` is 1 to 50 characters of letters, digits, and `_`; a new key does not start with a digit.
  `value` is a string of at most 1000 characters without control characters other than newline, carriage
  return, and tab. Values are returned in clear (each capped), as untrusted data.
- **Create** answers with the key and scope only: n8n reports no ID; `list` finds it. A repeated call may be
  refused as a key conflict.
- **Delete** cannot be undone.
- **403.** Reported as a license (`feat:variables`), scope, or role error, without telling which.

## Tags

A tag is an instance-wide label.

- **Instance-wide gate.** Tags belong to the instance, not to a project or a workflow, so every tag tool is
  refused locally on a connection with any `project/` or `workflow/` target, before a secret is read and
  before any request; the refusal names no target. The gate is one reusable check, `requireInstanceScope`,
  for any further instance-wide tool.
- **Fields.** `name` is 1 to 24 characters without control characters; `tag_id` is validated as a single path
  segment. Names are returned capped, as untrusted data. Tags on workflows are not managed here.
- **Delete** cannot be undone and removes the tag from every workflow that carries it.
- **403.** Reported as an API key scope or role error, without telling which.
