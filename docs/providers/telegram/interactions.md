---
description: >
  Describes the Telegram tools for polls, reactions, chat actions, locations, venues, contacts, and dice.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Interactions

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

## Locations, venues, contacts, and dice

`telegram.locations.send`, `telegram.venues.send`, `telegram.contacts.send`, and `telegram.dice.send` (`create`)
send one structured value to a bound chat, checked against the Bot API limits before the credential is read; place
identifiers and vCards are not offered. Keyboards and paid broadcasts are not offered. Locations, venues, and
contacts carry the data class `telegram-personal-data` and appear only in the request, never in errors, logs, or the
audit trail. `telegram.locations.edit-live` (`update`) moves a live location within these limits, and omitted
optional values keep their setting; `telegram.locations.stop-live` (`update`, idempotent) ends it through a `tools`
list only.
