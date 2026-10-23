---
description: >
  Describes Nextcloud CalDAV tools and their safety boundaries.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-23
---

# Nextcloud calendar

Group `calendar`: calendars, events, tasks, and trash.

The CalDAV group `calendar` needs a `calendar` target; an unbound calendar is refused locally, unnamed. Only direct
children of the calendar home and of a bound calendar count, never subscriptions, scheduling, or trash collections.
Profile `calendar` reads events and tasks; `events` adds creating events and replacing one by `etag`. Changes need
`confirm`, deleting and sharing a tools list. Writing tasks, calendar management and sharing, and `calendar.trash.*` are
in no profile. A task is replaced only if Qatlas can write all of it. The trash covers bound calendars only: another
calendar's entry is neither listed nor counted, and refused like a missing one; whole calendars are not restored.
Sharing gives one user or group access to an own calendar, never by link, e-mail, or team; Nextcloud ignores an
unknown recipient, so check `shares` of `calendar.calendars.list`.
`calendar.events.get` has the limits and the `overrides` rule of [infomaniak.dav.md](../infomaniak.dav.md#events).
