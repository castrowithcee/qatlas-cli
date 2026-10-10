---
description: >
  Describes Telegram message, update, file, media, and sticker operations, fixed chat targets, connection
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

The chat tools take an optional `chat` argument, the forward and copy tools also `from_chat`. It must equal a
bound chat target exactly: an `@username` never matches a numeric ID, and `bot` or `business/<id>` is no chat.
Without `chat`, the tool uses the one bound chat and refuses when the connection binds none or several. A chat
that is not bound is refused before the credential is read and before any request, without naming it. A chat
ID from an invocation argument or a Telegram response never becomes a target on its own.

`bot` unlocks only bot-wide tools and `business/<id>` only the tools of that one business connection; both
combine with chats, and a connection with only `bot` has no chat. A tool that needs one of them is refused
before the credential is read when the target is missing. Methods that accept only a numeric chat ID refuse an
`@username` chat locally.

Telegram's tools are sorted into tool groups for display; the message tools belong to `messages`, the pin tools
to `pins`, the member tools to `members`, the update tools to `updates`, the chat tools to `chats`, the invite
link tools to `invitelinks`, the bot and webhook tools to `bot`, the file tools to `files`, the media tools to
`media`, the poll, reaction, chat action, location, venue, contact, and dice tools to `interactions`, the
sticker tools to `stickers`, and the forum topic tools to `topics`. A group never changes a tool ID, a
permission, or a tools list. The base URL must be a plain `https` URL with a host and without user, query, or
fragment, with no exception for local addresses, and redirects are never followed.
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

`telegram.messages.forward` and `telegram.messages.copy` (`create`) move messages between two bound chats of the
connection. `from_chat` names the source and `chat` the target; both follow the rule for `chat` above and may be
the same chat. The source is one `message_id`, which uses `forwardMessage` or `copyMessage`, or `message_ids`, from
2 through 100 distinct positive identifiers in strictly ascending order, which uses `forwardMessages` or
`copyMessages`; exactly one of the two is required, and another order is refused rather than sorted. `copy`
leaves out the link to the original. Its optional `caption` (1 through 1024 characters, with `parse_mode`)
replaces the original caption of a single message and is refused with `message_ids`, since `copyMessages` has
none. `message_thread_id`, `disable_notification`, and `protect_content` apply as for `telegram.messages.send`.
The result is `message_id` for one source and `message_ids` for a list, nothing else. Telegram skips sources it
cannot find or transfer without notice, so `message_ids` can be shorter than the request.

`telegram.pins.pin` pins one message by `message_id`; `disable_notification` pins silently.
`telegram.pins.unpin` unpins `message_id`, or the most recently pinned message when it is omitted.
`telegram.pins.unpinall` unpins every pinned message of the chat and, like `telegram.messages.delete`,
requires a `tools` list. Telegram's own pin rights still apply, and topic-specific unpinning is not offered.

## Moderating members

`telegram.members.ban` bans one user by `user_id` (`delete`) and requires a `tools` list; `until_date` (a
positive Unix time) limits the ban, and `revoke_messages` also deletes all messages of the user in the chat.
`telegram.members.unban` lifts a ban (`update`) and always sends `only_if_banned`, so a current member is
never removed; it invites nobody. `telegram.members.restrict` (`update`) requires a `tools` list and sets the
complete `permissions` object of a user in a supergroup: every ChatPermissions boolean is required and sent
explicitly, a missing or unknown field is refused before any request, and Telegram's permission dependencies
are switched off (`use_independent_chat_permissions`). An optional `until_date` ends the restriction. All
three results are a single boolean. `telegram.senderchats.ban` (`delete`) bans a channel as a sender by
`sender_chat_id` and requires a `tools` list; `telegram.senderchats.unban` (`update`) lifts it. The channel
identifier is only the object of the call, never the target. Join requests are not offered.

