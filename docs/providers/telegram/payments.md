---
description: >
  Describes the read-only Telegram Stars and gifts tools.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Stars and gifts

`telegram.payments.star-balance`, `.star-transactions`, `telegram.gifts.available`, `.chat`, and `.user` (`read`)
only read. The first three need the `bot` target, `.chat` follows the [rules for `chat`](README.md#chat-argument),
and `.user` checks membership first, as [`telegram.users.*`](users.md) do. They carry the data class
`telegram-financial`; a transaction partner shows only its type and, for a user or chat, its numeric ID. Gifts show
their `owned_gift_id`, but Qatlas offers no tool to transfer, upgrade, or convert a gift, and none to pay, refund,
or send one or to convert Stars. The gift tools pass their filters to Telegram unchanged, so every later page needs
the same filter values as the first. A gift sticker has a `file_ref` bound to the target of the call.
