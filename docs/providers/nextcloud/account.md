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
without one locally, before any credential access or request. Provider text and addresses are untrusted data, capped
and marked `truncated` when cut; addresses are never fetched. A rich object member that holds a token, or a link to a
public share or to a conversation by token, is an access value and is never reported; the same holds for links, icons,
and previews. A notification action is reported with its address and is never followed or performed. The storage
location is a server file system path and is never reported, for security reasons. `display-name` repeats
`displayname` and is reported once as `display_name`; both rules also hold for `admin.users.get`.
`account.status.set` and `account.status.clear` send one request, need `confirm`, and are in no profile.

`account.notifications.list` reports at most 100 notifications; with the `etag` of a previous list an unchanged list is
reported as `unchanged`. `account.activity.list` takes a `filter` that the instance must offer in its filter list,
`limit` up to 100, and `since` from `next_cursor`. `object_type` and `object_id` go together, never with `filter`, and
read the fixed object path without the filter list; `search` matches file paths only. `account.notifications.dismiss` (after reading the notification) and
`account.notifications.dismiss-all` need `confirm` and a tools list, are in no profile, and send one request. An unclear
outcome is checked with `account.notifications.list` or `account.status.get` before repeating.

`account.search.providers` lists the search providers of the instance; `account.search.query` asks one of them for one page of up
to 25 hits (`term`, `limit`, `cursor` from the previous `next_cursor`). The provider ID is checked locally and
against the live list, so an unoffered one is refused without a search. A search filter is sent only when the chosen
provider declares it; any other is refused after the provider read, without a search. Hit links are never followed.
With filters set, `next_cursor` of both lists carries them, and a following page with other filters is refused; a cursor
from a list without filters stays valid as long as no filter is set. Both are in no profile.
