---
description: >
  Describes the Matomo roll-up tools: listing roll-up websites, creating one, and changing one, the instance and website targets, the rights, the plugin requirement, and the limits of what the RollUpReporting reference proves.
type: knowledge
edit: shared
created: 2026-11-03
updated: 2026-11-03
---

# Matomo roll-ups

The roll-up tools of the [Matomo provider](README.md). A roll-up is a website that reports over source websites.
The tools need the premium plugin RollUpReporting; an instance without it refuses all of them as an unsupported
capability, after one plugin check and without further requests. They are in no
[setup profile](README.md#setup-profiles). The reports of a roll-up are read with `matomo.reports.get` like those
of any website. Deleting a roll-up is not part of the plugin and has no tool.

## Targets

A roll-up belongs to the instance, and its sources are websites. All three tools are offered only to a connection
whose targets name `instance` and reach websites, for example `instance` with `site/1`.

- Every website in `sourceIdSites` and, for a change, the roll-up's own website must lie inside the `site` targets.
  Otherwise the call is refused before any secret is read and any request, without naming the website.
- `matomo.roll-ups.list` returns only the roll-ups whose own website lies inside the targets, and cuts their source
  lists to the websites inside the targets. A roll-up can therefore show fewer sources than it has.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.roll-ups.list` | reads the roll-ups of the instance inside the targets, in batches |
| `matomo.roll-ups.add` | creates a roll-up from a name, source websites, time zone, and currency, all required (not idempotent) |
| `matomo.roll-ups.update` | changes a roll-up; only the given fields are sent |

- `matomo.roll-ups.update` reads the roll-up list once and refuses a website that is no roll-up as not found, then
  sends the change. At least one field besides `idSite` is required.
- `sourceIdSites` replaces the whole source list on a change.
- Each change needs `confirm` and is sent once; it is never repeated after an unclear result such as a timeout or an
  unreadable answer. Read the roll-ups with `matomo.roll-ups.list` before repeating it.

## Limits

- The tools need Super User rights. This is evidenced by Matomo's public demo for `getRollUps` and `updateRollUp`
  and expected for `addRollUp`.
- The method signatures come from the Matomo 6 reference. They are not verified against Matomo 5 (RollUpReporting
  5.0.x); no version is checked per method, and an instance that lacks one ends as an unsupported capability.
- The plugin source is not public and the reference gives no example for any answer. The answers of all three methods
  are unverified. Assumed: `getRollUps` answers rows with `idsite`, `name`, `timezone`, `currency`, and
  `sourceIdSites` (rows without a valid `idsite` end the call as an invalid response), `addRollUp` the ID of the new
  roll-up website, `updateRollUp` the generic success form; any other form of the last two ends as an unclear result.
- What Matomo does with a field of `updateRollUp` that is left out or empty is not documented. The tool never sends
  an empty field and refuses empty names and lists.
