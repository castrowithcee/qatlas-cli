---
description: >
  Describes the Infomaniak kChat incoming and outgoing webhook and slash command tools with their binding and
  behaviour rules.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-28
---

# Infomaniak kChat integrations

The integrations tools of the [Infomaniak kChat provider](README.md). The effects and tool lists are in the
[tool overview](README.md#tools); the target checks are described under [Scope](README.md#scope).

- A `hook_id` (`incoming-webhooks.*`, `outgoing-webhooks.get`, `.update`, `.delete`) is bound through its
  channel like a post, but only in a bound team. A list keeps webhooks of live channels the token belongs to,
  inside the allow-list. An outgoing webhook without a channel watches every public channel of its team, and
  a `command_id` is bound through its team only; both are refused with a channel allow-list. Exclusions leave
  commands alone and only stop `outgoing-webhooks.update` of a webhook without a channel; a webhook of an
  excluded channel is neither listed nor bound.
- The webhook and slash command tools, reads included, need a tools list because an incoming webhook's ID is
  the secret of its post URL and a command carries a token; their IDs are redacted from every error and
  `.update` writes the object back as read.
- A command's token authenticates kChat to the callback server, and a callback URL may carry that server's
  credentials; both are access values for a system outside kChat. The token is never returned, a callback URL
  only as its origin.
- Deleted webhooks and commands are never returned, so their deletion time is not offered.
- `commands.list` lists the team's custom commands only, the purpose of the tool.

The token rights are listed under [Integrations](setup.md#integrations).
