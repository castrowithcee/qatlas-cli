---
description: >
  Describes the Matomo goal tools: reading goals, creating, replacing, and deleting them, the match attributes and pattern types, confirmation, and unclear outcomes.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-24
---

# Matomo goals

The goal tools of the [Matomo provider](README.md). The profiles are described under [Setup profiles](README.md#setup-profiles).

Goals are per website; `idGoal` counts per website, and the ecommerce values `ecommerceOrder` and
`ecommerceAbandonedCart` are no goals of these tools. Goal reports are read with the report tools. All five tools
need the Goals plugin; the changes need a write right on the website, which Matomo checks.

- `matomo.goals.list` and `matomo.goals.get` return every field Matomo documents for a goal, including the revenue
  and the event value flag. A goal matched `manually` has no pattern, pattern type, or case sensitivity.
  `matomo.goals.list` is cut on the Qatlas side, by ID or, with `orderByName`, by name without regard to case.
- `matomo.goals.add` creates a goal and returns its new ID; it is not idempotent, a repeated call makes a second
  goal. Recorded data does not change; the goal counts conversions from now on.
- `matomo.goals.update` replaces the whole definition: a value that is not given falls back to its default
  (no case sensitivity, no revenue, one conversion per visit, no description). Read the goal first with
  `matomo.goals.get`. Recorded conversions are not reprocessed. Matomo pairs `idSite` and `idGoal` itself and
  refuses an ID the website does not have, so no read comes first.
- `matomo.goals.delete` removes the goal from the website and its reports and deletes every conversion Matomo
  recorded for it, for good. Reports already archived stay stored but are no longer shown. A goal created again
  gets a new ID and no conversions. Matomo answers a delete of an ID the website does not have with success and
  nothing else, so the tool reads the goal first (one request) and ends as `not-found` before any delete.
  Because of the lost conversions the tool is offered only where a connection's `tools` list names it, and no
  setup profile selects it.

`matchAttribute` is one of `url`, `title`, `file`, `external_website`, `manually`, `visit_duration` (minutes),
`event_action`, `event_category`, and `event_name`. `patternType` is `exact`, `contains`, or `regex` for the string
attributes and `greater_than` for `visit_duration`; `manually` takes neither a pattern nor a pattern type.
`useEventValueAsRevenue` is only valid for the event attributes. Qatlas refuses a definition that breaks these
rules before it sends anything; Matomo checks them again and its answer decides.

Every change needs the confirmation of the caller and is sent exactly once. After a result that is unclear (no
answer in time, a reset connection, an answer Qatlas cannot read) the error says the change may have been applied
and is never repeated; read the goal with `matomo.goals.get` before trying again.
