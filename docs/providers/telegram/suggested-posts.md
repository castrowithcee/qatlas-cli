---
description: >
  Describes the Telegram tools that approve and decline suggested posts.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Suggested posts

`telegram.suggested-posts.approve` (`create`) and `telegram.suggested-posts.decline` (`update`, `tools` list only,
final) decide a suggested post in the direct messages chat of a channel. That chat must be bound by numeric ID; an
`@username` is refused locally. No price is ever sent.
