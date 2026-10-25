---
description: >
  Describes the Brave video and image search tools: query limits, safe search, video paging, and image addresses through the Brave image proxy.
type: knowledge
edit: shared
created: 2026-10-25
updated: 2026-10-25
---

# Brave video and image search

The video and image search tools of the [Brave Search provider](README.md). Configuration, plans, cost, rate
limits, goggles, privacy, errors, and the boundary are described there.

## brave.videos.search

`GET /res/v1/videos/search`. It returns videos, up to 50 per page.

- `q` takes at most 400 characters and 50 words. `safesearch` is `moderate` when omitted; there are no
  `goggles` and no `extra_snippets`.
- Paging works as for [News](news.md): `offset` or a bound `cursor`, from 0 through 9, no end signal from Brave, so
  `has_more` is true while `offset` is below 9 and the page was not empty.
- `video` carries `requires_subscription`, `views`, `tags` (at most 20), and `author`. `extra` carries
  `might_be_offensive`.

## brave.images.search

`GET /res/v1/images/search`. It returns up to 200 images in one answer; there is no paging, no `offset`,
no `cursor`, and no `ui_lang`.

- `q` takes at most 400 characters and 50 words. `safesearch` is `off` or `strict`, `strict` when omitted.
  `off` applies no filter except for illegal content and can return adult content.
- `thumbnail.src` and `properties.placeholder` are addresses served by Brave's image proxy, and
  `properties.url` is the original address. All are bounded text; Qatlas fetches no image and returns no
  binary content. `confidence` is passed as Brave names it, and `extra.might_be_offensive` is passed on.
