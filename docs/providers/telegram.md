---
description: >
  Describes Telegram message operations, fixed chat targets, connection permissions, and safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-09
---

# Telegram

A connection binds one bot token to its targets, given as `target` (one) or `targets` (a list). A target is
a chat (a numeric chat ID, also negative, or an `@username`), `bot`, or `business/<id>`. It can send
(`create`), edit (`update`), and delete (`delete`) messages in its chats, and every operation requires
confirmation. Telegram's own edit and delete restrictions still apply.

The chat tools take an optional `chat` argument. It must equal a bound chat target exactly: an `@username`
never matches a numeric ID, and `bot` or `business/<id>` is no chat. Without `chat`, the tool uses the one
bound chat and refuses when the connection binds none or several. A chat that is not bound is refused before
the credential is read and before any request, without naming it. A chat ID from an invocation argument or a
Telegram response never becomes a target on its own.

`bot` unlocks only bot-wide tools and `business/<id>` only the tools of that one business connection; both
combine with chats, and a connection with only `bot` has no chat. A tool that needs one of them is refused
before the credential is read when the target is missing. Methods that accept only a numeric chat ID refuse an
`@username` chat locally.

Telegram's tools are sorted into tool groups for display; the message tools belong to `messages`. A group
never changes a tool ID, a permission, or a tools list. The base URL must be a plain `https` URL with a
host and without user, query, or fragment, with no exception for local addresses, and redirects are never
followed. `config validate` rejects any other base URL. Errors never carry Telegram's own error text.

The credential provides `bot-token`. Connection permissions only reduce what Qatlas exposes and executes;
they do not broaden the bot's provider-side rights. An optional `tools` list narrows a connection further
to named tools, for example `[telegram.messages.send]` for a chat that may receive but never lose messages,
and never admits an effect `permissions` excludes. Mutations are never retried after an ambiguous network
result, preventing duplicate sends or unplanned repeated changes.

`telegram.messages.delete` requires a `tools` list: no permission offers it, so a connection without a `tools`
list no longer offers it. Its reach is that of the Bot API `deleteMessage`: in a bound chat the bot
deletes its own messages and, as an administrator, those of others; in a private chat it also deletes incoming
messages.

The terminal editor starts a new connection on the setup profile `send`, which ticks `[create]` and
`[telegram.messages.send]`: Telegram offers no read tool, and a send reaches only a bound chat, each
one after confirmation. The profile `messaging` also ticks `update`, `delete`, `telegram.messages.edit`, and
`telegram.messages.delete`. A profile is a visible starting selection, not a role: only the ticked
`permissions` and `tools` are saved, every tick can be changed before saving, and a saved connection never
follows a profile.
