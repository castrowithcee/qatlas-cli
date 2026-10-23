---
description: >
  Describes the Twenty CRM tools that read workspace members and roles and change roles.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-23
---

# Twenty CRM: members and roles

## Members and roles

`workspace-members.list`, `workspace-members.get`, and `roles.list` (`read`) are workspace-wide. Members are
personal data with their own data class; key names are never read.

- The avatar address of a member is a file address signed by Twenty with a token that grants access without
  login, so it is never read.
- A member's `userId` names the instance-wide Twenty user account behind the member, which no tool addresses;
  people appear by their workspace member id.

`roles.create`, `update`, `delete`, `set-object-permissions`, `set-field-permissions`, and `set-permission-flags` are
confirmed, need a `tools` entry, and assign nothing. They change only a role that a first read shows editable and
without API keys. A rights call covers one non-system object checked against the [data model](data-model.md). Flags
are replaced as a whole. All need "Roles", so use a key of its own; no profile ticks them.
