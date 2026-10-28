---
description: >
  Describes the Excalidraw+ workspace activity log read, its filters, and its personal data.
type: knowledge
edit: shared
created: 2026-10-28
updated: 2026-10-28
---

# Excalidraw+ activity log

Configuration, scope, targets, error classes, and cross-group limits are in [README.md](README.md).

## Reading the log

`excalidraw-plus.logs.list` reads the workspace activity log (`GET /logs`). It is read-only, safe to repeat, and
classified as personal data (`excalidrawplus-workspace-activity`). It belongs to the separate, not recommended
`activity` profile, never to `read`. The log is workspace-wide: on a connection without the `*` target the tool is
refused locally, before any secret is read or request is sent, without naming a target.

- Exclusions of the connection do not apply to the log. Its entries cannot be attributed to a collection or scene and
  may contain the names of excluded scenes; the tool description says so. Leave the `activity` profile out when the
  names must stay hidden.
- Filters, all validated locally: `user_id` (`[A-Za-z0-9_-]`, at most 64), `action` (lowercase identifier of at most
  64 characters, for example `scene:create`), `operation` (`create`, `read`, `update`, or `delete`; any other value is
  refused before a request), and the range `from` and `to` (RFC 3339, each usable alone; when both are given, `from`
  is not after `to` and the range is at most 366 days). They are sent as `user`, `action`, `operation`, `dateFrom`,
  and `dateTo`; nothing else is passed on.
- `has_next_page` comes from `hasMore`, `next_offset` is `offset + limit` (paging as under [Pagination](README.md#pagination)). Each text
  field is capped at 256 characters.
- Entries carry personal data, including the IP address, and are untrusted. `status` is an integer (contract
  version 2). `details` is text or an object: text is capped, an object beyond the limits is left out and marked.
- `user_picture` is not returned: the kind of the address is not documented, so it counts as an access value. The
  API's `cursor`, `page`, and `nextCursor` are not offered; the offset paging applies.
- Assumed: that `hasMore` pairs with `offset`/`limit` paging, that the date filters are inclusive, and the action
  identifier form (the documentation names no closed set of actions).