`telegram.members.promote` (`update`) requires a `tools` list and sets the complete `rights` object of an
existing member in a supergroup or channel: every administrator right of `promoteChatMember` is required and
sent explicitly, a missing or unknown field is refused before any request, and all `false` demotes. The
membership is checked beforehand with `getChatMember`; a non-member or an unclear status is refused before
any change.
`can_promote_members` and `can_invite_users` widen who may add administrators and members. The default
administrator rights of the bot are not offered. `telegram.members.setadmintitle` sets the custom `title` of
an administrator the bot promoted in a supergroup, and `telegram.members.settag` the `tag` of a regular
member; both are `update`, take 0 to 16 characters (Unicode characters, not bytes) without emoji, check
this locally, and remove the value when empty. The emoji check is conservative: symbols, joiners,
variation selectors, and the emoji blocks are refused.

## Polls, reactions, and chat actions

`telegram.polls.send` (`create`, not idempotent) sends one poll or quiz as plain text: no entities, no
media, no paid or business parameters. A quiz requires `correct_option_ids`, which regular polls refuse,
as they refuse an `explanation`. `open_period` and `close_date` exclude each other; everything Telegram's
limits fix is checked locally before the credential is read. The result is `message_id`, `date`, and
`poll_id`. `telegram.polls.stop` (`update`, idempotent) closes a poll of the bot for good and, like
`telegram.messages.delete`, requires a `tools` list; it reports only `stopped`, never counts or voters.
`telegram.reactions.set` (`update`, idempotent) sets at most one reaction of the bot from Telegram's
fixed emoji list or a numeric `custom_emoji_id`; an empty `reactions` list removes it. Paid reactions are
not offered. `telegram.reactions.remove` (`delete`, idempotent) removes the reactions of one user (`user_id`)
or one chat (`actor_chat_id`) from one message; `telegram.reactions.removeall` removes up to 10000 recent
reactions of that user or chat across the whole chat. Exactly one actor is required, the chat is a group or
supergroup in which the bot may delete messages, and both tools require a `tools` list. The actor chat is only
an object, never the target. `telegram.chatactions.send` (`create`, idempotent) shows one of Telegram's fixed
actions for about five seconds.

## Forum topics

`telegram.topics.create` (`create`, not idempotent, never repeated after an unclear result) creates a forum
topic with a `name` of 1 to 128 characters and optionally an `icon_color` from Telegram's fixed list of six
values; its result is limited to `message_thread_id`, `name`, `icon_color`, and `icon_custom_emoji_id`.
`telegram.topics.edit`, `telegram.topics.close`, and `telegram.topics.reopen` (`update`, idempotent) require
exactly one of `message_thread_id` and `general: true`; the choice fixes the method, and anything else is
refused before any request. Editing a topic needs a `name` or an `icon_custom_emoji_id`, and an empty
`icon_custom_emoji_id` removes the icon; the General topic is only renamed (`name` required, no icon).
`icon_custom_emoji_id` is accepted only as a string of 1 to 20 digits; `telegram.topics.iconstickers` lists
the valid values.
Reopening the General topic also unhides it. These results are a single boolean. Deleting topics and hiding
the General topic are not offered; the tools are in no profile and need no `tools` list.

## Locations, venues, contacts, and dice

`telegram.locations.send`, `telegram.venues.send`, `telegram.contacts.send`, and `telegram.dice.send`
(`create`, not idempotent) send one structured value to a bound chat. Every value is checked against the Bot
API limits before the credential is read: coordinates, `horizontal_accuracy` (0 through 1500), `live_period`
(60 through 86400, or 2147483647), `heading` (1 through 360), and `proximity_alert_radius` (1 through 100000).
`heading` and `proximity_alert_radius` need a `live_period`; a live location can be sent but not updated or
stopped. A venue needs `title` and `address`; place identifiers are not offered. A contact needs
`phone_number` and `first_name`, `last_name` is optional, and no vCard is accepted. The dice `emoji` is one of
🎲 (default), 🎯, 🏀, ⚽, 🎳, 🎰. The tools share the optional `reply_to_message_id`, `message_thread_id`,
`disable_notification`, and `protect_content`; keyboards and paid broadcasts are not offered. Locations,
venues, and contacts carry the data class `telegram-personal-data` and appear only in the request, never in
errors, logs, or the audit trail. The result is only `message_id` and `date`.

## Invite links

