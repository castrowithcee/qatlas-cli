---
description: >
  Describes the Matomo tracking tools: recording page views, events, internal searches, downloads or outlinks, goal conversions, e-commerce orders and cart updates, content impressions and interactions, several of these in one bulk request, and requests of AI chatbots through the tracking endpoint, its answers and their limits, the risks of writing raw data, the overrides of address, time, and location that need the token, the counts and partial success of the bulk request, and the instance check and token use of the chatbot tool.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Matomo tracking

The tracking tools of the [Matomo provider](README.md). The profiles are described under
[Setup profiles](README.md#setup-profiles).

## Tools

| Tool | Does |
| --- | --- |
| `matomo.tracking.record-page-view` | records a page view, optionally with page load timings (effect `create`) |
| `matomo.tracking.record-event` | records an event with category, action, name, and value (effect `create`) |
| `matomo.tracking.record-site-search` | records an internal search with keyword, category, and result count (effect `create`) |
| `matomo.tracking.record-link` | records a download or a click on an outgoing link, chosen by `kind` (effect `create`) |
| `matomo.tracking.record-goal` | records a conversion of a goal, optionally with revenue (effect `create`) |
| `matomo.tracking.record-order` | records an e-commerce order with order ID, revenue, amounts, and items (effect `create`) |
| `matomo.tracking.record-cart-update` | records the current content of a cart (effect `create`) |
| `matomo.tracking.record-content-impression` | records an impression of a content block with name, piece, and target (effect `create`) |
| `matomo.tracking.record-content-interaction` | records an interaction such as a click with a content block (effect `create`) |
| `matomo.tracking.record-batch` | records up to 100 entries of one website in one bulk request (effect `create`) |
| `matomo.tracking.record-bot-request` | records a request of an AI chatbot that the server of the website saw (effect `create`) |

All tools are offered only where the `tools` list of a connection names them and are in no profile.

## Endpoint and answer

The tools post once to `matomo.php` below the base URL, not to the reporting API. The parameters go in the form
body, never in the URL. The endpoint needs no token for the context arguments, so a call without an
[override](#overrides-of-address-time-and-location) neither reads the credential of the connection nor sends
`token_auth`; the [chatbot tool](#requests-of-ai-chatbots) is the exception and always reads it for its instance
check. No tracking request sends an `Authorization` header. The connection must still name a credential.

Matomo answers an accepted request with an empty HTTP 204 or an image. That is all the result says:
`sent` is true and `processingConfirmed` is always false.

- An unknown website is refused as HTTP 400 (`invalid-request`; Matomo uses the same status for an invalid
  parameter, for an override without a valid token of at least the Write role, and for a `timestamp` older than the
  raw data the instance keeps), and switched-off tracking as HTTP 503 (`unreachable`). Nothing was recorded in
  both cases.
- Many invalid parameters are answered with 204 all the same, and a queue (QueuedTracking) confirms the request
  before it processes it. A recorded request can be found with `matomo.visits.list` and the live plugin.
- A timeout, a reset connection, an HTTP 5xx, or an answer that is neither 204 nor an image leaves the outcome
  unclear. The request is sent once and never repeated; a repeat may be counted twice.

## Visitor, context, and limits

Without an override Matomo takes the address of the visitor from the sender, so the request comes from the host
that runs Qatlas, and the user agent is `qatlas-cli` unless `userAgent` is given. The tools offer the context of a
visit as arguments: `referrer`, `pageTitle`, `pageViewId`, `visitorId`, `userId`, `newVisit`, `language`,
`screenResolution`, `userAgent`, the local time (`hour`, `minute`, `second`, only together), `campaignName`,
`campaignKeyword`, `visitCount`, `firstVisitTime`, `previousVisitTime`, and `dimensions` for custom dimensions
(see [custom dimensions](dimensions.md)). The page view also takes `performance` timings and `generationTime`.

Addresses (`url`, `referrer`, `target`) must be http or https addresses of at most 2048 characters; they are
passed on as text and never fetched. Texts are limited as the tool schema says. Matomo's own limits apply on top.
The content target is passed on as free text, not checked as an address, and never fetched. The tools offer no media
tracking.

Goals and e-commerce use `matomo.php` with `idgoal`. Matomo resolves a goal ID only within the named website and
refuses an unknown one as invalid, so no token is needed to check it. An order is a request with an order ID, a cart
update one without; Matomo reads the cart items of a request as the full cart and marks items missing from a later
update as removed. Matomo normally refuses an order ID it already holds for the website, but a queue confirms
before it checks.

## Bulk tracking

`matomo.tracking.record-batch` sends entries of one website in one JSON request to `matomo.php`. It needs the
`BulkTracking` plugin of Matomo. The `type` of an entry names one of the nine single tools above, except the
chatbot tool, and the rules of that tool check the entry. An entry sends its parameters as a query string, the form
the plugin documents.

- The bulk takes none of the [overrides](#overrides-of-address-time-and-location): the token would apply to the
  whole request. It reads no secret and sends neither `token_auth` nor an `Authorization` header. A Super User token
  would make Matomo name the invalid entries; without it the result holds counts only. An instance that sets
  `bulk_requests_require_authentication` aborts every such bulk as HTTP 500 and records nothing.
- Matomo answers HTTP 200 with `tracked` and `invalid`. It skips an invalid entry and records the others, so a call
  can succeed in part. `tracked` also counts a visit that Matomo excludes, and an entry that hit a database lock
  timeout is in neither number. Switched-off tracking is answered with 0 and 0, not with HTTP 503. The counts prove
  no processing; a number above the entries sent, or an answer that is not a bulk answer, is an unreadable answer.
- An exception in the middle ends the bulk as HTTP 500 with the counts so far: the entries before it stay recorded,
  unless the instance sets `bulk_requests_use_transaction` and rolls the bulk back. Qatlas reports it as an error with
  the counts. An inactive `BulkTracking` plugin is answered as HTTP 400 (`invalid-request`); nothing was recorded.
- The confirmation covers the number of entries and the website, not which entries Matomo accepts. A repeat counts
  every entry twice, so a partial success is not repeated whole.

## Risks

Tracking writes raw data (visits, actions, conversions) that the API cannot undo; only the
[privacy tools](privacy.md#deleting-visits) remove it, and they may also remove other data. Fabricated requests
distort every report built on them; forged orders, cart updates, and goal revenue distort the e-commerce and goal
reports and cannot be undone. `visitorId`, `overrideVisitorId`, and `userId` merge a request into an existing
visitor or take over its identity. `overrideVisitorId` forces the visitor over the other two and needs the
greatest care. `userId`, `userAgent`, `referrer`, and the texts of events and searches may carry personal data.

## Overrides of address, time, and location

`clientIp`, `timestamp`, `country`, `region`, `city`, `latitude`, and `longitude` replace what Matomo derives
itself. They are shared by all tools. If at least one is set, Qatlas reads the token of the connection and sends
it as `token_auth` in the request body, after the website and the arguments are checked; otherwise it reads no
secret. Matomo accepts the overrides only from a Super User or a user with at least the Write role for the
website, unless the instance sets `tracking_requests_require_authentication=0`; then it accepts them from anyone.
`timestamp` is Unix time in seconds, not in the future and less than 20 years back, because Matomo records a
request with such a time at the time it arrives, silently. Matomo refuses a `timestamp` older than the raw data
the instance keeps (`delete_logs_older_than`) as HTTP 400.

The overrides forge origin, time, and place of a visit on top of the risks above, and `clientIp` is personal
data. The token never goes into the URL, a header, a message, or the result, and `cdo` is never sent.

## Requests of AI chatbots

`matomo.tracking.record-bot-request` reports a request that an AI chatbot made to a website and that Matomo
cannot see through JavaScript. It sends `recMode=1`, so Matomo handles the request as a bot request and never as
a visit; `recMode=2` and `bots` are never sent. It has none of the visitor, user, or location arguments above.

- Matomo stores the request only when the user agent matches a pattern of Matomo's own list of AI chatbots
  (`plugins/BotTracking/BotDetector.php`; in 5.14 for example ChatGPT-User, Claude-User, Perplexity-User,
  MistralAI-User, Gemini-Deep-Research, and Google-NotebookLM). The list changes with the Matomo release. Any other
  user agent is answered like an accepted request and not stored, so a result of `sent` does not mean that a
  chatbot was recognised.
- It stores the website, the time, the name and type of the chatbot, the address, and the given status, bytes,
  response time, and source, but no visitor ID, IP address, or user ID.
- Qatlas refuses values that do not fit Matomo's columns before the request, because Matomo writes such a failed
  record only to its debug log and still reports success.
- The tool needs Matomo 5.7.0 or newer and the activated plugin `BotTracking`. Before the one tracking request
  Qatlas checks both through the reporting API of the instance. That check reads the token of the connection and
  sends it as a bearer header to the reporting API only; the result is held for a few minutes. With an older
  Matomo or an inactive plugin the call ends as `unsupported-capability` and sends no tracking request.
- The tracking request itself carries a token only with `timestamp`: then the token of the check is sent as
  `token_auth` in the body, and without `timestamp` it carries neither `token_auth` nor an `Authorization` header.
  Matomo accepts a time more than 24 hours back only with a token of at least the Write role (otherwise HTTP 400)
  and invalidates the archived reports of that past day.
- Forged bot requests distort the bot reports and cannot be undone through the API; a repeated call counts twice.
