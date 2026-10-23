---
description: >
  Describes the Infomaniak kChat incoming and outgoing webhook and slash command tools with their binding and
  behaviour rules.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-23
---

# Infomaniak kChat integrations

The integrations tools of the [Infomaniak kChat provider](README.md). The effects and tool lists are in the
[tool overview](README.md#tools); the target checks are described under [Scope](README.md#scope).

- A `hook_id` (`incoming-webhooks.*`, `outgoing-webhooks.get`, `.update`, `.delete`) is bound through its
  channel like a post, but only in a bound team. A list keeps webhooks of live channels the token belongs to,
  inside the allow-list. An outgoing webhook without a channel watches every public channel of its team, and
  a `command_id` is bound through its team only; both are refused with a channel allow-list.
- The webhook and slash command tools, reads included, need a tools list because an incoming webhook's ID is
  the secret of its post URL and a command carries a token; their IDs are redacted from every error, `.update`
  writes the object back as read, no token ever appears, and a callback URL only as scheme and host.
