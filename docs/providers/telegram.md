---
description: >
  Describes Telegram message, update, file, media, and sticker operations, fixed chat targets, connection
  effects, and safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-13
---

# Telegram

A connection binds one bot token to its targets, given as `target` (one) or `targets` (a list). A target is a chat
(a numeric chat ID, also negative, or an `@username`), `bot`, or `business/<id>`. Every changing tool requires
confirmation, and Telegram's own restrictions still apply.

`telegram.messages.send` sends text; Telegram's parser decides whether `parse_mode` markup is valid, and
`reply_to_message_id` replies only within the same chat.

`inline_keyboard` attaches an inline keyboard whose buttons carry exactly one of `url` (a plain `https://`
link with a host, or a `tg://` link) or `callback_data`. Web-app, login, pay, and `switch_inline_query` buttons
and reply keyboards are not offered. The limits are checked before the credential is read.

`telegram.messages.edit` takes no reply, topic, silent, or protect option; an edit or
`telegram.messages.edit-reply-markup` without `inline_keyboard` removes an existing keyboard.
`telegram.messages.edit-caption` (`update`) with an empty caption removes it. `telegram.messages.edit-media`
(`update`) replaces the media with a photo, video, animation, or document from `local_path` or `file_ref` as the
media send tools do and returns the new `file_ref`.

The chat tools take an optional `chat` argument, the forward and copy tools also `from_chat`. It must equal a
bound chat target exactly: an `@username` never matches a numeric ID, and `bot` or `business/<id>` is no chat.
Without `chat`, the tool uses the one bound chat and refuses when the connection binds none or several. A chat
that is not bound is refused before the credential is read and before any request, without naming it. A chat
ID from an invocation argument or a Telegram response never becomes a target on its own.

`bot` unlocks only bot-wide tools and `business/<id>` only the tools of that one business connection; both
combine with chats, and a connection with only `bot` has no chat. A tool missing its target is refused before the
credential is read, and methods that accept only a numeric chat ID refuse an `@username` chat locally.

The Bot API has no chat list, so setup offers none. A `https://t.me/<username>` link, also with a message
number, becomes the target `@<username>`, and a `https://t.me/c/<id>/...` link becomes `-100<id>`. No request is
made. Invite links and other `t.me` feature paths name no chat and yield no target.

Tool groups only sort tools for display. The base URL must be a plain `https` URL with a host and without user,
query, or fragment, with no exception for local addresses, and redirects are never followed. `config validate`
rejects any other base URL. Errors never carry Telegram's own error text.

A tool that takes a `local_path` is offered only on a connection that releases a directory for it (`files`): for
reading to upload, for writing to download. A path outside it is refused before the credential is read, and an
upload's size is checked before the file is read. A local file is sent under a neutral name, so path and file
name stay local.

The credential provides `bot-token`. Connection effects only reduce what Qatlas exposes and executes; they do
not broaden the bot's provider-side rights. A connection may instead select named tools with a `tools` list,
for example `[telegram.messages.send]` for a chat that may receive but never lose messages. Mutations are never retried after an ambiguous network result.

`telegram.messages.delete` is offered only through a `tools` list, since no effect offers it. Its reach is
that of the Bot API `deleteMessage`: in a bound chat the bot deletes its own messages and, as an administrator,
those of others; in a private chat it also deletes incoming messages.

`telegram.messages.delete-many` deletes up to 100 messages of one chat, with the reach of
`telegram.messages.delete` and also only through a `tools` list. Telegram skips missing or undeletable
identifiers silently, so success does not say which messages are gone.

`telegram.messages.forward` and `telegram.messages.copy` (`create`) move messages from `from_chat` to `chat`, two
bound chats that follow the rule for `chat` above and may be the same. The source is one `message_id` or
`message_ids` in strictly ascending order; another order is refused rather than sorted. `copy` leaves out the link
to the original, and its `caption` replaces the caption of a single message and is refused with `message_ids`.
Telegram skips sources it cannot transfer without notice, so the list can be shorter than the request.

`telegram.pins.unpin-all` unpins all pinned messages of the chat, requires a `tools` list, and has no topic form.

## Moderating members

