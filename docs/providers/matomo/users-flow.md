---
description: >
  Describes the Matomo users flow tools: listing the data sources of the users flow and the actions of one interaction step, with targets, rights, the plugin requirement, and the limits of what the UsersFlow reference proves.
type: knowledge
edit: shared
created: 2026-11-03
updated: 2026-11-03
---

# Matomo users flow

The users flow tools of the [Matomo provider](README.md), for the premium plugin UsersFlow. An instance without the
plugin refuses both tools as an unsupported capability, after one plugin check and without further requests. They
are in no [setup profile](README.md#setup-profiles). The users flow reports `getUsersFlow` and `getUsersFlowPretty`
are read with `matomo.reports.get` like any report ([report catalog](reports.md#report-catalog)); they have no tool
of their own.

## Targets

Both tools are website tools: `idSite` must lie inside the `site` targets, otherwise the call is refused before any
secret is read and any request, without naming the website.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.users-flow.get-data-sources` | lists the data sources of the users flow, such as `page_url` and `page_title` |
| `matomo.users-flow.list-interaction-actions` | lists the actions of one interaction step for one period |

- The data sources do not depend on a website, and the request carries no `idSite`; the tool still needs a website
  inside the targets, so a connection that binds no website does not offer it.
- `list-interaction-actions` reads one table: one date, or with `period` `range` a range of dates that is summed.
  `lastN`, `previousN`, and several periods are refused. `interactionPosition` is the step,
  `idSubtable` an action of the previous step whose followers are read, `dataSource` a value of
  `get-data-sources`. At most 200 rows are returned, with `has_more` when Matomo answered more. Texts are shortened
  and nesting is limited.
- Page titles, URLs, and labels in the rows are visitor data: untrusted, never fetched.

## Limits

- The data source list is evidenced by Matomo's public demo with the view right. The form of the answer of
  `getInteractionActions` is assumed to be Matomo's data table convention and is not verified against a reference;
  its right (view) is expected, not stated.
- The signatures come from the Matomo 6 reference. They are not verified against Matomo 5 (plugin 5.x); no version
  is checked per method, and an instance that lacks one ends as an unsupported capability.
- The plugin source is not public.
