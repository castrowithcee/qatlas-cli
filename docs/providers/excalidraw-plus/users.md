---
description: >
  Describes the Excalidraw+ workspace user tools of the people profile.
type: knowledge
edit: shared
created: 2026-10-28
updated: 2026-10-28
---

# Excalidraw+ users

Configuration, scope, targets, error classes, and cross-group limits are in [README.md](README.md).

## Users

The `people` profile (not recommended; it holds personal data and access rights, so the recommended `read` profile
leaves it out) offers four tools. They are **workspace-wide** because users belong to no collection: only a
connection with the target `*` offers them, others are refused before any secret is read or request is sent, without
naming a target. Qatlas creates no permanent access or data paths to the outside (webhooks, invitations, public
links), so no tool invites a person.

| Tool | Request | Does |
| --- | --- | --- |
| `excalidraw-plus.users.list` | `GET /workspaces/users` | lists users with `offset` and `limit` |
| `excalidraw-plus.users.get` | `GET /workspaces/users/{id}` | reads one user: `user_id` |
| `excalidraw-plus.users.update` | `PATCH /workspaces/users/{id}` | changes `name` and/or `role` (`member` or `admin`) |
| `excalidraw-plus.users.remove` | `DELETE /workspaces/users/{id}` | removes a user from the workspace |

- The two reads are safe to repeat. They return the documented user fields except the picture address (`picture`:
  its kind is not documented, so it counts as an access value). The scene histories carry identifiers only. Account
  security data of other people is not part of the answer. The data is personal and untrusted: strings are capped,
  `teams` holds at most 50 groups with 50 entries of 128 characters each, `rate_limits` at most 50 keys per group,
  the scene histories at most 100 entries, and an entry with an invalid identifier is dropped from a list.
- The two changes need `confirm`, are offered only to a connection whose `tools` list names them (also with the
  matching permission), send exactly one request, and are never repeated; after a timeout, a reset connection, a 5xx
  answer, or an unreadable answer, read the user before repeating. Idempotency is reported as `unknown`, the data class
  is personal. Because a tool cannot be gated by argument, `users.update` is gated as a whole, so a name
  change and a promotion to `admin` need the same release.
- `users.remove` is final: Excalidraw+ revokes the user's access and removes the user's workspace data, and Qatlas
  cannot undo it. `users.update` changes neither the e-mail address nor teams.

## Boundary

For users, `PATCH` takes `name`, `picture`, `role`, `workspaceTeams`, and `preferences` and does not change the e-mail
address; `DELETE` removes the user and its workspace data. Assumed: the identifier format (narrowed to the local rule),
the meaning of the `workspaceTeams` keys, whether a team write replaces or merges (so no team write is offered), whether
a workspace owner can be removed or demoted, and the effect of a repeated request.