`telegram.members.ban` (`delete`) bans one user and requires a `tools` list; `until_date` limits the ban, and
`revoke_messages` also deletes the user's messages. `telegram.members.unban` (`update`) always sends
`only_if_banned`, so a current member is never removed and nobody is invited. `telegram.members.restrict`
(`update`) requires a `tools` list and sets the complete `permissions` object of a user in a supergroup: every
ChatPermissions boolean is required and sent explicitly, a missing or unknown field is refused before any
request, and permissions are independent (`use_independent_chat_permissions`). `telegram.sender-chats.ban`
(`delete`, `tools` list) and `telegram.sender-chats.unban` (`update`) act on a channel as sender; it is only the
object of the call, never the target.

`telegram.join-requests.decline` (`update`) declines the pending join request of `user_id` for a bound chat; joining
is never approved by any tool. `telegram.suggested-posts.approve` (`create`) and `telegram.suggested-posts.decline`
(`update`, `tools` list only, final) decide a suggested post in the direct messages chat of a channel. That chat
must be bound by numeric ID; an `@username` is refused locally. No price is ever sent.

`telegram.members.promote` (`update`) requires a `tools` list and sets the complete `rights` object of an
existing member the way `restrict` sets permissions; all `false` demotes. Membership is checked with
`getChatMember` first, and a non-member or an unclear status is refused before any change. `can_promote_members`
and `can_invite_users` widen who may add administrators and members; the bot's default administrator rights are
not offered. `telegram.members.set-admin-title` (an administrator the bot promoted) and `telegram.members.set-tag`
(a regular member) set a value of up to 16 characters without emoji, checked locally and conservatively; an
empty value removes it.

## Polls, reactions, and chat actions

`telegram.polls.send` (`create`) sends one poll or quiz as plain text, without entities, media, or paid or
business parameters; Telegram's limits are checked before the credential is read. `telegram.polls.stop`
(`update`) closes a poll of the bot for good, requires a `tools` list, and reports only `stopped`, never counts
or voters. `telegram.reactions.set` (`update`) sets at most one reaction of the bot from Telegram's fixed emoji
list or a `custom_emoji_id`; an empty list removes it, and paid reactions are not offered.
`telegram.reactions.remove` and `telegram.reactions.remove-all` (`delete`) require a `tools` list and remove the
reactions of exactly one user or actor chat from one message or, up to 10000 recent ones, from the whole group;
the actor chat is only an object, never the target. `telegram.chat-actions.send` (`create`) shows one of
Telegram's fixed actions for about five seconds.

## Forum topics

`telegram.topics.create` (`create`) creates a forum topic with a `name` and optionally an `icon_color` from
Telegram's fixed list. `telegram.topics.edit`, `telegram.topics.close`, `telegram.topics.reopen`, and
`telegram.topics.unpin-all` (`update`) require exactly one of `message_thread_id` and `general: true`, which fixes
the method. Editing needs a `name` or an `icon_custom_emoji_id`, a value `telegram.topics.icon-stickers` lists,
and an empty one removes the icon; the General topic is only renamed. Reopening the General topic also unhides
it; `telegram.topics.hide-general` hides and closes it, and `telegram.topics.unhide-general` shows it again.
`telegram.topics.unpin-all` unpins all messages of a topic, and `telegram.topics.delete` (`delete`) removes a
topic with its messages; both require a `tools` list.

## Locations, venues, contacts, dice, and games

`telegram.locations.send`, `telegram.venues.send`, `telegram.contacts.send`, and `telegram.dice.send` (`create`)
send one structured value to a bound chat, checked against the Bot API limits before the credential is read; place
identifiers and vCards are not offered. The tools share `reply_to_message_id`, `message_thread_id`,
`disable_notification`, and `protect_content`; keyboards and paid broadcasts are not offered. Locations, venues, and
contacts carry the data class `telegram-personal-data` and appear only in the request, never in errors, logs, or the
audit trail. `telegram.locations.edit-live` (`update`) moves a live location within these limits, and omitted
optional values keep their setting; `telegram.locations.stop-live` (`update`, idempotent) ends it through a `tools`
list only.

