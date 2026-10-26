---
description: >
  Describes the Matomo tracking tools: recording page views, events, internal searches, downloads or outlinks, goal conversions, e-commerce orders and cart updates, and content impressions and interactions through the tracking endpoint, its answers and their limits, the risks of writing raw data, and the overrides of address, time, and location that need the token.
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

All tools are offered only where the `tools` list of a connection names them and are in no profile.

## Endpoint and answer

The tools post once to `matomo.php` below the base URL, not to the reporting API. The parameters go in the form
body, never in the URL. The endpoint needs no token for the context arguments, so a call without an
[override](#overrides-of-address-time-and-location) neither reads the credential of the connection nor sends
`token_auth`. No call sends an `Authorization` header. The connection must still name a credential.

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
tracking and no bulk tracking.

Goals and e-commerce use `matomo.php` with `idgoal`. Matomo resolves a goal ID only within the named website and
refuses an unknown one as invalid, so no token is needed to check it. An order is a request with an order ID, a cart
update one without; Matomo reads the cart items of a request as the full cart and marks items missing from a later
update as removed. Matomo normally refuses an order ID it already holds for the website, but a queue confirms
before it checks.

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
