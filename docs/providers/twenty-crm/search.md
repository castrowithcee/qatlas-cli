---
description: >
  Describes the Twenty CRM full-text search across the reachable objects.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Twenty CRM: search

## Searching across objects

`records.search-all` (`read`) finds records by full-text search over all reachable objects in one call and
returns per hit `object`, `id`, and a capped `label`, plus `omitted`. Qatlas sends one fixed query with the
checked arguments as variables only; there is no free GraphQL. A failed query shows no text of Twenty.

- `objects` limits the search to a subset of the reachable objects; an object outside the connection is
  ignored, and an empty remaining set is refused before the search is sent.
- The searched objects are always named explicitly: the bound objects with targets, otherwise the
  non-system objects of the workspace catalog, each cut with `objects`. Hits of any other object are
  dropped and only counted in `omitted`.
