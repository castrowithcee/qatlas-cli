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
personal data with their own data class; `roles.list` carries it too, because it lists the members of each role.

- The avatar address of a member is a file address signed by Twenty with a token that grants access without
  login, so it is never read.
- A member's `userId` and `userWorkspaceId` name the Twenty user account behind the member, which no tool
  addresses; people appear by their workspace member id. Inside a role, a member's own roles are not read again.
- An API key of a role appears only by identifier, name, and dates; Twenty offers no token there, and none is read.
- A relation list of a role beyond its limit is left out and marked, never shortened, so a partial list cannot pass
  for the whole one. The value of a row-level right is workspace content, bounded like any free object.

`roles.create`, `update`, `delete`, `set-object-permissions`, `set-field-permissions`, and `set-permission-flags` are
confirmed, need a `tools` entry, and assign nothing. They change only a role that a first read shows editable and
without API keys. A rights call covers one non-system object checked against the [data model](data-model.md). Flags
are replaced as a whole. All need "Roles", so use a key of its own; no profile ticks them.
