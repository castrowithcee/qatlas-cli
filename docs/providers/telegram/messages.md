---
description: >
  Describes Telegram message sending, inline keyboards, editing, deleting, forwarding, and copying.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Messages

`telegram.messages.send` sends text; Telegram's parser decides whether `parse_mode` markup is valid, and
`reply_to_message_id` replies only within the same chat.

## Inline keyboard

`inline_keyboard` attaches an inline keyboard whose buttons carry exactly one of `url` (a plain `https://`
link with a host, or a `tg://` link) or `callback_data`. Web-app, login, pay, and `switch_inline_query` buttons
and reply keyboards are not offered. The limits are checked before the credential is read.

## Editing

`telegram.messages.edit` takes no reply, topic, silent, or protect option; an edit or
`telegram.messages.edit-reply-markup` without `inline_keyboard` removes an existing keyboard.
`telegram.messages.edit-caption` (`update`) with an empty caption removes it. `telegram.messages.edit-media`
(`update`) replaces the media with a photo, video, animation, or document from `local_path` or `file_ref` as the
[media send tools](media.md) do and returns the new `file_ref`.

## Deleting

`telegram.messages.delete` and `.delete-many` (up to 100 messages of one chat) are offered only through a `tools`
list. Their reach is that of `deleteMessage`: the bot deletes its own messages and, as an administrator, those of
others; in a private chat also incoming ones. `.delete-many` skips missing or undeletable identifiers silently.

## Forwarding and copying

`telegram.messages.forward` and `telegram.messages.copy` (`create`) move messages from `from_chat` to `chat`, two
bound chats that follow the [rule for `chat`](README.md#chat-argument) and may be the same. The source is one
`message_id` or `message_ids` in strictly ascending order; another order is refused rather than sorted. `copy`
leaves out the link to the original, and its `caption` replaces the caption of a single message and is refused with
`message_ids`. Telegram skips sources it cannot transfer without notice, so the list can be shorter than the
request.
