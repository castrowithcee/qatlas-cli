---
description: >
  Describes the Matomo tracking tools: recording page views, events, internal searches, and downloads or outlinks through the tracking endpoint, its answers and their limits, the risks of writing raw data, and the parameters that are never sent.
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

All four tools are offered only where the `tools` list of a connection names them and are in no profile.

## Endpoint and answer

The tools post once to `matomo.php` below the base URL, not to the reporting API. The parameters go in the form
body, never in the URL. The endpoint needs no token for them, so the tools neither read the credential of the
connection nor send `token_auth` or an `Authorization` header. The connection must still name a credential.

Matomo answers an accepted request with an empty HTTP 204 or an image. That is all the result says:
`sent` is true and `processingConfirmed` is always false.

- An unknown website is refused as HTTP 400 (`invalid-request`; Matomo uses the same status for an invalid
  parameter), and switched-off tracking as HTTP 503 (`unreachable`). Nothing was recorded in both cases.
- Many invalid parameters are answered with 204 all the same, and a queue (QueuedTracking) confirms the request
  before it processes it. A recorded request can be found with `matomo.visits.list` and the live plugin.
- A timeout, a reset connection, an HTTP 5xx, or an answer that is neither 204 nor an image leaves the outcome
  unclear. The request is sent once and never repeated; a repeat may be counted twice.

## Visitor, context, and limits

Matomo takes the address of the visitor from the sender, so every request comes from the host that runs Qatlas,
and the user agent is `qatlas-cli` unless `userAgent` is given. The tools offer the context of a visit as
arguments: `referrer`, `pageTitle`, `pageViewId`, `visitorId`, `userId`, `newVisit`, `language`,
`screenResolution`, `userAgent`, the local time (`hour`, `minute`, `second`, only together), `campaignName`,
`campaignKeyword`, `visitCount`, `firstVisitTime`, `previousVisitTime`, and `dimensions` for custom dimensions
(see [custom dimensions](dimensions.md)). The page view also takes `performance` timings and `generationTime`.

Addresses (`url`, `referrer`, `target`) must be http or https addresses of at most 2048 characters; they are
passed on as text and never fetched. Texts are limited as the tool schema says. Matomo's own limits apply on top.
The tools do not offer e-commerce, goal, content, or media tracking and no bulk tracking.

## Risks

Tracking writes raw data (visits, actions, conversions) that the API cannot undo; only the
[privacy tools](privacy.md#deleting-visits) remove it, and they may also remove other data. Fabricated requests
distort every report built on them. `visitorId`, `overrideVisitorId`, and `userId` merge a request into an existing
visitor or take over its identity. `overrideVisitorId` forces the visitor over the other two and needs the
greatest care. `userId`, `userAgent`, `referrer`, and the texts of events and searches may carry personal data.

## Never sent

The tools offer no way to set the visitor's address (`cip`), the request time (`cdt`, `cdo`), the location
(`country`, `region`, `city`, `lat`, `long`), or `token_auth`; the sender refuses a request that carries one.
These parameters override what Matomo derives itself and need a token.
