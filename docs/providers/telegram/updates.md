---
description: >
  Describes how Telegram updates are read, which are shown, and how they are confirmed.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-23
---

# Updates

`telegram.updates.list` (`read`) shows every update type of the Bot API that its target binding allows: the
pending updates of the bot for every bound chat, topic, and business connection, and chatless updates with the
target `bot`. Of a topic only its messages, button presses, and stopped generations appear, with the topic target
as `chat`. Every update the binding does not allow, and every type Telegram adds later, is only counted in
`skipped`, and `last_update_id` names the highest update seen, skipped ones included. An `@username` target is
matched through a fixed `getChat` call; the ID that returns is used for matching only. Business updates follow
the [business rules](business.md#business-updates).

Updates without a chat (inline queries, chosen inline results, callbacks of inline messages, shipping and
pre-checkout queries, purchased paid media, polls, poll answers, managed bots, subscriptions) appear only on a
connection with the target `bot`. The people who trigger them are in that binding, so their user, address, and
order data are shown. `voter_chat` and `actor_chat` are chats and never become a target.

`telegram.updates.list` acknowledges nothing and stores no offset: the same updates appear again until Telegram
drops them after at most 24 hours. Telegram allows one consumer per bot: the call conflicts with an active webhook
or another `getUpdates` consumer, and a long poll interrupts that of another consumer.

`allowed_updates` of `getUpdates` is no filter: Telegram stores the value permanently and bot-wide for every
consumer. `telegram.updates.list` therefore never sends it.

## Confirming

`telegram.updates.confirm` (`delete`, `tools` list) makes Telegram drop all updates up to and including
`update_id`. It needs the target `bot`. Its effect is bot-wide: it confirms the updates of every chat of the
bot, including chats this connection does not bind, and it is the only way an update leaves the list before
Telegram drops it. It fails with a conflict while a webhook is active, and another poller of the same bot loses
the confirmed updates or competes for the offset. The result contains only `confirmed_through`.

## References in updates

File, callback query, join request query, guest query, shipping query, pre-checkout query, and inline message
identifiers appear as [signed references](README.md#signed-references); `join_request_ref` appears only for a join
request that carries a query. The identifiers of shipping and pre-checkout queries and of inline messages have no
chat and are bound to `bot`. The tools that accept references are described in [queries.md](queries.md).

The location of an inline query or chosen inline result is data the requesting person gave the bot, and it belongs
to the `bot` binding. An invite link in member and join request events grants access without a login, so only its
string is dropped; its other members are shown.

## Message members

The messages of updates, `telegram.users.personal-chat-messages`, and the `pinned_message` of
`telegram.chats.get` share one mapping, and a replied-to message is followed one level. Telegram texts are
untrusted: they are length-capped and stripped of control characters, and `entities` and `caption_entities` keep
at most 100 entries.

`chat_info`, `sender_chat`, `forward_origin`, and `external_reply` can name chats outside the target binding; they
appear as data and never become a target. A chat of a business connection appears only as `business_chat_ref`.
`guest_query_id` appears only as a signed `guest_query_ref`. Addresses in `entities`, `link_preview_options`,
`reply_markup`, and `invoice` go out as untrusted text and are never fetched. A button's `callback_data` is shown
like that of a `callback_query` update.
