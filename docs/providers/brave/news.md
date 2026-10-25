---
description: >
  Describes the Brave news search tool: query limits, extra snippets, offset and cursor paging without an end signal, and fetch metadata.
type: knowledge
edit: shared
created: 2026-10-25
updated: 2026-10-25
---

# Brave news search

The news search tool of the [Brave Search provider](README.md). Configuration, plans, cost, rate limits,
goggles, privacy, errors, and the boundary are described there.

## brave.news.search

`GET /res/v1/news/search`. It returns news articles, up to 50 per page.

- `q` takes at most 400 characters and 50 words. `safesearch` is `strict` when omitted.
- `extra_snippets` is a boolean that asks for up to five further excerpts per article.
- `offset` counts pages of `count` results, from 0 through 9, so at most ten pages are reachable. A
  `cursor` carries the offset and is bound to the query and to every argument that shapes the list,
  `count` included; it cannot be combined with `offset`.
- Brave sends no end signal for news. `has_more` is true while `offset` is below 9 and the page was not
  empty, so the last page may be empty, and consecutive pages may overlap.
- `include_fetch_metadata` adds fetch metadata such as `page_fetched` to the articles.
