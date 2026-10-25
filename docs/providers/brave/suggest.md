---
description: >
  Describes the Brave suggestion and spellcheck tools: cost, plans, rich suggestions, image addresses, the omitted is_entity, and empty corrections.
type: knowledge
edit: shared
created: 2026-10-25
updated: 2026-10-25
---

# Brave suggestions and spellcheck

The suggestion and spellcheck tools of the [Brave Search provider](README.md). Configuration, plans, rate
limits, privacy, errors, and the boundary are described there. Both are reads in the
`autosuggest` profile, which is not recommended; the `search` profile does not contain them. Neither has pages
or an offset.

## brave.suggestions.list

- Costs 0.0005 USD per request and belongs to the Autosuggest plan. Whether a key of the Spellcheck plan
  reaches this endpoint is not documented.
- `rich=true` adds entity titles, descriptions, and image addresses and needs a paid Autosuggest subscription;
  without it the call ends with the error class of Brave's status, usually `permission`.
- Image addresses are shown as text when they are plain http or https addresses and are never fetched.
- The deprecated `is_entity` member is omitted; `type` (`query` or `entity`) replaces it. Treat an unknown
  `type` as a plain query suggestion.

## brave.spellcheck.check

- Costs 0.0005 USD per request and belongs to the Spellcheck plan. Whether a key of the Autosuggest plan
  reaches this endpoint is not documented.
- `results` is empty when the phrase needs no correction.
