---
description: >
  Describes the n8n user and security audit tools: their profiles, the owner-only and instance-wide gate,
  personal-data handling, assignable roles, the explicit choice every user deletion needs, and the bounds of
  the audit report.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# n8n users and security audit

Configuration, targets, error classes, and cross-group limits are in [README.md](README.md); the retry
contract of every change is in [workflows.md](workflows.md#changes-and-their-retry-contract).

## Profiles

| Profile | Tools |
| --- | --- |
| `users-read` | `n8n.users.list`, `n8n.users.get` |
| `users-manage` | `n8n.users.list`, `n8n.users.get`, `n8n.users.set-role` |
| `audit` | `n8n.audit.generate` |

These are in none of the other profiles. `n8n.users.delete` is in no profile and offered only by a `tools`
list.

## Users

A user's id, email, names, instance role, pending status, and times are personal data (`data_sensitivity`
`n8n-users-personal`) and returned capped, as untrusted data. The MFA status of other users is an account
security detail and is not returned, for security reasons. Qatlas creates no
permanent access or data paths to the outside (webhooks, invitations, public links); reading, pausing,
revoking, and deleting remain, so no tool invites a user.

- **Owner-only and instance-wide.** n8n offers these endpoints to the instance owner only, so a 403 is
  reported as an owner, license, or API key scope error, without telling which. Every user tool is refused
  locally on a connection with any `project/` or `workflow/` target (`requireInstanceScope`), before a secret
  is read and before any request.
- **Read.** `n8n.users.get` takes the user ID only. n8n would also accept an email address there; Qatlas does
  not. `n8n.users.list` can filter by project; the `project_id` is checked locally and an unknown project is
  answered as not found by n8n. The filter does not lift the instance-wide gate.
- **Roles.** Only `global:admin`, `global:member`, and `global:chatUser` can be assigned, as `role` of
  `n8n.users.set-role` (sent as `newRoleName`). `global:owner` and free-form role names are never sent.
- **Delete** cannot be undone. It needs exactly one choice for the user's workflows and credentials:
  `transfer_project_id` moves them into that project (`DELETE /users/{id}?transferId=...`), or
  `delete_owned_resources: true` deletes them permanently with the user (the request without `transferId`).
  Neither, both, or `delete_owned_resources: false` without `transfer_project_id` is refused locally, before
  a secret is read; there is no default. The result names the consequence (`transferred` or
  `deleted_with_user`).

## Security audit

`n8n.audit.generate` is instance-wide (refused locally on a connection with any `project/` or `workflow/`
target, before a secret is read) and needs the owner or admin role; a 403 is reported as a scope, license, or
role error without telling which. It only reads and computes, so it is effect `read`, safe, and needs no
confirmation. `days_abandoned_workflow` is limited to 1 to 3650, and `categories` to distinct values of
`credentials`, `database`, `nodes`, `filesystem`, `instance`; no other audit parameter exists. The result
holds at most 5 reports, each with at most 50 sections and at most 50 locations per section. A section's
`settings` is a free object: it is passed on only within fixed bounds (scalars and lists of scalars, limited
depth and key count) and otherwise left out whole and flagged, never cut. Newer versions per section are
limited, and their nodes are counted and named, nothing more of them. `documentationUrl` of a version is an
address Qatlas never fetches; it is untrusted text. All of it is untrusted data, capped per value, and
`truncated` flags what was cut.
The audit may be slow on a large instance; an error is never retried.
