---
description: >
  Describes the Brave Answers tool: listed only, the price formula, streaming within one call, no partial answer and no retry, the research limit, entities, citations, rate limit, and arguments.
type: knowledge
edit: shared
created: 2026-10-25
updated: 2026-10-25
---

# Brave Answers

The Answers tool of the [Brave Search provider](README.md). Configuration, plans, rate limits, privacy,
errors, and the boundary are described there.

## brave.answers.ask

`POST /res/v1/chat/completions`, Brave's OpenAI-compatible endpoint. It sends one question and returns the
answer Brave writes from a live web search, with citations, token usage, and the cost Brave reports. The tool
needs the Answers plan.

- **Listed only.** The cost varies with every answer, so the tool is never offered by the `read` effect of a
  connection, only by its `tools` list. The profile `answers` holds only this tool; it is not recommended and
  not part of `search`. The connection test never calls Answers, so a connection that lists only this tool
  has no endpoint to test.
- **Cost.** Brave bills 4 USD per 1000 searches plus 5 USD per million input and output tokens: `cost = searches
  × 0.004 + (input tokens + output tokens) × 0.000005` USD. A normal answer runs one search, research mode up to
  50. `cost` in the result is what Brave reports for the answer (`requests`, `queries`, `tokens_in`,
  `tokens_out`, their costs in USD, and `total_cost`); it is missing when Brave sent no usage. A spending limit
  set in the Brave dashboard is checked before the answer starts; a started answer runs to its end.
- **Streaming within one call.** The tool always asks for the stream, because citations, entities, and
  research exist only there, and reads it to the closing `data: [DONE]` before it returns one result: the
  text fragments joined as `answer`, the `<citation>` tags as `citations`, and the `<usage>` tag as `cost`. A
  tag may arrive split over several fragments. A tag whose content is not the documented JSON object stays in
  the text.
- **No partial answer, no retry.** A stream that ends without `[DONE]`, breaks off, or exceeds 8 MiB ends with
  `invalid-provider-response`; an error event in the stream with `provider-error`. The whole request, the
  rate-limit wait included, ends after 55 seconds with `timeout`, five seconds inside the 60 seconds of an
  invoke. Each of these messages says that Brave may have billed the request. Qatlas sends exactly one request
  and never repeats it, because a repetition is billed again.
- **Research.** `enable_research` lets Brave search several times. Brave allows a research budget of up to 300
  seconds; here `research_maximum_number_of_seconds` takes at most 50 and is 40 when omitted, which leaves time
  for the answer Brave writes after its research. Longer research does not run through Qatlas. The other
  `research_maximum_*` arguments keep Brave's ranges and need `enable_research`. Brave gives no citations or
  entities in research mode, so `enable_citations` or `enable_entities` together with `enable_research` is
  refused before the key is read. Research answers carry further tags such as `<thinking>`, `<progress>`, and
  `<answer>`, which stay in the text.
- **Entities.** Brave does not document the tags `enable_entities` adds; they stay in `answer` unchanged.
- **Citations.** `start_index` and `end_index` are passed as Brave sends them; Brave does not document whether
  they count in the text with or without the tags. `url` and `favicon` follow the address rule of all tools, see [Privacy and terms of use](README.md#privacy-and-terms-of-use).
- **Rate limit.** The Answers plan allows two requests per second. Besides the spacing the answer headers
  imply, the next request of the same key waits at least 500 ms after an answer request is sent.
- **Arguments.** `question` becomes the one `user` message Brave accepts; there is no conversation. `model` is
  `brave-pro` when omitted, as the reference says. Brave documents `country` (default `us`) and `language`
  (default `en`) without a list of values, so Qatlas checks only their form: a two-letter code or `ALL`, and a
  language code such as `en` or `pt-br`. The location becomes
  `web_search_options.user_location.approximate`.
- **Not offered.** `metadata`, `seed`, and `research_allow_thinking`, which Brave accepts and ignores; `stream`,
  which is always true; more than one message and roles other than `user`. The blocking answer without stream,
  whose cost Brave sends as `X-Request-*` headers, is not used.
