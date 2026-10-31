---
description: >
  Describes the Matomo funnel tools: listing, reading, creating, replacing, and deleting the funnels of a website, testing a URL against funnel steps, listing the pattern types, the binding of a funnel to the connection's targets, confirmation and unclear outcomes, the plugin requirement, and the limits of what the Funnels reference proves.
type: knowledge
edit: shared
created: 2026-10-30
updated: 2026-10-31
---

# Matomo funnels

The funnel tools of the [Matomo provider](README.md). They need the premium plugin Funnels; an instance without it
refuses all of them as an unsupported capability, after one plugin check and without further requests. The tools are
in no [setup profile](README.md#setup-profiles). The figures of a funnel (entries, exits, flow,
conversion rate) are reports; `matomo.reports.get` reads them like any other report of the website.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.funnels.list` | reads the activated funnels of a website with their steps |
| `matomo.funnels.get` | reads one funnel of a website, by funnel ID, by goal, or the sales funnel |
| `matomo.funnels.test-steps` | asks Matomo whether a URL matches the patterns of steps given in the call |
| `matomo.funnel-options.list` | reads the pattern types a step can use |
| `matomo.funnels.set-for-goal` | creates or replaces the funnel of a goal, or the sales funnel (effect `update`) |
| `matomo.funnels.save` | creates a funnel without a goal or replaces one (effect `update`) |
| `matomo.funnels.delete-for-goal` | deletes the funnel of a goal, or the sales funnel (effect `delete`, only via `tools`) |
| `matomo.funnels.delete` | deletes a funnel without a goal (effect `delete`, only via `tools`) |

- `matomo.funnels.list` first asks whether the website has any activated funnel and reads the list only when it
  has. `has_activated_funnel` tells which case it was. A deactivated funnel is not listed.
- `matomo.funnels.get` takes exactly one of `idFunnel` and `idGoal`; `idGoal` 0 is the sales funnel. A website
  without the funnel is not found.
- `matomo.funnels.test-steps` stores nothing and fetches nothing: the value is sent to Matomo as text. The steps are
  sent as `steps[N][pattern_type]`, `steps[N][pattern]`, and, when given, `name` and `required`. Matomo's answer
  is returned as it comes, with strings, lists, and nesting shortened.
- `matomo.funnel-options.list` returns Matomo's grouping of pattern types in the same way.
- `matomo.funnels.set-for-goal` replaces the steps and the activation of the funnel of a goal as a whole; a funnel
  that does not exist yet is created. An activated funnel needs at least one step. `matomo.funnels.save` takes
  `idFunnel` 0 to create a funnel, which is not idempotent: a repeated create makes a second funnel; any other ID
  replaces name, description, and steps. A funnel of a goal is not changed or deleted by the tools for funnels
  without a goal, and the reverse. Both write tools send steps like `matomo.funnels.test-steps`; the position of
  a step is its place in the list. Test patterns first with `matomo.funnels.test-steps`.
- The delete tools remove the funnel definition for good; they are offered only where a connection's `tools` list
  names them, and no setup profile selects them. Whether the reports of a deleted funnel stay readable is not
  verified.
- Every change needs the confirmation of the caller and is sent exactly once. After a result that is unclear (no
  answer in time, a reset connection, an answer Qatlas cannot read) the error says the change may have been
  applied and is never repeated; read the funnel with `matomo.funnels.get` before trying again.

Names, descriptions, and patterns are untrusted provider data. They are shortened, a pattern is returned as text
and never fetched, and a funnel shows at most 100 steps.

## Targets

All tools but `matomo.funnels.test-steps` and `matomo.funnel-options.list` take `idSite`, which must be inside
the targets ([Scope](README.md#scope)), and the change tools check it before any secret is read. A funnel ID is not tied to a website, so the answer must carry the
website it belongs to: the one read that delivers the funnel also proves it, and a funnel of another website, or
an answer without a website, is not shown. In a list, such rows are dropped. The change tools bind their object
with one such read before anything is written: `matomo.funnels.save` for an existing funnel, the two delete tools,
and `matomo.funnels.set-for-goal` through the goal (read with `Goals.getGoal`; the sales funnel is bound by the
website alone). Creating a funnel with `idFunnel` 0 needs no read. A refused object ends as `not-found` and names
no other website. The two tools without `idSite` read no website data; they need a connection that binds at least
one website.

## Limits

- The rights are not checked in advance. A view right on the website is expected for each read and a write right for
  each change; neither is proven by the reference.
- The method signatures come from the Matomo 6 reference. They are not verified against Matomo 5 (Funnels 5.5.3).
- The reference gives no example for the answers of `testUrlMatchesSteps` and `getAvailablePatternMatches`, and
  does not say how `testUrlMatchesSteps`, `setGoalFunnel`, or `saveNonGoalFunnel` expect their steps; the nested step
  form above is an assumption, as are the limits of 100 steps and of 255 characters for name and description.
- The reference describes the answers of the write methods only as IDs and of the delete methods as the generic
  success form. The write tools accept an ID or the success form; the delete tools accept the success form.
- The error form for a missing plugin or a missing licence is not proven. Qatlas recognizes the common Matomo
  messages for a missing method, module, or inactive plugin as an unsupported capability.
