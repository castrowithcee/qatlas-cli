---
description: >
  Describes the Telegram tools that answer callback queries, join request queries, and inline queries.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-23
---

# Queries

`telegram.callbacks.answer` answers a `callback_ref`, and `telegram.join-request-queries.answer` declines or
queues a `join_request_ref`; Telegram expects the answers right after the press and within 10 seconds
respectively. Both references are [signed references](README.md#signed-references) taken from
[updates](updates.md#references-in-updates).

`telegram.inline.answer` (`create`, `tools` list) needs `bot` and an `inline_ref` and sends text articles only,
never a URL, media, or keyboard.

The `callback_ref` of a callback on an inline message has no chat and is bound to `bot`, like the
[chatless references of updates](updates.md).
