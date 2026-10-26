---
description: >
  Describes the Matomo live tools: the visit log, live counters, and latest visit time, their personal data classification, paging, and Matomo Cloud limits.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-26
---

# Matomo live data

The live tools of the [Matomo provider](README.md); they are selected by the profile `live`, see [Setup profiles](README.md#setup-profiles).

## Tools

| Tool | Does |
| --- | --- |
| `matomo.visits.list` | reads the visit log of one website |
| `matomo.live-counters.get` | reads visits, actions, visitors, and converted visits of the last minutes |
| `matomo.visits.get-latest-time` | reads the time of the latest visit per website |

The live tools read `Live.getLastVisitsDetails`, `Live.getCounters`, and `Live.getMostRecentVisitsDateTime`. They
need a view right on the website, and `matomo.visits.list` a visits log that is not disabled for it; otherwise
Matomo answers with an error that ends as `unsupported-capability` or `permission`.

**Personal data.** A visit holds the visitor ID, fingerprint, IP address, user ID, and the location of a person.
All three tools carry the data classification `matomo-live-personal`, also the counters, because a `segment` can
single out one visitor. They are not part of the profile `read`; a connection selects the profile `live` or lists
them in `tools`. Addresses, titles, referrers, keywords, and names in a visit are untrusted text: they are
shortened, and an address is never fetched.

**`matomo.visits.list`** returns the visits of one `idSite`, newest first, 25 per batch and at most 100. Paging
uses `filter_offset` and one extra row (`limit` plus one) and an opaque `next_cursor` that is bound to the
website, period, segment, and the other arguments; Matomo's `countVisitorsToFetch` is not used. `period` and `date`
come together or not at all. Actions come only with `include_actions` (otherwise `doNotFetchActions=1` is sent),
up to 50 per visit, with `actions_truncated` marking more. `flat` and `enhanced` are sent only when set;
`flat` does not change the result, which still lists the actions. A row of another website than asked for is
dropped.

A visit and its actions carry every member that the plugins shipped with Matomo 5.14.2 add to the visit log:
Live, CoreHome, Actions, Events, Contents, Goals, Ecommerce, Referrers, UserCountry, DevicesDetection,
DevicePlugins, Resolution, UserLanguage, VisitTime, and VisitorInterest, in snake_case (`device_type`,
`event_category`, `item_details`, and so on; the tool description lists them). Custom dimensions of the visit and
the action scope come as `custom_dimensions`, a map of `dimensionN` to value, up to 30. Lists are bounded (50
actions per visit, 50 items per order, 5 product categories). Members that only present another member are not
returned: icons and icon paths, pretty dates, durations and times, rendered titles and subtitles. The Custom
Variables and Provider plugins are not part of the Matomo core release and their members are not returned.
A member that Matomo does not report, or reports as false, is absent.

**`matomo.live-counters.get`** needs `last_minutes` (1 through 2880) and can narrow the counters with `segment`,
`show_columns`, and `hide_columns`. Matomo rounds the counters when data rounding is enforced.

**`matomo.visits.get-latest-time`** reads one website per request. `idSites` names up to 20 websites, each inside
the targets and refused otherwise; when it is omitted, the connection must bind at most 20 single websites,
which are then all read.

**Cloud limits.** Matomo Cloud limits `method=live` to 200 requests per minute per IP address and runs at most
eight raw-data queries at once per subdomain. Qatlas lets one process run only one live request at a time per
instance and token and keeps 300 ms between them. Another process, another machine, or another token on the
same IP address is not coordinated, so a 429 can still occur and ends as `rate-limited`.
