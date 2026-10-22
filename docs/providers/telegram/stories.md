---
description: >
  Describes the Telegram story tools of business connections and their restrictions.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Stories

The story tools share target, data class, and setup profile rules with the [business tools](business.md).

`telegram.stories.delete` needs a `tools` list. `telegram.stories.post` and `.edit` take media only from
`local_path`, and `.repost` copies only between bound connections.
