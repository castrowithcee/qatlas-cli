---
description: >
  Describes the Matomo funnel tools: listing and reading the funnels of a website, testing a URL against funnel steps, listing the pattern types, the binding of a funnel to the connection's targets, the plugin requirement, and the limits of what the Funnels reference proves.
type: knowledge
edit: shared
created: 2026-10-30
updated: 2026-10-30
---

# Matomo funnels

The funnel tools of the [Matomo provider](README.md). They need the premium plugin Funnels; an instance without it
refuses all four as an unsupported capability, after one plugin check and without further requests. The tools are in
no [setup profile](README.md#setup-profiles) and read only. The figures of a funnel (entries, exits, flow,
conversion rate) are reports; `matomo.reports.get` reads them like any other report of the website.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.funnels.list` | reads the activated funnels of a website with their steps |
| `matomo.funnels.get` | reads one funnel of a website, by funnel ID, by goal, or the sales funnel |
| `matomo.funnels.test-steps` | asks Matomo whether a URL matches the patterns of steps given in the call |
| `matomo.funnel-options.list` | reads the pattern types a step can use |

- `matomo.funnels.list` first asks whether the website has any activated funnel and reads the list only when it
  has. `has_activated_funnel` tells which case it was. A deactivated funnel is not listed.
- `matomo.funnels.get` takes exactly one of `idFunnel` and `idGoal`; `idGoal` 0 is the sales funnel. A website
  without the funnel is not found.
- `matomo.funnels.test-steps` stores nothing and fetches nothing: the value is sent to Matomo as text. The steps are
  sent as `steps[N][pattern_type]`, `steps[N][pattern]`, and, when given, `name` and `required`. Matomo's answer
  is returned as it comes, with strings, lists, and nesting shortened.
- `matomo.funnel-options.list` returns Matomo's grouping of pattern types in the same way.

Names, descriptions, and patterns are untrusted provider data. They are shortened, a pattern is returned as text
and never fetched, and a funnel shows at most 100 steps.

## Targets

`matomo.funnels.list` and `matomo.funnels.get` take `idSite`, which must be inside the targets
([Scope](README.md#scope)). A funnel ID is not tied to a website, so the answer must carry the website it belongs
to: the one read that delivers the funnel also proves it, and a funnel of another website, or an answer without
a website, is not shown. In a list, such rows are dropped. `matomo.funnels.test-steps` and
`matomo.funnel-options.list` read no website data and take no `idSite`; they need a connection that binds at least
one website.

## Limits

- The rights are not checked in advance. A view right on the website is expected for each method and is not proven
  by the reference.
- The method signatures come from the Matomo 6 reference. They are not verified against Matomo 5 (Funnels 5.5.3).
- The reference gives no example for the answers of `testUrlMatchesSteps` and `getAvailablePatternMatches`, and
  does not say how `testUrlMatchesSteps` expects its steps; the nested step form above is an assumption.
- The error form for a missing plugin or a missing licence is not proven. Qatlas recognizes the common Matomo
  messages for a missing method, module, or inactive plugin as an unsupported capability.
