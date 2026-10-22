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

`records.search-all` (`read`) finds records by full-text search over all reachable objects in one call.
Qatlas sends one fixed query with the checked arguments as variables only; there is no free GraphQL. A failed
query shows no text of Twenty.

- `image_url` leaves as an address and is never fetched. Twenty signs image addresses of stored files with a
  token that grants access without sign-in, so an address with a query or fragment counts as an access value
  and is dropped.
- `objects` limits the search to a subset of the reachable objects; an object outside the connection is
  ignored, and an empty remaining set is refused before the search is sent.
- The searched objects are always named explicitly: the bound objects with targets, otherwise the
  non-system objects of the workspace catalog, each cut with `objects`. Hits of any other object are
  dropped and only counted in `omitted`.
