---
description: >
  Describes how Telegram updates are read, which are shown, and how they are confirmed.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Updates

`telegram.updates.list` (`read`) shows the pending updates of the bot for every bound chat, topic, and business
connection; of a topic only its messages and button presses, with the topic target as `chat`. Every other update
is only counted in `skipped`, and `last_update_id` names the highest update seen, skipped ones included. An
`@username` target is matched through a fixed `getChat` call; the ID that returns is used for matching only.
Business updates follow the [business rules](business.md#business-updates).

`telegram.updates.list` acknowledges nothing and stores no offset: the same updates appear again until Telegram
drops them after at most 24 hours. Telegram allows one consumer per bot: the call conflicts with an active webhook
or another `getUpdates` consumer, and a long poll interrupts that of another consumer.

## Confirming

`telegram.updates.confirm` (`delete`, `tools` list) makes Telegram drop all updates up to and including
`update_id`. It needs the target `bot`. Its effect is bot-wide: it confirms the updates of every chat of the
bot, including chats this connection does not bind, and it is the only way an update leaves the list before
Telegram drops it. It fails with a conflict while a webhook is active, and another poller of the same bot loses
the confirmed updates or competes for the offset. The result contains only `confirmed_through`.

## References in updates

File, callback query, and join request query identifiers appear as [signed references](README.md#signed-references);
`join_request_ref` appears only for a join request that carries a query. The tools that accept them are described
in [queries.md](queries.md).

`inline_query` and `chosen_inline_result` updates appear only with the target `bot`; only an `inline_query` has
an `inline_ref`, and a location is never shown.
