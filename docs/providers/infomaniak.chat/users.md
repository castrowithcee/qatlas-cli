---
description: >
  Describes the Infomaniak kChat user, status, custom status, profile, and user group tools with their binding and
  behaviour rules.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-26
---

# Infomaniak kChat users

The users tools of the [Infomaniak kChat provider](README.md). The effects and tool lists are in the
[tool overview](README.md#tools); the target checks are described under [Scope](README.md#scope).

- A user's last password change, failed sign-in attempts, and MFA state are never returned: they describe how
  well another person's account is protected.
- Roles, notification settings, and properties of other users are returned for users of the bound teams only;
  other users are never listed.
- `not_in_team`, `without_team`, `in_group`, `in_group_id`, `group_constrained`, and the system role filters
  `role` and `roles` are not offered: they reach users outside the bound teams, which this provider never lists.
- `users.list` and `.search` need exactly one of `team_id` or `channel_id`; an instance-wide list is not offered.
  `not_in_channel_id` is the exception: it needs `team_id` and excludes `channel_id`; its channel goes through the
  same checks as a `channel_id` and must belong to that team. `users.search` is a `POST` that changes nothing.

The token rights are listed under [Users](setup.md#users).
