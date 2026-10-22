---
description: >
  Describes the Telegram business connection tools: targets, data class, business updates, and restrictions.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Business

The tools of the groups `business` and [`stories`](stories.md) need the target `business/<id>` of the connection
named in `business`, carry the data class `telegram-business-content`, and are in no [setup
profile](README.md#setup-profiles).

## Business updates

`telegram.updates.list` shows business updates only for a bound connection, as `business/<id>` in `chat`; their
chat appears only as `business_chat_ref`, a signed reference bound to that connection, and a business file has no
`file_ref`, also not in `media_items`.

## Tools

Only a `business_chat_ref` addresses a chat: `telegram.business.mark-read` marks a message as read,
`telegram.messages.send` with `business` replies to it and `telegram.checklists.send` and `.edit` post and replace
a checklist in it; none takes topic or keyboard, and a repeated send can post twice. A `tools` list is needed for
`telegram.business.delete-messages`, which does not report skipped messages, `.set-username`, as the old name can be
lost, and `.remove-photo`. `telegram.business.set-photo` (a static JPEG) takes media only from `local_path`;
`telegram.business.gifts` and `.star-balance` only read, with no identifier to move or convert a gift.
