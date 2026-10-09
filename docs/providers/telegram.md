---
description: >
  Describes Telegram message and update operations, fixed chat targets, connection permissions, and safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-09
---

# Telegram

A connection binds one bot token to its targets, given as `target` (one) or `targets` (a list). A target is
a chat (a numeric chat ID, also negative, or an `@username`), `bot`, or `business/<id>`. It can send
(`create`), edit (`update`, including the inline keyboard), and delete (`delete`) messages in its chats, and
every operation requires confirmation. Telegram's own edit and delete restrictions still apply.

`telegram.messages.send` sends text of 1 through 4096 characters. `parse_mode` (`HTML` or `MarkdownV2`)
formats it; Telegram's parser decides whether the markup is valid, and its rejection surfaces as a provider
error without Telegram's text. `reply_to_message_id` replies to a message of the same chat (never another
chat), `message_thread_id` sends into a forum topic, `disable_notification` sends silently,
`protect_content` forbids forwarding and saving, and `disable_link_preview` hides the link preview.

`inline_keyboard` attaches an inline keyboard of at most 8 rows with 1 through 8 buttons each. A button has
`text` (1 through 64 characters) and exactly one of `url` (a plain `https://` link with a host, or a `tg://`
link, without control characters, at most 2048 characters) or `callback_data` (1 through 64 bytes). Web-app,
login, pay, and `switch_inline_query` buttons and reply keyboards are not offered. The limits are checked
before the credential is read.

`telegram.messages.edit` accepts `parse_mode`, `disable_link_preview`, and `inline_keyboard` besides the text;
reply, topic, silent, and protect do not apply to edits. An edit without `inline_keyboard` removes an existing
keyboard. `telegram.messages.editreplymarkup` changes only the keyboard of a message: it sets
`inline_keyboard` or, when that is omitted or empty, removes the keyboard.

The chat tools take an optional `chat` argument. It must equal a bound chat target exactly: an `@username`
never matches a numeric ID, and `bot` or `business/<id>` is no chat. Without `chat`, the tool uses the one
bound chat and refuses when the connection binds none or several. A chat that is not bound is refused before
the credential is read and before any request, without naming it. A chat ID from an invocation argument or a
Telegram response never becomes a target on its own.

`bot` unlocks only bot-wide tools and `business/<id>` only the tools of that one business connection; both
combine with chats, and a connection with only `bot` has no chat. A tool that needs one of them is refused
before the credential is read when the target is missing. Methods that accept only a numeric chat ID refuse an
`@username` chat locally.

Telegram's tools are sorted into tool groups for display; the message tools belong to `messages`, the update
tool to `updates`. A group never changes a tool ID, a permission, or a tools list. The base URL must be a plain
`https` URL with a host and without user, query, or fragment, with no exception for local addresses, and
redirects are never followed. `config validate` rejects any other base URL. Errors never carry Telegram's own
error text.

The credential provides `bot-token`. Connection permissions only reduce what Qatlas exposes and executes;
they do not broaden the bot's provider-side rights. An optional `tools` list narrows a connection further
to named tools, for example `[telegram.messages.send]` for a chat that may receive but never lose messages,
and never admits an effect `permissions` excludes. Mutations are never retried after an ambiguous network
result, preventing duplicate sends or unplanned repeated changes.

`telegram.messages.delete` requires a `tools` list: no permission offers it, so a connection without a `tools`
list no longer offers it. Its reach is that of the Bot API `deleteMessage`: in a bound chat the bot
deletes its own messages and, as an administrator, those of others; in a private chat it also deletes incoming
messages.

## Reading updates

`telegram.updates.list` (`read`) shows the pending updates of the bot, without `chat`, for every bound chat.
It needs at least one chat target. Only updates of bound chats appear; every other update, whatever its chat
or type, is only counted in `skipped`, and `last_update_id` names the highest update seen, skipped ones
included. An `@username` target is matched through a fixed `getChat` call on the configured name; the ID that
returns is used for matching only. A long poll (`wait_seconds`) waits for a new update.

The tool acknowledges nothing and stores no offset: the same updates appear again on the next call until
Telegram drops them, which it does after at most 24 hours. Telegram allows one consumer per bot. The call
fails with a conflict while a webhook is active or another `getUpdates` consumer runs, and a long poll
interrupts the long poll of another consumer of the same bot.

Identifiers that belong to no chat (a file, a callback query, a join request) are returned only as signed
references, never as Telegram's raw identifier. A reference is bound to the target it came from and to the
bot token, so rotating the token invalidates every earlier reference. It is signed, not secret. A tool that
accepts a reference checks its format and target before the credential is read and its signature before any
request.

## Setup profiles

The terminal editor starts a new connection on the setup profile `send`, which ticks `[create]` and
`[telegram.messages.send]`: the read tools expose incoming message content, while a send reaches only a bound
chat, each one after confirmation. The profile `read` ticks `[read]` and `[telegram.updates.list]`. The
profile `messaging` also ticks `update`, `delete`, `telegram.messages.edit`, and `telegram.messages.delete`;
`telegram.messages.editreplymarkup` is in no profile. A profile is a visible starting selection, not a role:
only the ticked `permissions` and `tools` are saved, every tick can be changed before saving, and a saved
connection never follows a profile.