`telegram.invitelinks.primary` (`read`) returns only the `invite_link` field of the chat, and no other chat
data; `telegram.chats.get` never shows it. The link grants joining the chat and carries the data class
`telegram-invite-link`: it appears only in this result, never in errors, logs, or the audit trail. A value
that is not a `t.me` invite link is dropped.

`telegram.invitelinks.revoke` revokes one link of the bound chat and requires a `tools` list. It accepts only
`https://t.me/+TOKEN` or `https://t.me/joinchat/TOKEN` with the exact host `t.me`, checked before the
credential is read. The request carries only the bound chat and the link, so Telegram refuses links of other
chats. The result is only `revoked: true`, never the link. When the primary link is revoked, Telegram
generates a new primary link by itself; the result does not contain it, and creating, exporting, or editing
invite links is not offered.

## Reading updates

`telegram.updates.list` (`read`) shows the pending updates of the bot, without `chat`, for every bound chat.
It needs at least one chat target. Only updates of bound chats appear; every other update, whatever its chat
or type, is only counted in `skipped`, and `last_update_id` names the highest update seen, skipped ones
included. An `@username` target is matched through a fixed `getChat` call on the configured name; the ID that
returns is used for matching only. A long poll (`wait_seconds`) waits for a new update.

`telegram.updates.list` acknowledges nothing and stores no offset: the same updates appear again on the next
call until Telegram drops them, which it does after at most 24 hours. Telegram allows one consumer per bot. The
call fails with a conflict while a webhook is active or another `getUpdates` consumer runs, and a long poll
interrupts the long poll of another consumer of the same bot.

`telegram.updates.confirm` (`delete`, confirmation required) makes Telegram drop all updates up to and
including `update_id`, so the next `telegram.updates.list` shows only newer ones. It needs the target `bot`, is
released only through the connection's tools list, and belongs to no profile. Its effect is bot-wide: it
confirms the updates of every chat of the bot, including chats this connection does not bind, and it is
the only way an update leaves the list before Telegram drops it. It fails with a conflict while a webhook is
active, and another poller of the same bot loses the confirmed updates or competes for the offset. A failure
whose outcome is unknown is reported as such and never repeated. The result contains only `confirmed_through`.

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

## Changing chat settings and leaving

`telegram.chats.settitle` (1 through 128 characters), `telegram.chats.setdescription` (up to 255 characters;
the argument is required, and an empty string removes the description), `telegram.chats.setphoto` (`update`),
and `telegram.chats.deletephoto` (`delete`) change the master data of a bound group or channel; Telegram
refuses private chats. Lengths are checked before the credential is read, and each result is only
`updated: true` or `deleted: true`. `telegram.chats.deletephoto` requires a `tools` list.

`telegram.chats.setphoto` takes only a `local_path` inside a directory the connection releases for reading
(`files`), never a `file_ref` or URL, and repeating it has an unknown effect. The photo is limited to 10 MB,
checked from the file before it is read, and sent under a neutral name, so neither path nor file name leaves
the machine.

`telegram.chats.setpermissions` sets the default member permissions of a bound group or supergroup. Like
`telegram.members.restrict`, it takes the complete `permissions` object: every boolean is required, a missing
or unknown field is refused before any request, and all are sent explicitly with independent permissions.
`telegram.chats.setstickerset` sets the group sticker set by name, never a URL; as a narrower reading than
Telegram, the name must be 1 through 64 letters, digits, or underscores, checked before the credential is
read. `telegram.chats.deletestickerset` (`delete`) removes it. The results are `updated: true` or
`deleted: true`.

`telegram.chats.leave` (`delete`) makes the bot leave a bound chat. The bot then has no access to it, and only
a human can add it again. `telegram.chats.deletestickerset` and `telegram.chats.leave` require a `tools`
list.

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

`telegram.photos.send`, `telegram.documents.send`, `telegram.videos.send`, `telegram.animations.send`,
`telegram.videonotes.send`, and `telegram.mediagroups.send` (`create`) send a photo, a document, a video, an
animation, a video note, or an album. An album has 2 through 10 items: photos and videos may be mixed, with an
optional `type` per item, documents are never mixed with them. Each file comes from exactly one of `local_path`
or `file_ref`; an album takes the choice per item. A URL is never a source. The tools are offered only on a
connection that releases a directory for reading (`files`), and the options of `telegram.messages.send` that
apply (`parse_mode`, `reply_to_message_id`, `message_thread_id`, `disable_notification`, `protect_content`)
carry over, plus a `caption` of at most 1024 characters. An album caption goes on its first item. A video note
has no caption and no `parse_mode`, as the Bot API gives it none. Duration, size, thumbnail, streaming, and
spoiler settings are not offered; Telegram reads them from the file.

