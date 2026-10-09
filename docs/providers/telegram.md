---
description: >
  Describes Telegram message, update, file, and media operations, fixed chat targets, connection
  permissions, and safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-09
---

# Telegram

A connection binds one bot token to its targets, given as `target` (one) or `targets` (a list). A target is
a chat (a numeric chat ID, also negative, or an `@username`), `bot`, or `business/<id>`. It can send
(`create`), edit (`update`, including the inline keyboard), and delete (`delete`) messages in its chats and pin
or unpin them (`update`), and every operation requires confirmation. Telegram's own edit and delete
restrictions still apply.

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

Telegram's tools are sorted into tool groups for display; the message tools belong to `messages`, the pin tools
to `pins`, the member tools to `members`, the update tool to `updates`, the chat tools to `chats`, the bot
and webhook tools to `bot`, the file tools to `files`, the media tools to `media`. A group never changes a
tool ID, a permission, or a tools list.
The base URL must be a plain `https` URL with a host and without user, query, or fragment, with no exception
for local addresses, and redirects are never followed.
`config validate` rejects any other base URL. Errors never carry Telegram's own error text.

The credential provides `bot-token`. Connection permissions only reduce what Qatlas exposes and executes;
they do not broaden the bot's provider-side rights. An optional `tools` list narrows a connection further
to named tools, for example `[telegram.messages.send]` for a chat that may receive but never lose messages,
and never admits an effect `permissions` excludes. Mutations are never retried after an ambiguous network
result, preventing duplicate sends or unplanned repeated changes.

`telegram.messages.delete` requires a `tools` list: no permission offers it, so a connection without a `tools`
list no longer offers it. Its reach is that of the Bot API `deleteMessage`: in a bound chat the bot
deletes its own messages and, as an administrator, those of others; in a private chat it also deletes incoming
messages.

`telegram.messages.deletemany` deletes from 1 through 100 messages of one chat in a single request and also
requires a `tools` list. Its reach is that of `telegram.messages.delete`. Repeated identifiers count once.
Telegram skips missing or undeletable identifiers without any notice, so success reports only that Telegram
accepted the request, not which messages are gone.

`telegram.pins.pin` pins one message by `message_id`; `disable_notification` pins silently.
`telegram.pins.unpin` unpins `message_id`, or the most recently pinned message when it is omitted.
`telegram.pins.unpinall` unpins every pinned message of the chat and, like `telegram.messages.delete`,
requires a `tools` list. Telegram's own pin rights still apply, and topic-specific unpinning is not offered.

`telegram.members.ban` bans one user by `user_id` (`delete`) and requires a `tools` list; `until_date` (a
positive Unix time) limits the ban, and `revoke_messages` also deletes all messages of the user in the chat.
`telegram.members.unban` lifts a ban (`update`) and always sends `only_if_banned`, so a current member is
never removed; it invites nobody. `telegram.members.restrict` (`update`) requires a `tools` list and sets the
complete `permissions` object of a user in a supergroup: every ChatPermissions boolean is required and sent
explicitly, a missing or unknown field is refused before any request, and Telegram's permission dependencies
are switched off (`use_independent_chat_permissions`). An optional `until_date` ends the restriction. All three
results are a single boolean. Promoting members, banning sender chats, and join requests are not offered.

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

## Reading chats and members

The four chat read tools address a bound chat through `chat` as above and return fixed fields only.
`telegram.chats.get` shows `id`, `type`, `title`, `username`, `is_forum`, `description`, the boolean default
`permissions`, `slow_mode_delay`, `pinned_message_id` (the number only, never the message), and `linked_chat_id`
(a number only, never a target). `telegram.chats.administrators` lists at most 200 administrators and
`telegram.chats.member` shows one member by `user_id`: identity (`id`, `is_bot`, names, `username`), `status`,
the boolean rights Telegram returns, `custom_title`, and `until_date`. `telegram.chats.membercount` shows
`count`. The invite link and every other field are never returned. Strings are length-capped. Member data
of administrators and members is classified `telegram-member-data`; the other two tools keep the provider's
message classification.

