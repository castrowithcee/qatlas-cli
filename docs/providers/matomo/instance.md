---
description: >
  Describes the Matomo tools for instance-wide website settings and reference data: global settings, operations (archiving, trusted hosts, branding), reference lists, IP range expansion, diagnostics, and the database statistics.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-26
---

# Matomo instance settings and reference data

The instance tools of the [Matomo provider](README.md). Targets are described under [Scope](README.md#scope).

## Global settings

`matomo.global-settings.get` reads the defaults every website inherits. It needs a connection whose targets name
`instance` and is in no profile. `section` reads one setting with one request; without it all ten sections are read one
after another, which is the bound of the call. Every result member is present only for a section that was read.

Rights are those of the Matomo methods: the excluded IPs, user agents, and referrers, the site search parameters, and
the default currency need an admin right on some website; the query parameter sections and the URL fragment setting
need a view right on some website; the default time zone needs none. A missing right ends as a permission error.
`idSite` filters `excluded-query-parameters` and `query-parameter-exclusion-type` for one website and must lie
inside the connection's targets; for other sections it is refused. Lists are comma-separated untrusted text, cut at 8192
bytes.
Matomo answers `false` for a list that was never set; the tool returns an empty text.

The eight `matomo.global-settings.set-*` tools replace one global default for every website of the instance, including
websites outside the connection's website targets. Each needs Super User rights and a connection whose targets name
`instance`, is in no profile, and sends one request that is never repeated. A list is sent whole and an empty list
clears it, so read the current value first. Matomo trims entries and drops empty ones; Qatlas refuses entries Matomo
would split or misread (commas, control characters) and checks IP entries as addresses, CIDR ranges, or IPv4 with
trailing `*` octets, which is narrower than Matomo's own check.
`-excluded-query-parameters` calls `SitesManager.setGlobalQueryParamExclusion`: the type `custom` takes the list, the
Matomo types take none. A privacy policy of the instance can make Matomo keep the old type while it answers success.
`SitesManager.setGlobalExcludedQueryParameters` is deprecated and not used.
`-search-parameters` sends both the keyword and the category list. `-default-currency` takes three capital letters and
`-default-timezone` takes text without spaces; Matomo checks both against its own lists, which
`matomo.reference-data.list` shows, and a rejected value ends as an error.

## Operations

Four tools change the operation of the instance. Each needs Super User rights and a connection whose targets name
`instance`, is in no profile, and sends one request that is never repeated; after an unclear result check the state
in Matomo before repeating it. None sends `passwordConfirmation`: Matomo asks for it only for a session token.

- `matomo.archive-settings.set` sends both values every time. `todayArchiveTimeToLive` is narrowed to 60 to 86400
  seconds. A value fixed in the configuration file can override the stored one, and an instance without the general
  settings in its interface refuses the change.
- `matomo.trusted-hosts.set` replaces the whole list. Matomo refuses every request from a host outside it, so Qatlas
  refuses before any request a list without the host of the connection's base URL (with its port when the URL names
  one other than 443), compared exactly and not by parent domain, which is narrower than Matomo's own match. Entries
  are host names, IPv4 or bracketed IPv6 addresses with an optional port, at most 50, never empty; they are sent in
  lower case without duplicates. The tool is offered only where the `tools` list of the connection names it.
- `matomo.branding.set` switches only. No file is uploaded, so a logo is published only if an upload already waits in
  Matomo. Turning the branding off deletes the uploaded files.
- `matomo.reports.archive` also needs the website inside the site targets, because Matomo requires Super User rights
  for a direct call. It waits for the archiving, which can exceed the timeout and then leaves the state unknown.
  `period` day, week, month, or year takes one date (`today`, `yesterday`, or `YYYY-MM-DD`); `range` takes two dates of
  at most 366 days apart. `lastN` and `previousN` are refused. `segment` is checked like the report segment; `plugin`
  and `report` narrow the work. Matomo's hidden maintenance methods (scheduled tasks, cron archiving, invalidation)
  are not offered.

## Reference data

`matomo.reference-data.list` takes `kind`: `currencies`, `currency-symbols`, `timezones`, `timezone-name`,
`timezone-support`, `measurable-types`, or `country-codes`. A view right on some website is enough, so it is in the
`read` profile and offered to connections that reach websites. `country-codes` needs the `UserCountry` plugin;
`measurable-types` comes from `API.getAvailableMeasurableTypes`.

List kinds are sorted by group and ID and paged like other lists; Matomo translates the names into the interface
language of the token's user. `timezone-name` needs `timezone` and takes `countryCode` and
`multipleTimezonesInCountry` as Matomo documents them; these arguments are refused for other kinds. The settings of a
measurable type pass on as Matomo describes them and are cut to text above 16384 bytes.

## IP ranges

`matomo.ip-ranges.expand` calls `SitesManager.getIpsForRange` and returns the lowest and highest address of a range.
Matomo checks no right for it, so the tool is offered to connections that reach websites and is in the `read` profile.
Qatlas accepts only an IP address or CIDR notation (IPv4 or IPv6, up to 64 characters) and refuses wildcards, dashes,
and zones before any request. A range Matomo cannot read is an invalid request.

## Diagnostics

All four need a connection that reaches websites, except `matomo.brute-force-ips.unblock`, which needs the target
`instance`. Qatlas never fetches an address from an argument or an answer.

- `matomo.seo.rank` needs a view right on some website. Matomo asks external SEO services about the host, so the
  host leaves the instance. Qatlas sends only the lower-case host of an `http(s)` URL or host name; credentials,
  other schemes, paths without a scheme, and non-ASCII names are refused.
- `matomo.ip-locations.get` is in the `live` profile because address and location can be personal data. The address
  is required, so Matomo never falls back to the address of the request. `provider` is an ID such as `default`.
- `matomo.js-tracker-install.get-status` only reads the result of a test started elsewhere; the start method is not
  offered, because Matomo would fetch a caller URL. `idSite` follows the site targets.
- `matomo.brute-force-ips.unblock` lifts every current block at once, takes no argument, and needs Super User rights.
  It is in no profile, is offered only where the `tools` list of the connection names it, and sends one request that
  is never repeated. Matomo asks for no `passwordConfirmation` here.

## Database statistics

`matomo.database-statistics.get` reads the `DBStats` plugin of the instance. It needs Super User rights and a connection
whose targets name `instance`, is in no profile, and is offered only where the `tools` list of the connection names
it. The eleven Matomo methods are `section` values and one section is one request, because the individual summaries
group every archive table by name and are slow on a large database; `force_cache` recalculates them and applies
only to `individual-reports` and `individual-metrics`. The result is only sizes and counts of tables and report
names, with the status of the database server; it holds no table content. Table sections are cut at `limit`
(1 to 1000, 100 when omitted) and continued with `next_cursor`; `general-information` and `status` take neither.
