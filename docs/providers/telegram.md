---
description: >
  Describes Telegram message, update, file, media, and sticker operations, fixed chat targets, connection
  permissions, and safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-10
---

# Telegram

A connection binds one bot token to its targets, given as `target` (one) or `targets` (a list). A target is
a chat (a numeric chat ID, also negative, or an `@username`), `bot`, or `business/<id>`. It can send
(`create`), edit (`update`, including the inline keyboard), and delete (`delete`) messages in its chats and pin
or unpin them (`update`), and every operation requires confirmation. Telegram's own edit and delete
restrictions still apply.

`telegram.messages.send` sends text; `parse_mode` (`HTML` or `MarkdownV2`) formats it, and Telegram's parser
decides whether the markup is valid. `reply_to_message_id` replies only within the same chat; `message_thread_id`,
`disable_notification`, `protect_content`, and `disable_link_preview` set topic, silence, protection, and preview.

`inline_keyboard` attaches an inline keyboard whose buttons carry exactly one of `url` (a plain `https://`
link with a host, or a `tg://` link) or `callback_data`. Web-app, login, pay, and `switch_inline_query` buttons
and reply keyboards are not offered. The limits are checked before the credential is read.

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

`telegram.messages.deletemany` deletes up to 100 messages of one chat in one request, with the reach of
`telegram.messages.delete` and also only through a `tools` list. Telegram skips missing or undeletable
identifiers silently, so success does not say which messages are gone.

`telegram.messages.forward` and `telegram.messages.copy` (`create`) move messages from `from_chat` to `chat`,
two bound chats that follow the rule for `chat` above and may be the same. The source is one `message_id` or
`message_ids` in strictly ascending order; another order is refused rather than sorted. `copy` leaves out the
link to the original, and its `caption` replaces the caption of a single message and is refused with
`message_ids`. The result is only `message_id` or `message_ids`; Telegram skips sources it cannot transfer
without notice, so the list can be shorter than the request.

`telegram.pins.pin` pins one message by `message_id`; `disable_notification` pins silently.
`telegram.pins.unpin` unpins `message_id`, or the most recently pinned message when it is omitted.
`telegram.pins.unpinall` unpins every pinned message of the chat and, like `telegram.messages.delete`,
requires a `tools` list. Telegram's own pin rights still apply, and topic-specific unpinning is not offered.

## Moderating members

`telegram.members.ban` (`delete`) bans one user and requires a `tools` list; `until_date` limits the ban, and
`revoke_messages` also deletes the user's messages. `telegram.members.unban` (`update`) always sends
`only_if_banned`, so a current member is never removed and nobody is invited. `telegram.members.restrict`
(`update`) requires a `tools` list and sets the complete `permissions` object of a user in a supergroup: every
ChatPermissions boolean is required and sent explicitly, a missing or unknown field is refused before any
request, and permissions are independent (`use_independent_chat_permissions`). `telegram.senderchats.ban`
(`delete`, `tools` list) and `telegram.senderchats.unban` (`update`) act on a channel as sender; it is only the
object of the call, never the target. Join requests are not offered.

`telegram.members.promote` (`update`) requires a `tools` list and sets the complete `rights` object of an
existing member the way `restrict` sets permissions; all `false` demotes. Membership is checked with
`getChatMember` first, and a non-member or an unclear status is refused before any change. `can_promote_members`
and `can_invite_users` widen who may add administrators and members; the bot's default administrator rights are
not offered. `telegram.members.setadmintitle` (an administrator the bot promoted) and `telegram.members.settag`
(a regular member) set a value of up to 16 characters without emoji, checked locally and conservatively; an
empty value removes it.

## Polls, reactions, and chat actions

`telegram.polls.send` (`create`) sends one poll or quiz as plain text, without entities, media, or paid or
business parameters; Telegram's limits are checked before the credential is read. `telegram.polls.stop`
(`update`) closes a poll of the bot for good, requires a `tools` list, and reports only `stopped`, never counts
or voters. `telegram.reactions.set` (`update`) sets at most one reaction of the bot from Telegram's fixed emoji
list or a `custom_emoji_id`; an empty list removes it, and paid reactions are not offered.
`telegram.reactions.remove` and `telegram.reactions.removeall` (`delete`) require a `tools` list and remove the
reactions of exactly one user or actor chat from one message or, up to 10000 recent ones, from the whole group;
the actor chat is only an object, never the target. `telegram.chatactions.send` (`create`) shows one of
Telegram's fixed actions for about five seconds.

## Forum topics

`telegram.topics.create` (`create`, never repeated after an unclear result) creates a forum topic with a `name`
and optionally an `icon_color` from Telegram's fixed list. `telegram.topics.edit`, `telegram.topics.close`,
`telegram.topics.reopen`, and `telegram.topics.unpinall` (`update`) require exactly one of `message_thread_id`
and `general: true`, which fixes the method. Editing needs a `name` or an `icon_custom_emoji_id`, a value
`telegram.topics.iconstickers` lists, and an empty one removes the icon; the General topic is only renamed.
Reopening the General topic also unhides it; `telegram.topics.hidegeneral` hides and closes it, and
`telegram.topics.unhidegeneral` shows it again. `telegram.topics.unpinall` unpins all messages of a topic, and
`telegram.topics.delete` (`delete`) removes a topic with all its messages; both require a `tools` list.