## Bot identity and webhook status

`telegram.bot.get` (`read`) shows the identity of the bot behind the token: `id`, `username`, `first_name`,
`can_join_groups`, `can_read_all_group_messages`, and `supports_inline_queries`. It runs on every connection
and needs no `bot` target, since it concerns only the connection's own token.

`telegram.webhook.get` (`read`) shows whether a webhook is active (`has_webhook`), the number of updates
waiting (`pending_update_count`), and, when present, the time of the last delivery error (`last_error_date`).
It needs the `bot` target and is refused before the credential is read without it. The webhook URL and every
other field of Telegram's answer are never returned. It is part of no setup profile.

## Files

`telegram.files.get` (`read`) returns the size and `file_unique_id` of one file; `telegram.files.download`
(`read`) writes it to `local_path`. Both take only a `file_ref` from `media.file_ref` of `telegram.updates.list`:
a raw file identifier, a reference bound to another target, one signed with another token, or one of another
kind is refused. Neither tool is part of a setup profile.

`telegram.files.download` is offered only on a connection that releases a directory for writing (`files`).
A `local_path` outside it is refused before the credential is read. An existing file is replaced only with
confirmation, and a failed or incomplete transfer leaves no file. The result carries size and SHA-256, never the
content. Files above 20 MB, the Bot API's limit for bots, are refused before the download, and a response
longer than the reported size is rejected. The download URL carries the bot token; it and Telegram's
`file_path` appear in no output, error, or log, and a redirect is never followed.

## Sending media

`telegram.photos.send`, `telegram.documents.send`, and `telegram.mediagroups.send` (`create`) send a photo, a
document, or an album of 2 through 10 photos or of 2 through 10 documents; the kinds are never mixed. Each
file comes from exactly one of `local_path` or `file_ref`; an album takes the choice per item. A URL is never
a source. The tools are offered only on a connection that releases a directory for reading (`files`), and the
options of `telegram.messages.send` that apply (`parse_mode`, `reply_to_message_id`, `message_thread_id`,
`disable_notification`, `protect_content`) carry over, plus a `caption` of at most 1024 characters. An album
caption goes on its first item.

A `local_path` outside the released directories is refused before the credential is read. A photo is limited
to 10 MB, any other file to 50 MB, and the files of one album together to 50 MB; the size is checked from the
file before it is read or sent. A local file is sent under a neutral name (`file` or `file-N`, plus the plain
extension), so neither the path nor the file name leaves the machine.

A `file_ref` must come from the selected chat itself: one issued for another target, even another bound chat,
for another token, or of another kind is refused before the credential is read, and a raw file identifier is
never accepted. Its size is not known locally; Telegram enforces the limits for it.

The result is `message_id`, `date`, and a `file_ref` of the sent file, for an album one such entry per message
under `messages`. Each tool sends exactly one request and reports an unclear outcome instead of repeating it.

## Setup profiles

The terminal editor starts a new connection on the setup profile `send`, which ticks `[create]` and
`[telegram.messages.send]`: the read tools expose incoming message content, while a send reaches only a bound
chat, each one after confirmation. The profile `read` ticks `[read]`, `[telegram.updates.list]`,
`[telegram.bot.get]`, and the four `telegram.chats.*` tools. The profile `messaging` also ticks `update`,
`delete`, `telegram.messages.edit`, and `telegram.messages.delete`; the profile `pins` ticks `update`,
`telegram.pins.pin`, and `telegram.pins.unpin`; the profile `media` ticks `create` and the three media send
tools; the profile `moderation` ticks `update` and `telegram.members.unban`.
`telegram.messages.editreplymarkup`, `telegram.pins.unpinall`, `telegram.members.ban`, and
`telegram.members.restrict` are in no profile. A profile is a visible starting selection, not a role: only the
ticked `permissions` and `tools` are saved, every tick can be changed before saving, and a saved
connection never follows a profile.
