---
description: >
  Describes the Infomaniak kChat bot, custom emoji, and role tools with their binding and behaviour rules.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-26
---

# Infomaniak kChat administration

The administration tools of the [Infomaniak kChat provider](README.md). The effects and tool lists are in the
[tool overview](README.md#tools); the target checks are described under [Scope](README.md#scope).

- The `bots` tools never return a token; `bots.update` sends only the fields it is given; `.disable` stops the
  bot's integrations at once.
- `emoji.create` takes a `name` and a released local PNG, GIF, or JPEG of up to 1 MiB, checked before any
  secret is read. Its multipart body holds `image` and `emoji` (JSON `name` and `creator_id`, the token's own
  user from `users/me`); that format is not proven live, so a wrong one is a `provider-error`.

The token rights are listed under [Administration](setup.md#administration).
