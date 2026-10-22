---
description: >
  Describes the Telegram tools that read chats and change chat settings or leave a chat.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Chats

## Reading chats

The four chat read tools address a bound chat through [`chat`](README.md#chat-argument). `pinned_message_id` is
only a number, never the message. `personal_chat`, `parent_chat`, and `linked_chat_id` name chats outside the
target binding; they appear as data and never become a target. `telegram.chats.member` shows one member by
`user_id`. An invite link grants access without sign-in, so `telegram.chats.get` never returns it; it is reachable
only through the [invite link tools](invite-links.md). Strings are length-capped. Member data is classified
`telegram-member-data`.

## Changing chat settings

`telegram.chats.set-title`, `telegram.chats.set-description` (an empty string removes the description),
`telegram.chats.set-photo` (`update`), and `telegram.chats.delete-photo` (`delete`, `tools` list) change the master
data of a bound group or channel; Telegram refuses private chats. `set-photo` takes only a `local_path`, never a
`file_ref` or URL, and repeating it has an unknown effect.

`telegram.chats.set-permissions` sets the default member permissions of a bound group or supergroup. Like
[`telegram.members.restrict`](members.md#restricting-and-promoting), it takes the complete `permissions` object:
every boolean is required, a missing or unknown field is refused before any request, and all are sent explicitly
with independent permissions.
`telegram.chats.set-sticker-set` sets the group sticker set by name, never a URL; as a narrower reading than Telegram,
the name must be 1 through 64 letters, digits, or underscores, checked before the credential is read.
`telegram.chats.delete-sticker-set` (`delete`, `tools` list) removes it.

## Leaving

`telegram.chats.leave` (`delete`, `tools` list) makes the bot leave a bound chat; only a human can add it again.
