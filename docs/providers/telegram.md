---
description: >
  Describes Telegram message operations, fixed chat targets, connection permissions, and safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-09-12
---

# Telegram

A connection binds one bot token to one fixed chat target. It can send (`create`), edit (`update`), and
delete (`delete`) messages in that chat. The chat ID never comes from invocation arguments, and every
operation requires confirmation. Telegram's own edit and delete restrictions still apply.

The credential provides `bot-token`. Connection permissions only reduce what Qatlas exposes and executes;
they do not broaden the bot's provider-side rights. Mutations are never retried after an ambiguous network
result, preventing duplicate sends or unplanned repeated changes.