`telegram.games.send` (`create`), `telegram.games.set-score` (`update`), and `telegram.games.high-scores` (`read`)
send a game registered with BotFather, set a player's score, and list the scores around a player, only in a chat
bound by its numeric ID. Scores carry the data class `telegram-member-data`; inline game messages and keyboards are
not offered.

## Invite links

`telegram.invite-links.primary` (`read`) returns only the `invite_link` field of the chat, and no other chat
data; `telegram.chats.get` never shows it. The link grants joining the chat and carries the data class
`telegram-invite-link`: it appears only in this result, never in errors, logs, or the audit trail. A value
that is not a `t.me` invite link is dropped.

`telegram.invite-links.revoke` revokes one link of the bound chat and requires a `tools` list. It accepts only
`https://t.me/+TOKEN` or `https://t.me/joinchat/TOKEN` with the exact host `t.me`, checked before the
credential is read. The request carries only the bound chat and the link, so Telegram refuses links of other
chats. The result is only `revoked: true`, never the link. Revoking the primary link makes Telegram generate a
new one that the result does not contain; creating, exporting, or editing invite links is not offered.

## Reading updates

`telegram.updates.list` (`read`) shows the pending updates of the bot, without `chat`, for every bound chat and
business connection. It needs at least one chat or business target. Every other update, whatever its chat or
type, is only counted in `skipped`, and `last_update_id` names the highest update seen, skipped ones included. An
`@username` target is matched through a fixed `getChat` call on the configured name; the ID that returns is used
for matching only. A long poll (`wait_seconds`) waits for a new update.

`telegram.updates.list` acknowledges nothing and stores no offset: the same updates appear again until Telegram
drops them after at most 24 hours. Telegram allows one consumer per bot: the call conflicts with an active webhook
or another `getUpdates` consumer, and a long poll interrupts that of another consumer.

`telegram.updates.confirm` (`delete`, `tools` list) makes Telegram drop all updates up to and including
`update_id`. It needs the target `bot`. Its effect is bot-wide: it confirms the updates of every chat of the
bot, including chats this connection does not bind, and it is the only way an update leaves the list before
Telegram drops it. It fails with a conflict while a webhook is active, and another poller of the same bot loses
the confirmed updates or competes for the offset. The result contains only `confirmed_through`.

Identifiers that belong to no chat (a file, a callback query, a join request query) are returned only as signed
references, never as Telegram's raw identifier; `join_request_ref` appears only for a join request that carries a
query. A reference is bound to its target and the bot token, so rotating the token invalidates earlier ones; it is
signed, not secret. A tool that accepts one checks format and target before the credential is read and the signature
before any request. `telegram.callbacks.answer` answers a `callback_ref`, and `telegram.join-request-queries.answer`
declines or queues a `join_request_ref`; Telegram expects the answers right after the press and within 10 seconds
respectively.

`inline_query` and `chosen_inline_result` updates appear only with the target `bot`; only an `inline_query` has
an `inline_ref`, and a location is never shown. `telegram.inline.answer` (`create`, `tools` list) needs `bot` and
an `inline_ref` and sends text articles only, never a URL, media, or keyboard.

## Business connections

The tools of the groups `business` and `stories` need the target `business/<id>` of the connection named in
`business`, carry the data class `telegram-business-content`, and are in no setup profile. `telegram.updates.list`
shows business updates only for a bound connection, as `business/<id>` in `chat`; their chat appears only as
`business_chat_ref`, a signed reference bound to that connection, and a business file has no `file_ref`. Only such a
reference addresses a chat: `telegram.business.mark-read` marks a message as read, `telegram.messages.send` with
`business` replies to it and `telegram.checklists.send` and `.edit` post and replace a checklist in it; none takes
topic or keyboard, and a repeated send can post twice. A `tools` list is needed for
`telegram.business.delete-messages`, which does not report skipped messages, `.set-username`, as the old name can be
lost, `.remove-photo`, and `telegram.stories.delete`. `telegram.business.set-photo` (a static JPEG) and
`telegram.stories.post` and `.edit` take media only from `local_path`, and `.repost` copies only between bound
connections; `telegram.business.gifts` and `.star-balance` only read, with no identifier to move or convert a gift.

## Reading chats and members

