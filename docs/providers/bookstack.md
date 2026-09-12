---
description: >
  Describes BookStack page operations, credentials, connection permissions, and safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-09-12
---

# BookStack

A connection selects one BookStack instance and one API token. It supports listing and reading pages
(`read`), creating pages (`create`), changing title or Markdown (`update`), and deleting pages (`delete`).
Create requires exactly one `book_id` or `chapter_id`; all mutations require confirmation.

Credentials provide `token-id` and `token-secret`. The connection's `permissions` list is an independent
local ceiling: it may hide and block operations even when the BookStack token could perform them, but it
cannot grant rights the token lacks. Page content is bounded to 1 MiB and treated as untrusted data.
