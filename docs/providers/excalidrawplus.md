---
description: >
  Describes the Excalidraw+ provider (beta): the workspace API key and fixed API host, the collection targets,
  the collection, scene, and scene content reads with their client-side search, creating, renaming, and moving
  scenes, the bounds, and the errors.
type: knowledge
edit: shared
created: 2026-10-02
updated: 2026-10-02
---

# Excalidraw+

This provider reads the collections, scenes, and scene content of one Excalidraw+ workspace through its public
REST API (`https://api.excalidraw.com/api/v1`). The recommended `read` profile changes nothing. The optional `manage`
profile can create a scene and rename or move one; there is no tool to change a scene's content, to delete anything, or
to manage collections, users, invites, or activity logs.

**Beta.** The Excalidraw+ API is a public beta whose names and schemas may change. Every tool descriptor carries a
`version` that increases with a breaking change.

## Credential and base URL

The credential provides `api-key`: a workspace API key from the workspace settings. A key belongs to one workspace and
carries the right `read`, `full`, or routes, and an expiry date; reading needs `read` or the routes for collections and
scenes. It is sent as `Authorization: Bearer ...` and registered with the redactor. Qatlas does not narrow the key;
it narrows a connection through its targets, its `tools` list, and its `permissions`.

`base_url` is `https://api.excalidraw.com`, the only accepted value: any other host, `http`, a port, path, user info,
query, or fragment is refused before a secret is read. Redirects are never followed.

```yaml
services:
  excalidraw-customer-a:
    provider: excalidrawplus

credentials:
  excalidraw-customer-a-reader:
    provider: excalidrawplus
    type: keyring

connections:
  excalidraw-customer-a:
    service: excalidraw-customer-a
    credential: excalidraw-customer-a-reader
    target: collection/COLLECTION_ID
```

## Scope

| Target | Allows |
| --- | --- |
| `collection/COLLECTION_ID` | the scenes of this collection; repeatable |
| `*` | every collection the key reads; must be the only target |

A collection identifier is limited to ASCII letters, digits, hyphen, and underscore, at most 64 characters.

- A `collection_id` outside the targets is refused before any secret is read or request is sent, without naming the
  collection.
- A scene is bound through its own collection: `scenes.get` and `scenes.content` first read the scene's metadata and
  continue only when the collection it reports is allowed. A scene of another collection, a scene without a
  collection, and a scene in the trash are refused (the last two are only reachable with `*` or never, respectively),
  and the content is not requested. The refusal never names the real collection.
- Lists drop collections and scenes outside the targets and in the trash.

## Tools

| Tool | Reads |
| --- | --- |
| `excalidrawplus.collections.list` | the allowed collections: `id`, `name`, `is_default`, `created`, `updated` |
| `excalidrawplus.scenes.list` | scenes of the allowed collections; optional `collection_id` and `name` |
| `excalidrawplus.scenes.get` | one scene's metadata, without creator, preview, or share links |
| `excalidrawplus.scenes.content` | one scene's elements, capped; optional `query`, `offset`, `limit` |

All four are read-only and safe to repeat. `scenes.list` needs a `collection_id` when the connection allows several
collections; with exactly one allowed collection it is the default, with `*` it is optional.

## Creating, renaming, and moving scenes

The `manage` profile (not recommended) adds two tools that change data. Each needs `confirm`, sends exactly one
request, and is never repeated.

| Tool | Does |
| --- | --- |
| `excalidrawplus.scenes.create` | creates an empty, unpinned scene: `name` and `collection_id` |
| `excalidrawplus.scenes.update` | renames and/or moves a scene: `scene_id` and `name` and/or `collection_id` |

- The target collection of `create` and of a move must be allowed by the targets; it is checked before any secret is
  read or request is sent. The `private` collection is never offered.
- `create` without `collection_id` uses the only allowed collection; with several allowed collections or `*` it is
  required, so a scene is never created outside the allowed collections.
- `update` first reads the scene's metadata and continues only when its current collection is allowed; a scene of
  another collection receives no change request.
- A name has 1 to 250 characters without control characters. Only these fields are sent; pinning, content, and
  deletion are not offered.
- After a timeout, a reset connection, a 5xx answer, or an unreadable answer, the error says the change may have taken
  effect; read the scene before repeating it. Idempotency is reported as `unknown`.

## Pagination

Lists take `offset` (0 to 1 000 000) and `limit` (1 to 100, 50 when omitted) and answer `has_next_page` and, when
true, `next_offset`. A page is filtered after Excalidraw+ answers, so it can hold fewer items than `limit` while
`has_next_page` is true.

## Scene content and search

Scene content is untrusted data. It is reduced to elements with `id`, `type`, `text`, `name`, position, size,
`frame_id`, and `container_id`; deleted elements, links, images, and embedded files are not returned (files are
only counted in `files_count`). One answer holds at most 1000 elements (200 when `limit` is omitted), 1 KiB per text
or name, and 256 KiB of text in total; `truncated` and `next_offset` say where to continue. A content body above
16 MiB is refused.

Excalidraw+ has no content search. `query` is searched by Qatlas: an element is kept when its text or name contains the
query, ignoring case; `matched` counts all matches. `scenes.list` has only a `name` filter, applied to the returned
page.

## Rate limit and errors

Excalidraw+ allows 600 requests per minute and IP; Qatlas spaces requests at least 100 ms apart. After a 429 it holds
until `X-RateLimit-Reset`, for at most 60 seconds.

| Class | Cause |
| --- | --- |
| `auth` | the key was rejected (401) or is unusable |
| `permission` | the key may not read this (403): it needs `read` or the matching routes and must not be expired |
| `not-found` | Excalidraw+ does not hold the scene or does not show it to this key |
| `rate-limited` | Excalidraw+ answered 429 |
| `timeout`, `unreachable` | no answer in time, or the service is unavailable |
| `invalid-provider-response` | unreadable, too large, or a different scene than requested |
| `provider-error` | every other rejection, including a redirect |

A `collection_id` or scene outside the targets is an invalid request, not a provider error. Errors never carry the key
or a provider response body.

## Boundary

The response shapes follow the API reference's pages for collections, scenes, scene content, and rate limiting; error
bodies, the identifier format, and the element fields beyond the Excalidraw file format are not documented there, and
the element fields (`text`, `name`, `isDeleted`, `frameId`, `containerId`) are assumed from that format. The behaviour
of `collectionId=private` for personal keys is not relied on. The request bodies of `POST /scenes` (`name`,
`pinned`, `collectionId`) and `PATCH /scenes/{sceneId}` (`name`, `pinned`, `collectionId`, all optional) and their
answers follow the API reference; the effect of a repeated request and the error bodies are not documented, and no
live call has been made.