The four chat read tools address a bound chat through `chat` as above and return fixed fields only:
`pinned_message_id` is only a number, never the message, and `linked_chat_id` is never a target.
`telegram.chats.administrators` lists at most 200 administrators, and `telegram.chats.member` shows one member by
`user_id`. The invite link is never returned; strings are length-capped. Member data is classified
`telegram-member-data`.

`telegram.users.profile-photos`, `.profile-audios`, `.chat-boosts`, and `.personal-chat-messages` read profile and boost
data of a `user_id`, at most 20 entries. Each call first checks with `getChatMember` that the user is a current
member of the bound chat and refuses a non-member before the actual request. Profile files appear only as a
`file_ref` bound to that chat. `.chat-boosts` needs administrator rights of the bot. Membership is checked on issue
only: a `file_ref` stays redeemable through `telegram.files.get` and `.download` after the user leaves.

## Stars and gifts

`telegram.payments.star-balance`, `.star-transactions`, `telegram.gifts.available`, `.chat`, and `.user` (`read`)
only read. The first three need the `bot` target, `.chat` follows the rules for `chat`, and `.user` checks
membership first, as `telegram.users.*` do. They carry the data class `telegram-financial`; a transaction partner
shows only its type and, for a user or chat, its numeric ID, and no gift identifier that could transfer, upgrade, or
convert a gift is returned. Tools that pay, refund, send gifts, or convert Stars are not offered.

## Changing chat settings and leaving

`telegram.chats.set-title`, `telegram.chats.set-description` (an empty string removes the description),
`telegram.chats.set-photo` (`update`), and `telegram.chats.delete-photo` (`delete`, `tools` list) change the master
data of a bound group or channel; Telegram refuses private chats. `set-photo` takes only a `local_path`, never a
`file_ref` or URL, and repeating it has an unknown effect.

`telegram.chats.set-permissions` sets the default member permissions of a bound group or supergroup. Like
`telegram.members.restrict`, it takes the complete `permissions` object: every boolean is required, a missing or
unknown field is refused before any request, and all are sent explicitly with independent permissions.
`telegram.chats.set-sticker-set` sets the group sticker set by name, never a URL; as a narrower reading than Telegram,
the name must be 1 through 64 letters, digits, or underscores, checked before the credential is read.
`telegram.chats.delete-sticker-set` (`delete`, `tools` list) removes it. `telegram.chats.leave` (`delete`, `tools`
list) makes the bot leave a bound chat; only a human can add it again.

## Bot identity and settings

`telegram.bot.get` (`read`) shows the identity of the bot behind the token. It runs on every connection, since it
concerns only the connection's own token; every other tool of this section needs the `bot` target.
`telegram.webhook.get` (`read`) shows whether a webhook is active, the number of waiting updates, and the time of
the last delivery error, never the webhook URL.

`telegram.commands.get` (`read`), `telegram.commands.set` (`update`), and `telegram.commands.delete` (`delete`,
`tools` list) read, replace, and remove the commands of one `scope` and `language_code`; the scopes `chat`,
`chat_administrators`, and `chat_member` require a named bound `chat`. `telegram.menu-button.get` (`read`) never
shows a web app URL, and `telegram.menu-button.set` (`update`) sets `commands` or `default`, never a web app. Both
address the default button or, with a bound `chat` given by numeric ID, that private chat.

`telegram.profile.get` (`read`) and `telegram.profile.set-name`, `.set-description`, and `.set-short-description`
(`update`) read and set the bot's texts per `language_code`; an empty value removes that language's version.
`telegram.profile.set-photo` (`update`) sets a static JPEG from a `local_path`, and `telegram.profile.remove-photo`
(`delete`, `tools` list) removes it. `telegram.default-admin-rights.get` (`read`) and
`telegram.default-admin-rights.set` (`update`, `tools` list) read and replace the rights the bot requests when
added, every right given explicitly.

## Files

`telegram.files.get` (`read`) returns the size and `file_unique_id` of one file; `telegram.files.download` (`read`)
writes it to `local_path`. Both take only a `file_ref` from `media.file_ref` of `telegram.updates.list`: a raw file
identifier and a reference of another target, token, or kind are refused.
`telegram.files.download` replaces an existing file only with confirmation, and a failed or incomplete transfer
leaves no file. The result carries size and SHA-256, never the content. Files above 20 MB are refused before the
download, and a longer response than reported is rejected. The download URL carries the bot token; it and Telegram's
`file_path` appear in no output, error, or log, and a redirect is never followed.

