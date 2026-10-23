---
description: >
  Describes how Telegram updates are read, which are shown, and how they are confirmed.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-23
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

## Message members

The messages of updates, `telegram.users.personal-chat-messages`, and the `pinned_message` of
`telegram.chats.get` share one mapping of content and service members, and a replied-to message is followed one
level. Telegram texts are untrusted: they are length-capped and stripped of control characters, and `entities` and
`caption_entities` keep at most 100 entries.

`chat_info`, `sender_chat`, `forward_origin`, and `external_reply` can name chats outside the target binding; they
appear as data and never become a target. A chat of a business connection appears only as `business_chat_ref`.
`guest_query_id` appears only as a signed `guest_query_ref`. Addresses in `entities`, `link_preview_options`,
`reply_markup`, and `invoice` go out as untrusted text and are never fetched. A button's `callback_data` is shown
like that of a `callback_query` update.

## Service members

A service message carries every documented service member of `Message`. Chats and people named there are data
only: `migrate_to_chat_id`, `migrate_from_chat_id`, and the chat of `chat_shared` never become a target. Photos
appear only as signed `file_ref`, none in business messages. `web_app_data`, `connected_website`, and the names in
`users_shared` and `chat_shared` are untrusted text.

A member that holds a message (`pinned_message` and the message of checklist, giveaway, poll option, and
suggested post events) is mapped one level deep, with the same mapper. An inaccessible message appears only with
`chat_info`, `message_id`, and a `date` of 0. Inside a replied-to or pinned message such a member is left out and
named in `omitted_fields`.

Payment identifiers such as `telegram_payment_charge_id`, and `owned_gift_id` of gifts, are printed: no tool
refunds, transfers, or converts with them. `passport_data` is dropped (see [Nested types](README.md#nested-types)).
