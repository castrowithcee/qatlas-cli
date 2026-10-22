---
description: >
  Describes the Telegram forum topic tools.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Topics

The tools with `message_thread_id` stay within bound topics as described under
[topic targets](README.md#topic-targets).

`telegram.topics.create` (`create`) creates a forum topic with a `name` and optionally an `icon_color` from
Telegram's fixed list. `telegram.topics.edit`, `telegram.topics.close`, `telegram.topics.reopen`, and
`telegram.topics.unpin-all` (`update`) require exactly one of `message_thread_id` and `general: true`, which fixes
the method. Editing needs a `name` or an `icon_custom_emoji_id`, a value `telegram.topics.icon-stickers` lists,
and an empty one removes the icon; the General topic is only renamed. Reopening the General topic also unhides
it; `telegram.topics.hide-general` hides and closes it, and `telegram.topics.unhide-general` shows it again.
`telegram.topics.unpin-all` unpins all messages of a topic, and `telegram.topics.delete` (`delete`) removes a
topic with its messages; both require a `tools` list.
