---
description: >
  Describes the Matomo channel attribution tools: reading the goals offered for multi-channel attribution, the attribution of a goal, and the campaign dimension combinations, and turning the attribution of a goal on or off, with targets, rights, the plugin requirement, and the limits of what the MultiChannelConversionAttribution reference proves.
type: knowledge
edit: shared
created: 2026-11-03
updated: 2026-11-03
---

# Matomo channel attribution

The channel attribution tools of the [Matomo provider](README.md), for the premium plugin
MultiChannelConversionAttribution. An instance without the plugin refuses both tools as an unsupported capability,
after one plugin check and without further requests. They are in no [setup profile](README.md#setup-profiles). The
channel attribution report is read with `matomo.reports.get` like any report; it has no tool of its own.

## Targets

Both tools are website tools: `idSite` must lie inside the `site` targets, otherwise the call is refused before any
secret is read and any request, without naming the website. Goal IDs count per website.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.channel-attribution.get` | reads one of three things, chosen by `kind` |
| `matomo.channel-attribution.set` | turns the attribution of one goal on or off (idempotent) |

- `kind` `goals` lists the goals the website offers for attribution, in batches. Matomo adds two entries that are
  no goals: `All Goals` (`idGoal` -2, `type` `all-goals`) and the ecommerce orders (`idGoal` 0, `type` `ecommerce`).
  They are kept and marked by `type`. Matomo writes IDs as numbers or text; both are read. Rows of another website
  are dropped.
- `kind` `attribution` reads the attribution of the goal `idGoal`; `limit` and `cursor` do not apply.
- `kind` `dimensions` lists the campaign dimension combinations. They do not depend on a website, but the tool still
  needs a website inside the targets, like the other tools of the group.
- `idGoal` of `attribution` and of `set` must be a goal ID from 1 up; the two pseudo entries are no goals here.
  One read of the goal of the website (`Goals.getGoal`) proves it before the attribution is read or changed. A goal
  the website does not have is refused as not found, and nothing is changed.
- `set` needs `confirm` and is sent once; it is never repeated after an unclear result such as a timeout or an
  unreadable answer. Read the goals with `kind` `goals` before repeating it.

## Limits

- The reads of the goal list and of the combinations are evidenced by Matomo's public demo with the view right.
  The answer of `getGoalAttribution` is not verified against any reference: the tool passes it on with texts
  shortened and lists and depth limited. The right of `setGoalAttribution` (write) is expected, not stated.
- `isEnabled` is sent as `1` or `0`, the convention of Matomo's core API; the reference does not say. The change is
  taken as accepted on the generic success form or a bare `true`; any other answer ends as an unclear result.
- The signatures come from the Matomo 6 reference. They are not verified against Matomo 5 (plugin 5.0.x); no version
  is checked per method, and an instance that lacks one ends as an unsupported capability.
- The plugin source is not public. Which goals Matomo lists for attribution beyond the two pseudo entries is not
  documented.
