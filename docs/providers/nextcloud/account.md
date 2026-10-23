---
description: >
  Describes Nextcloud account tools and their safety boundaries.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-23
---

# Nextcloud account

Group `account`: account data, status, notifications, activity, and unified search.

The tool group `account` reads the own account data, status, notifications, activity stream, and unified search of
the identity, changes the status, and dismisses notifications. All its tools need an `account` target and refuse
without one locally, before any credential access or request. Provider text is untrusted data, capped and marked
`truncated` when cut. Icons, previews, and rich objects are never reported, nor are links except a search hit's, which
is untrusted text and never fetched; an action of a notification shows only its label and method and is never followed.
`account.status.set` and `account.status.clear` send one request, need `confirm`, and are in no profile.

`account.notifications.list` reports at most 100 notifications; with the `etag` of a previous list an unchanged list is
reported as `unchanged`. `account.activity.list` takes a filter from a fixed list that the instance must also offer,
`limit` up to 100, and `since` from `next_cursor`. `account.notifications.dismiss` (after reading the notification) and
`account.notifications.dismiss-all` need `confirm` and a tools list, are in no profile, and send one request. An unclear
outcome is checked with `account.notifications.list` or `account.status.get` before repeating.

`account.search.providers` lists the search providers of the instance; `account.search.query` asks one of them for one page of up
to 25 hits (`term`, `limit`, `cursor` from the previous `next_cursor`). The provider ID is checked locally and
against the live list, so an unoffered one is refused without a search. Hits are title, subtitle, and link as
untrusted text; a link is never followed. Both are in no profile.
