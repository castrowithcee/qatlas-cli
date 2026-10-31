---
description: >
  Describes the Matomo heatmap tools: listing and reading heatmap configurations, reading the recorded visitor behavior of a heatmap, listing the heatmap options, the binding of a heatmap to the connection's targets, the removal of the token from view addresses, how missing rights affect the options tool, the plugin requirement, and the limits of what the HeatmapSessionRecording reference proves.
type: knowledge
edit: shared
created: 2026-10-31
updated: 2026-10-31
---

# Matomo heatmaps

The heatmap tools of the [Matomo provider](README.md). They need the premium plugin HeatmapSessionRecording; an
instance without it refuses all of them as an unsupported capability, after one plugin check and without further
requests. The tools are in no [setup profile](README.md#setup-profiles). Reports of the plugin are read with
`matomo.reports.get` like any other report. Session recordings are not part of these tools.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.heatmaps.list` | reads the heatmap configurations of a website, in batches |
| `matomo.heatmaps.get` | reads one heatmap configuration of a website |
| `matomo.recorded-heatmaps.get` | reads what visitors did in one heatmap in one period |
| `matomo.heatmap-options.list` | reads the values heatmaps are configured and read with, and tests a URL against page rules |

- `matomo.heatmaps.list` asks Matomo for the recorded page copy (`page_treemirror`) only when
  `includePageTreeMirror` is true, because it is large; Matomo's own default would include it. The copy is untrusted
  markup and is shortened.
- `matomo.recorded-heatmaps.get` takes `heatmapType` and `deviceType` as numbers; `matomo.heatmap-options.list` gives
  them. One call reads the metadata (sample counts, average fold per device) and the activities of that type on
  that device, for one period, optionally for a segment. The activities come in batches. The period is a single
  day, week, month, or year, or `range` with a range of dates. What the activities hold depends on the type; for
  click and move they are selector, offsets, and a count, and other types are returned as Matomo gives them,
  shortened. This is visitor behavior, so the tool carries its own data sensitivity.
- `matomo.heatmap-options.list` reads device types and heatmap types and, when the token may, the statuses, the
  building blocks of page rules, and the session recording sample limits. With `url` it also asks Matomo whether the
  URL matches the given `matchPageRules`; without `url` that test is not asked. Nothing is stored and the URL is
  never fetched.

Names, page rules, selectors, and addresses are untrusted provider data. They are shortened, an address is
returned as text and never fetched, and a heatmap shows at most 100 page rules.

## Tokens in addresses

Matomo puts the token of the caller into `heatmapViewUrl` as the parameter `token_auth`. The tools remove it
before the result leaves the provider: the address is parsed, the parameter is dropped in every spelling (case,
percent-encoding, `;` separator, array suffix), and an address that cannot be parsed or that still names the
parameter or holds the token is left out. The redactor stays a second line. The session replay address of the
session recording tools has the same form and takes the same removal.

## Targets

`matomo.heatmaps.list`, `matomo.heatmaps.get`, and `matomo.recorded-heatmaps.get` take `idSite`, which must be inside
the targets ([Scope](README.md#scope)) and is checked before any secret is read. A heatmap ID is not tied to a
website, so the answer must carry the website it belongs to: the one read that delivers a heatmap also proves it,
and a heatmap of another website, or an answer without a website, is not shown. In a list, such rows are dropped.
`matomo.recorded-heatmaps.get` reads the configuration first as this proof and reads metadata and activities only
when the heatmap belongs to the website. A refused heatmap ends as `not-found` and names no other website.
`matomo.heatmap-options.list` reads no website data; it needs a connection that binds at least one website.

## Rights of the options tool

Matomo asks a view right on any website for device types and heatmap types, and a write right on at least one
website for statuses, page rule building blocks, sample limits, and the URL test. The rights are not checked in
advance. When Matomo refuses an option for the right of the token, the option is left out of the result and
named in `unavailable` with the reason `permission`; the result is never silently shortened. When Matomo refuses
every asked option, the call fails with a permission error. Any other failure of one option (unsupported plugin
or method, rate limit, timeout, an unreadable answer) ends the whole call. A write right on a website outside the
connection's targets also satisfies Matomo, and the options reveal nothing of that website.

## Limits

- The rights are not checked in advance. A view right on the website is expected for the configuration and the
  recorded data, as the public demo shows; the reference does not state it.
- The method signatures come from the Matomo 6 reference. They are not verified against Matomo 5
  (HeatmapSessionRecording 5.x); the changelog shows no signature change.
- The reference gives no example for the answers of the options that need a write right and of
  `testUrlMatchPages`, nor for the data of a scroll heatmap, and does not show the member `page_treemirror` or the
  array form `matchPageRules[N][member]` of the request; both, and the limits of 20 rules and 100 page rules, are
  assumptions. The two options with an example are device types and heatmap types.
- The error form for a missing method, module, or inactive plugin is recognized by the common Matomo messages as an
  unsupported capability; the text of an unknown heatmap is the one the public demo gives and ends as `not-found`.
