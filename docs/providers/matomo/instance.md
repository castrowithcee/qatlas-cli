---
description: >
  Describes the Matomo tools for instance-wide website settings and reference data: global settings, reference lists, and IP range expansion.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-24
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
Matomo answers `false` for a list that was never set; the tool returns an empty text. Writing global settings is not
part of this group, and neither is `SitesManager.setGlobalExcludedQueryParameters`.

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
