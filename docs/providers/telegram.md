---
description: >
  Describes Telegram message operations, fixed chat targets, connection permissions, and safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-09-23
---

# Telegram

A connection binds one bot token to one fixed chat target. It can send (`create`), edit (`update`), and
delete (`delete`) messages in that chat. The chat ID never comes from invocation arguments, and every
operation requires confirmation. Telegram's own edit and delete restrictions still apply.

The credential provides `bot-token`. Connection permissions only reduce what Qatlas exposes and executes;
they do not broaden the bot's provider-side rights. An optional `tools` list narrows a connection further
to named tools, for example `[telegram.messages.send]` for a chat that may receive but never lose messages,
and never admits an effect `permissions` excludes. Mutations are never retried after an ambiguous network
result, preventing duplicate sends or unplanned repeated changes. The terminal editor starts a new connection
on the setup profile `send`, which ticks `[create]` and `[telegram.messages.send]`: Telegram offers no read
tool, and a send reaches only the configured chat, each one after confirmation. The profile `messaging` also
ticks `update`, `delete`, `telegram.messages.edit`, and `telegram.messages.delete`. A profile is a visible
starting selection, not a role: only the ticked `permissions` and `tools` are saved, every tick can be changed
before saving, and a saved connection never follows a profile.