## Locations, venues, contacts, and dice

`telegram.locations.send`, `telegram.venues.send`, `telegram.contacts.send`, and `telegram.dice.send`
(`create`) send one structured value to a bound chat, checked against the Bot API limits before the credential
is read. A live location can be sent but not updated or stopped; place identifiers and vCards are not offered.
The tools share `reply_to_message_id`, `message_thread_id`, `disable_notification`, and `protect_content`;
keyboards and paid broadcasts are not offered. Locations, venues, and contacts carry the data class
`telegram-personal-data` and appear only in the request, never in errors, logs, or the audit trail. The result
is only `message_id` and `date`.

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
`telegram.videonotes.send`, `telegram.audio.send`, `telegram.voice.send`, and `telegram.mediagroups.send`
(`create`) send one file of that kind or an album of 2 through 10 items. Photos and videos may be mixed in an
album, with an optional `type` per item; documents and audio files form albums of their own, and a voice message
is never part of one. Each file comes from exactly one of `local_path` or `file_ref`, an album takes the choice
per item, and a URL is never a source. The tools are offered only on a connection that releases a directory for
reading (`files`). The options of `telegram.messages.send` that apply (`parse_mode`, `reply_to_message_id`,
`message_thread_id`, `disable_notification`, `protect_content`) carry over, plus a `caption` of at most 1024
characters, which an album shows on its first item; a video note takes neither caption nor `parse_mode`.
Duration, performer, title, thumbnail, streaming, and spoiler settings are not offered.

A `local_path` outside the released directories is refused before the credential is read. A photo is limited
to 10 MB, any other file, videos included, to 50 MB, and the files of one album together to 50 MB; the size is
checked from the file before it is read or sent. A local file is sent under a neutral name (`file` or `file-N`,
plus the plain extension), so neither the path nor the file name leaves the machine.

A `file_ref` must come from the selected chat itself: one issued for another target, even another bound chat,
for another token, or of another kind is refused before the credential is read, and a raw file identifier is
never accepted. Its size is not known locally; Telegram enforces the limits for it.

`telegram.livephotos.send` (`create`) sends a live photo: a video of at most 10 seconds and 10 MB from
`local_path` or `file_ref`, and its still image of up to 10 MB from `photo_local_path` or `photo_file_ref`. Both
are required and follow the rules above; the options are those of the other media tools, and a live photo is
never part of an album. Its result `file_ref` is that of the video.

The result is `message_id`, `date`, and a `file_ref` of the sent file, for an album one such entry per message
under `messages`. Each tool sends exactly one request and reports an unclear outcome instead of repeating it.

## Stickers

`telegram.stickers.send` sends one sticker to a bound chat from a `local_path` (a `.webp`, `.tgs`, or `.webm`
file of at most 512 KB in a released directory, sent under a neutral name) or a `file_ref` bound to the selected
chat or to `bot`, never from a URL. `telegram.stickersets.get`, `telegram.stickers.customemoji`, and
`telegram.topics.iconstickers` read public catalog data on every connection and return fixed fields per sticker,
with a `bot`-bound `file_ref` only on a connection that binds the `bot` target.

The set tools `telegram.stickers.uploadfile`, `telegram.stickersets.create`, `telegram.stickersets.addsticker`,
and `telegram.stickersets.delete` and the sticker tools `telegram.stickers.setposition`,
`telegram.stickers.replace`, and `telegram.stickers.delete` need the `bot` target. A set belongs to the given
Telegram user, not to a chat, and takes files only as `bot`-bound references from `uploadfile`. Only the bot's
own sets change: the name must end in `_by_` and the bot's username, checked with one `getMe` read, and a
sticker, addressed by the set `name` and a `bot`-bound `file_ref`, must belong to that set, checked with reads.
Deleting a set or removing a sticker is final and available only through a tools list. Each changing tool sends
one request and reports an unclear outcome instead of repeating it.

## Setup profiles

The terminal editor starts a new connection on the setup profile `send`, which ticks `[create]` and
`[telegram.messages.send]`: the read tools expose incoming message content, while a send reaches only a bound
chat, each one after confirmation. The profile `read` ticks `[read]`, `[telegram.updates.list]`,
`[telegram.bot.get]`, and the four chat read tools. The profile `messaging` also ticks `update`,
`delete`, `telegram.messages.edit`, and `telegram.messages.delete`; the profile `pins` ticks `update`,
`telegram.pins.pin`, and `telegram.pins.unpin`; the profile `media` ticks `create` and the nine media send
tools; the profile `moderation` ticks `update` and `telegram.members.unban`; the profile `chat-admin`
ticks `update`, `telegram.chats.settitle`, `telegram.chats.setdescription`, and `telegram.chats.setphoto`.
Every other Telegram tool is in no profile. A profile is a visible starting selection, not a role: only the
ticked `permissions` and `tools` are saved, every tick can be changed before saving, and a saved connection
never follows a profile.
