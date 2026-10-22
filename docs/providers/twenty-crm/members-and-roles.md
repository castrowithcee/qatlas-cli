---
description: >
  Describes the Twenty CRM tools that read workspace members and roles and change roles.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Twenty CRM: members and roles

## Members and roles

`workspace-members.list`, `workspace-members.get`, and `roles.list` (`read`) are workspace-wide. Members are
personal data with their own data class; avatars, user identifiers, and member and key names are never read.

`roles.create`, `update`, `delete`, `set-object-permissions`, `set-field-permissions`, and `set-permission-flags` are
confirmed, need a `tools` entry, and assign nothing. They change only a role that a first read shows editable and
without API keys. A rights call covers one non-system object checked against the [data model](data-model.md). Flags
are replaced as a whole. All need "Roles", so use a key of its own; no profile ticks them.
