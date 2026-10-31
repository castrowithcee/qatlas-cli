---
description: >
  Describes the Matomo heatmap tools: listing and reading heatmap configurations, creating, replacing, and copying them, reading the recorded visitor behavior of a heatmap, listing the heatmap options, the binding of a heatmap and of the destination websites of a copy to the connection's targets, the removal of the token from view addresses, how missing rights affect the options tool, the plugin requirement, and the limits of what the HeatmapSessionRecording reference proves.
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
| `matomo.heatmaps.add` | creates a heatmap (effect `create`) |
| `matomo.heatmaps.update` | replaces the configuration of a heatmap (effect `update`) |
| `matomo.heatmaps.duplicate` | copies a heatmap to other websites (effect `create`) |

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

- `matomo.heatmaps.add` and `matomo.heatmaps.update` take `name`, `matchPageRules`, and every optional setting the
  reference lists. A setting that is not given is not sent, so Matomo's own defaults apply. `matomo.heatmaps.update`
  is a full replacement: `name` and `matchPageRules` are required, and a setting left out may reset a value the
  heatmap had to Matomo's default (by the signature, `sampleLimit` 1000, `sampleRate` 5, `captureDomManually` and
  `autoRepeat` false, the others empty). Nothing is filled in from the current configuration; read it with
  `matomo.heatmaps.get` and pass what is to stay. `description` and `autoRepeat` appear only in the legacy reference.
  `matomo.heatmaps.add` is not idempotent: a repeated call creates a second heatmap; `matomo.heatmaps.update` is.
- `matomo.heatmaps.duplicate` needs `idDestinationSites`, 1 to 20 different websites, and does not default to the
  source website as Matomo does. Each copy is a new heatmap, so a repeated call copies again. The answer carries
  what Matomo returns, bounded.
- Every change needs the confirmation of the caller and is sent exactly once. After a result that is unclear (no
  answer in time, a reset connection, an answer Qatlas cannot read) the error says the change may have been
  applied and is never repeated; read the heatmaps with `matomo.heatmaps.list` or `matomo.heatmaps.get` before
  trying again. No change deletes anything, so none needs the `tools` list.

Names, page rules, selectors, and addresses are untrusted provider data. They are shortened, an address is
returned as text and never fetched, and a heatmap shows at most 100 page rules.

## Tokens in addresses

Matomo puts the token of the caller into `heatmapViewUrl` as the parameter `token_auth`. The tools remove it
before the result leaves the provider: the address is parsed, the parameter is dropped in every spelling (case,
percent-encoding, `;` separator, array suffix), and an address that cannot be parsed or that still names the
parameter or holds the token is left out. The redactor stays a second line. The session replay address of the
session recording tools has the same form and takes the same removal.

## Targets

All tools but `matomo.heatmap-options.list` take `idSite`, which must be inside the targets
([Scope](README.md#scope)) and is checked before any secret is read. A heatmap ID is not tied to a
website, so the answer must carry the website it belongs to: the one read that delivers a heatmap also proves it,
and a heatmap of another website, or an answer without a website, is not shown. In a list, such rows are dropped.
`matomo.recorded-heatmaps.get`, `matomo.heatmaps.update`, and `matomo.heatmaps.duplicate` read the configuration
first as this proof and read or write further only when the heatmap belongs to the website; `matomo.heatmaps.add`
needs no read. Every website in `idDestinationSites` of a copy must be inside the targets too: one outside them
refuses the whole call before any secret is read and any request, and the message names no website. A refused heatmap ends as `not-found` and names no other website.
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
  recorded data, as the public demo shows; the reference does not state it. A write right is expected for the
  change tools, and for a copy on every destination website; the public demo allows no change, so none is proven.
- The method signatures come from the Matomo 6 reference. They are not verified against Matomo 5
  (HeatmapSessionRecording 5.x); the changelog shows no signature change.
- The reference gives no example for the answers of the options that need a write right and of
  `testUrlMatchPages`, nor for the data of a scroll heatmap, and does not show the member `page_treemirror` or the
  array form `matchPageRules[N][member]` of the request; both, and the limits of 20 rules and 100 page rules, are
  assumptions. The two options with an example are device types and heatmap types.
- The reference gives `addHeatmap` an ID as answer and `updateHeatmap` the generic success form; the form of the
  answer of `duplicateHeatmap` is described only in words. The tools take the ID or the success form, and for a copy an
  object that does not report `success` as false. The limits of 255 characters for name and description, 0.1 to
  100 for `sampleRate`, 1 to 99999 for the breakpoints, and 20 destination websites are Qatlas's own.
- The error form for a missing method, module, or inactive plugin is recognized by the common Matomo messages as an
  unsupported capability; the text of an unknown heatmap is the one the public demo gives and ends as `not-found`.
