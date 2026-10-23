---
description: >
  Describes the Infomaniak kChat user, status, custom status, profile, and user group tools with their binding and
  behaviour rules.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-23
---

# Infomaniak kChat users

The users tools of the [Infomaniak kChat provider](README.md). The effects and tool lists are in the
[tool overview](README.md#tools); the target checks are described under [Scope](README.md#scope).

- Users are output through a fixed allow-list: `email` only when kChat reports one to the token, and roles,
  notification settings, properties, authentication service, and credential or MFA fields never. `users.list` and
  `.search` need exactly one of `team_id` or `channel_id`; an instance-wide list is not offered. `users.search`
  is a `POST` that changes nothing.
