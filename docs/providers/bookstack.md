---
description: >
  Describes BookStack page operations, credentials, connection permissions, and safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-09-23
---

# BookStack

A connection selects one BookStack instance and one API token. It supports listing and reading pages
(`read`), creating pages (`create`), changing title or Markdown (`update`), and deleting pages (`delete`).
Create requires exactly one `book_id` or `chapter_id`; all mutations require confirmation.

Credentials provide `token-id` and `token-secret`. The connection's `permissions` list is an independent
local ceiling: it may hide and block operations even when the BookStack token could perform them, but it
cannot grant rights the token lacks. An optional `tools` list narrows a connection further to named tools,
for example `[bookstack.pages.get]` for a route that reads a known page but cannot list pages; it never
admits an effect `permissions` excludes. Page content is treated as untrusted data. The terminal editor
starts a new connection on the setup profile `read`, which ticks `[read]` and
`[bookstack.pages.list, bookstack.pages.get]`. A profile is a visible starting selection, not a role: only the
ticked `permissions` and `tools` are saved, every tick can be changed before saving, and a saved connection
never follows a profile.

## Limits

Reading accepts a response of up to 16 MiB; a larger one fails as `invalid-provider-response`. Writing
accepts at most 1 MiB of page content. Redirects are never followed: a 3xx answer is a `provider-error`.

## Deleting

`bookstack.pages.delete` moves the page to the BookStack recycle bin; it does not erase it permanently.
Behavior change: the tool requires a tool allow list and is part of no profile. A connection offers it only
when its `tools` list names `bookstack.pages.delete` and its `permissions` include `delete`; a connection
without a `tools` list no longer offers it.

## Errors

Errors carry a fixed class and never the text BookStack sent.

| Status | Class |
| --- | --- |
| 401 | `auth` |
| 403 | `permission`: the token's user lacks the BookStack role permission for this action, or the token expired or lacks API access (the connection test reports 403 as `auth`) |
| 404 | `not-found` |
| 429 | `rate-limited` |
| 422 | `provider-error`, naming at most the invalid argument names, never values |
| other | `provider-error (HTTP n)` |

A create, update, or delete that ends in a timeout, a cancellation, a 5xx answer, or an unreadable answer
adds: "this change may have taken effect, read the current state in BookStack before repeating it". Each
change is sent exactly once and never retried.
