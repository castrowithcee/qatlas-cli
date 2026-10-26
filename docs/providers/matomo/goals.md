---
description: >
  Describes the Matomo goal tools: reading goals, creating, replacing, and deleting them, the match attributes and pattern types, goal recommendations, confirmation, and unclear outcomes.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-26
---

# Matomo goals

The goal tools of the [Matomo provider](README.md). The profiles are described under [Setup profiles](README.md#setup-profiles).
The rights and the plugin are listed in [rights per tool group](setup.md#rights-per-tool-group).

## Tools

| Tool | Does |
| --- | --- |
| `matomo.goals.list` | reads the goals of a website |
| `matomo.goals.get` | reads one goal |
| `matomo.goals.add` | creates a goal (effect `create`) |
| `matomo.goals.update` | replaces the definition of a goal (effect `update`) |
| `matomo.goals.delete` | deletes a goal and its conversions (effect `delete`, only via `tools`) |
| `matomo.goal-recommendations.list` | reads the saved goal recommendations of a website |
| `matomo.goal-recommendations.scan` | lets Matomo crawl the website and save goal recommendations, without AI (effect `execute`) |
| `matomo.goal-recommendations.dismiss` | dismisses one saved recommendation or all of a website (effect `update`) |

Goals are per website; `idGoal` counts per website, and the ecommerce values `ecommerceOrder` and
`ecommerceAbandonedCart` are no goals of these tools. Goal reports are read with the report tools.

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

## Goal recommendations

`matomo.goal-recommendations.list`, `.scan`, and `.dismiss` work on the recommendations that Matomo derives for a
website. All three need the Goal recommendations feature of the instance; an instance without it reports the tools as
unsupported. The list is not in the `read` profile.

- `list` reads the result of the last scan from Matomo's stored options, without the recommendations dismissed one by
  one. Nothing saved gives an empty result without `generatedAt`. It returns every documented member: `mode`, `goals`
  (with the recommendation `id`, the goal definition members, `reason`, `source`, `needsSetup`,
  `implementationNote`, `evidence`, `sourcePages`, and what else Matomo adds), `manualGoals`, `warnings`, `useAi`,
  `generatedAt`, `remainingAiScans`, `providerName`, `aiAvailability`, and `privacyNote`. Texts and addresses are
  untrusted and never fetched; lists and nested values are cut at fixed limits.
- `scan` makes the Matomo server fetch the website's main URL and up to 50 pages of the same site (about 25 seconds
  at most), score links, forms, and downloads, and replace the saved recommendations, which clears the single
  dismissals. It creates no goal. It is not idempotent (each call crawls again), and a scan already running for the
  website or the user is refused. A scan can outlast the 30 second limit of the call; the outcome is then unclear,
  the call is not repeated, and `list` shows whether a result was saved. The result adds `aiError`, which Matomo
  sets when the home page could not be analysed. The development-only `debug` member is not returned.
- Qatlas never sends Matomo's AI option and offers no argument for it, so a scan sends no site data to an AI
  provider; the AI members of the result only describe the instance.
- `dismiss` takes exactly one of `recommendation_id` (a goal `id` from `list`: a match attribute, a colon, a
  lower-case pattern, up to 300 characters) and `all: true`, which deletes all saved recommendations. A single
  dismissal lasts until the next scan; an ID without a saved recommendation changes nothing and returns
  `dismissed: false`. It is idempotent and needs confirmation.

A recommended goal is created with `matomo.goals.add`. The `goals` profile selects all three tools.