## Sending media

`telegram.photos.send`, `telegram.documents.send`, `telegram.videos.send`, `telegram.animations.send`,
`telegram.video-notes.send`, `telegram.audio.send`, `telegram.voice.send`, and `telegram.media-groups.send` (`create`)
send one file of that kind or an album of 2 through 10 items. Photos and videos may be mixed in an album, with an
optional `type` per item; documents and audio files form albums of their own, and a voice message is never part of
one. Each file comes from exactly one of `local_path` or `file_ref`, an album takes the choice per item, and a URL
is never a source. The options of `telegram.messages.send` that apply (`parse_mode`, `reply_to_message_id`,
`message_thread_id`, `disable_notification`, `protect_content`) carry over, plus a `caption` of at most 1024
characters, which an album shows on its first item; a video note takes neither caption nor `parse_mode`. A
`file_ref` must come from the selected chat itself; any other one, as under Files, is refused before the credential
is read, even from another bound chat. Its size is not known locally; Telegram enforces the limits.

`telegram.rich-messages.send` (`create`) sends one rich message of the modeled block types, which its schema lists.
Files come only from `local_path` or a `file_ref` of the selected chat, never a URL; buttons follow the inline
keyboard rules, with no web-app or login targets, and drafts are not offered. A repeated send can post twice.

`telegram.live-photos.send` (`create`) sends a live photo: a video of at most 10 seconds and 10 MB from
`local_path` or `file_ref`, and its still image of up to 10 MB from `photo_local_path` or `photo_file_ref`. Both
are required and follow the rules above; the options are those of the other media tools, and a live photo is
never part of an album. Its result `file_ref` is that of the video.

## Stickers

`telegram.stickers.send` sends one sticker to a bound chat from a `local_path` (a `.webp`, `.tgs`, or `.webm` file
of at most 512 KB) or a `file_ref` bound to the selected chat or to `bot`, never from a URL.
`telegram.sticker-sets.get`, `telegram.stickers.custom-emoji`, and `telegram.topics.icon-stickers` read public
catalog data on every connection and return fixed fields per sticker, with a `bot`-bound `file_ref` only on a
connection that binds the `bot` target.

The set tools `telegram.stickers.upload-file`, `telegram.sticker-sets.create`, `telegram.sticker-sets.add-sticker`,
and `telegram.sticker-sets.delete` and the sticker tools `telegram.stickers.set-position`,
`telegram.stickers.replace`, and `telegram.stickers.delete` need the `bot` target. A set belongs to the given
Telegram user, not to a chat, and takes files only as `bot`-bound references from `stickers.upload-file`. Only the bot's own
sets change: the name must end in `_by_` and the bot's username, checked with one `getMe` read, and a sticker,
addressed by the set `name` and a `bot`-bound `file_ref`, must belong to that set, checked with reads. Deleting a
set or removing a sticker is final and available only through a tools list. The setters
`telegram.stickers.set-emoji-list`, `telegram.stickers.set-keywords`, `telegram.stickers.set-mask-position`,
`telegram.sticker-sets.set-title`, `telegram.sticker-sets.set-thumbnail`, and
`telegram.sticker-sets.set-custom-emoji-thumbnail` follow the same rules; a thumbnail comes only from a `local_path`,
and an omitted mask position or thumbnail or an empty keyword list removes the value.

## Setup profiles

The terminal editor starts a new connection on the setup profile `send` (`[telegram.messages.send]`):
the read tools expose incoming message content, while a send reaches only a bound chat after confirmation. The
profiles `read`, `messaging` (also `telegram.messages.delete`), `pins`, `media`, `moderation` (only
`telegram.members.unban`), `chat-admin`, and `payments-read` (the Stars and gifts tools) select the tools their titles
name. Every other Telegram tool is in no profile. A profile is only a visible starting selection: every tick can be
changed before saving, and a saved connection never follows a profile.
