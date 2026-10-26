---
description: >
  Describes the Matomo visitor tools: the visitor profile, the ID of the latest visitor, and the first visit of a visitor, with the personal data they carry, their caps, and the visitor profile setting they need.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-26
---

# Matomo visitors

The visitor tools of the [Matomo provider](README.md). They belong to the live data of a website and share its
rules: the profile `live` selects them, they pass the same gate as the visit log (one live request at a time
per instance and token, spaced below the Cloud limit), and visits come back in the shape of
`matomo.visits.list`. They carry the data classification `matomo-live-personal`.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.visitors.get-profile` | reads the profile of one visitor, bounded; needs visitor profiles enabled |
| `matomo.visitors.get-latest-id` | reads the visitor ID of the latest visit, optionally within a segment |
| `matomo.visits.get-first` | reads the first visit of one visitor without actions; needs visitor profiles enabled |

## Personal data

Visitor IDs, user IDs, IP addresses, locations, referrers, page addresses, and search keywords identify or
describe a person. They are untrusted text, shortened, and addresses are never fetched. Rows of another website
than the one asked for are dropped.

## Visitor profiles

`matomo.visitors.get-profile` and `matomo.visits.get-first` need visitor profiles to be enabled on the website
(`Live.isVisitorProfileEnabled`, checked first). A disabled profile ends the call as an unsupported capability
without a further request. `matomo.visitors.get-latest-id` does not need it.

`visitor_id` is 16 hexadecimal characters. `matomo.visitors.get-profile` takes it optionally; without it Matomo
profiles the most recent visitor. An unknown visitor answers `found: false`.

## Profile

`matomo.visitors.get-profile` returns every field Matomo's plugins add to a profile, in snake_case: totals and
averages, first and last visit summary, goal conversions and revenue per goal, ecommerce totals (only when
ecommerce is enabled), searches, visited pages, devices, continents, countries with cities, custom dimensions
by scope, adjacent visitor IDs, and `last_visits`. Formatted duplicates (pretty dates and durations, icons,
flags) are left out.

The profile is large, so lists are capped and a cut sets a `*_truncated` flag: 100 rows for pages, searches,
goals, and countries; 30 for devices, continents, and custom dimensions; 20 values per entry. `limit_visits`
(1 through 20, 10 by default) bounds `last_visits`; each visit carries up to 50 actions. The totals aggregate
every visit Matomo reads for the profile, whatever the cap on `last_visits`. `segment` filters the profile and
its adjacent visitors.

## Latest visitor and first visit

`matomo.visitors.get-latest-id` returns the visitor ID of the most recent visit, optionally within a `segment`.
Matomo searches the last 7 days, then one year, then all logs, which can be slow on a large log.

`matomo.visits.get-first` returns the first visit of a visitor without its actions.