A `local_path` outside the released directories is refused before the credential is read. A photo is limited
to 10 MB, any other file, videos included, to 50 MB, and the files of one album together to 50 MB; the size is
checked from the file before it is read or sent. A local file is sent under a neutral name (`file` or `file-N`,
plus the plain extension), so neither the path nor the file name leaves the machine.

A `file_ref` must come from the selected chat itself: one issued for another target, even another bound chat,
for another token, or of another kind is refused before the credential is read, and a raw file identifier is
never accepted. Its size is not known locally; Telegram enforces the limits for it.

The result is `message_id`, `date`, and a `file_ref` of the sent file, for an album one such entry per message
under `messages`. Each tool sends exactly one request and reports an unclear outcome instead of repeating it.

## Stickers

`telegram.stickers.send` (`create`) sends one sticker to a bound chat, from exactly one of a `local_path` or a
`file_ref`; a URL is never a source. A `local_path` must end in `.webp`, `.tgs`, or `.webm` (checked before
anything else), lie in a directory the connection releases for reading (`files`), and be at most 512 KB; it is
sent under a neutral name, with an optional `emoji`. A `file_ref` is accepted when it is bound to the selected
chat or to `bot`, the binding of every sticker the read tools return; any other binding, token, or kind is
refused before the credential is read, and `emoji` is refused with it. The options `reply_to_message_id`,
`message_thread_id`, `disable_notification`, and `protect_content` carry over; there is no reply markup and no
business or paid option. The result is `message_id` and `date`. One request is sent, and an unclear outcome is
reported instead of repeated.

`telegram.stickersets.get` (name of 1 through 64 letters, digits, or underscores), `telegram.stickers.customemoji`
(1 through 200 identifiers of digits), and `telegram.topics.iconstickers` (`read`) read public catalog data and
run on every connection without a `bot` target. They return only a fixed set of fields per sticker, at most 200
stickers, with texts shortened; a sticker carries a `file_ref` only on a connection that binds the `bot`
target, signed with the binding `bot`. The stickers tools are in no setup profile.

## Setup profiles

The terminal editor starts a new connection on the setup profile `send`, which ticks `[create]` and
`[telegram.messages.send]`: the read tools expose incoming message content, while a send reaches only a bound
chat, each one after confirmation. The profile `read` ticks `[read]`, `[telegram.updates.list]`,
`[telegram.bot.get]`, and the four chat read tools. The profile `messaging` also ticks `update`,
`delete`, `telegram.messages.edit`, and `telegram.messages.delete`; the profile `pins` ticks `update`,
`telegram.pins.pin`, and `telegram.pins.unpin`; the profile `media` ticks `create` and the six media send
tools; the profile `moderation` ticks `update` and `telegram.members.unban`; the profile `chat-admin`
ticks `update`, `telegram.chats.settitle`, `telegram.chats.setdescription`, and `telegram.chats.setphoto`.
`telegram.messages.editreplymarkup`, `telegram.pins.unpinall`, `telegram.members.ban`,
`telegram.members.restrict`, `telegram.members.promote`, `telegram.members.setadmintitle`,
`telegram.members.settag`, `telegram.senderchats.ban`, `telegram.senderchats.unban`,
`telegram.chats.deletephoto`, `telegram.chats.setpermissions`, `telegram.chats.setstickerset`,
`telegram.chats.deletestickerset`, `telegram.chats.leave`, `telegram.messages.forward`,
`telegram.messages.copy`, the interaction tools including locations, venues, contacts, and dice, and the
invite link tools are in no profile. A profile is a visible starting selection, not a role: only the ticked
`permissions` and `tools` are saved, every tick can be changed before saving, and a saved connection never
follows a profile.
