---
description: >
  Describes the Telegram tools that send stickers, read sticker catalogs, and manage the bot's own sticker sets.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Stickers

`telegram.stickers.send` sends one sticker to a bound chat from a `local_path` (a `.webp`, `.tgs`, or `.webm` file
of at most 512 KB) or a `file_ref` bound to the selected chat or to `bot`, never from a URL.
`telegram.sticker-sets.get`, `telegram.stickers.custom-emoji`, and `telegram.topics.icon-stickers` read public
catalog data on every connection and return fixed fields per sticker, with a `bot`-bound `file_ref` only on a
connection that binds the `bot` target.

## Managing sticker sets

The set tools `telegram.stickers.upload-file`, `telegram.sticker-sets.create`, `telegram.sticker-sets.add-sticker`,
and `telegram.sticker-sets.delete` and the sticker tools `telegram.stickers.set-position`,
`telegram.stickers.replace`, and `telegram.stickers.delete` need the `bot` target. A set belongs to the given
Telegram user, not to a chat, and takes files only as `bot`-bound references from `stickers.upload-file`. Only the
bot's own sets change: the name must end in `_by_` and the bot's username, checked with one `getMe` read, and a
sticker, addressed by the set `name` and a `bot`-bound `file_ref`, must belong to that set, checked with reads.
Deleting a set or removing a sticker is final and available only through a tools list. The setters
`telegram.stickers.set-emoji-list`, `telegram.stickers.set-keywords`, `telegram.stickers.set-mask-position`,
`telegram.sticker-sets.set-title`, `telegram.sticker-sets.set-thumbnail`, and
`telegram.sticker-sets.set-custom-emoji-thumbnail` follow the same rules; a thumbnail comes only from a `local_path`,
and an omitted mask position or thumbnail or an empty keyword list removes the value.
