---
description: >
  Describes the Brave LLM Context tool: page content extracted for a model, its budgets, location, local recall, snippets, and sources.
type: knowledge
edit: shared
created: 2026-10-25
updated: 2026-10-25
---

# Brave LLM Context

The LLM Context tool of the [Brave Search provider](README.md). Configuration, plans, cost, rate limits,
goggles, privacy, errors, and the boundary are described there.

## brave.llm-context.get

`GET /res/v1/llm/context`. It returns page content extracted for a model, and Brave recommends it when an
agent, not a person, reads the result. It has no pages; the size of the answer is set by the budgets
`maximum_number_of_urls`, `_tokens`, `_snippets`, `_tokens_per_url`, and `_snippets_per_url`.

- `q` takes at most 600 characters and 75 words. The enumerations of `country` and `search_lang` match
  those of News. There is no `ui_lang`.
- The location arguments (`latitude` with `longitude`, `city`, `state`, `state_name`, `country_code`,
  `postal_code`) become the `X-Loc-*` headers. With a location, Brave turns on local recall, which adds
  `grounding.poi` and `grounding.map`; `enable_local` forces it on or off. Brave documents `enable_local` as
  text without values; Qatlas offers a boolean.
- `safesearch` sends nothing when omitted, and Brave then filters nothing except in local recall.
- A snippet is plain text or a JSON-serialized table, schema, or code block. It is cut at 8000 characters,
  at most 100 snippets per page or place, 50 pages or places per group, and 100 sources.
- `sources` is keyed by page address and carries `title`, `hostname`, and `age`, a list of four texts: full
  date, `YYYY-MM-DD`, relative age, and ISO 8601 timestamp, or empty when the page has no known date.
  With `enable_source_metadata` a source also has `description`, `fetched_content_timestamp`, `site_name`,
  `favicon`, and `thumbnail`. Brave does not document the form of `thumbnail`; Qatlas passes an address or
  an object with `src` and `original`, and drops anything else. A source whose key is not a safe address
  is dropped.
