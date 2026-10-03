---
description: >
  Describes the Excalidraw+ provider (beta): the workspace API key and fixed API host, the collection targets,
  the collection, scene, and scene content reads with their client-side search, creating, renaming, and moving
  scenes, patching and replacing scene content, deleting scenes and collections into the trash, the bounds, and the
  errors.
type: knowledge
edit: shared
created: 2026-10-02
updated: 2026-10-03
---

# Excalidraw+

This provider reads the collections, scenes, and scene content of one Excalidraw+ workspace through its public
REST API (`https://api.excalidraw.com/api/v1`). The recommended `read` profile changes nothing. The optional `manage`
profile can create a scene, rename or move one, patch its elements, replace its whole content, and move a scene or a
collection to the trash; there is no tool to manage users, invites, or activity logs, or to restore from the trash.

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
| `excalidrawplus.scenes.content` | one scene's elements, capped, with their `version`; optional `query`, `offset`, `limit` |

All four are read-only and safe to repeat. `scenes.list` needs a `collection_id` when the connection allows several
collections; with exactly one allowed collection it is the default, with `*` it is optional.

## Activity log

`excalidrawplus.logs.list` reads the workspace activity log (`GET /logs`). It is read-only, safe to repeat, and
classified as personal data (`excalidrawplus-workspace-activity`). It belongs to the separate, not recommended
`activity` profile, never to `read`.

- The log is workspace-wide, so the tool works only on a connection with the `*` target. On a connection with
  collection targets it is refused locally, before any secret is read or request is sent, without naming a target.
- Filters, all validated locally: `user_id` (`[A-Za-z0-9_-]`, at most 64), `action` (lowercase identifier of at most
  64 characters, for example `scene:create`), and the range `from` and `to` (RFC 3339, given together, `from` not
  after `to`, at most 366 days). They are sent as `user`, `action`, `dateFrom`, and `dateTo`; nothing else is passed on.
- Paging with `offset` (at most 1 000 000) and `limit` (1 to 100, 50 when omitted); `has_next_page` comes from
  `hasMore`, `next_offset` is `offset + limit`. Each text field is capped at 256 characters.
- Returned per entry: `id`, `action`, `operation`, `created_at`, `user_id`, `user_email`, `status`. IP addresses,
  details, user names, pictures, and source fields are not returned. Entries are untrusted data.
- Documented: path, the parameters `limit`, `offset`, `user`, `action`, `dateFrom`, `dateTo`, and the response fields.
  Assumed: that `hasMore` pairs with `offset`/`limit` paging, that the date filters are inclusive, and the action
  identifier form (the documentation names no closed set of actions).

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
- A name has 1 to 250 characters without control characters. Only these fields are sent; pinning and deletion are
  not offered.
- After a timeout, a reset connection, a 5xx answer, or an unreadable answer, the error says the change may have taken
  effect; read the scene before repeating it. Idempotency is reported as `unknown`.

## Editing scene content

The `manage` profile also offers two content tools. Both are in the Excalidraw+ scene content API, which is a public
beta, need `confirm`, send exactly one request, and are never repeated. Both first read the scene's metadata and
continue only when its collection is allowed; a scene of another collection receives no `PATCH` or `PUT`.

| Tool | Request | Does |
| --- | --- | --- |
| `excalidrawplus.content.patch` | `PATCH /scenes/{id}/content` | merges 1 to 100 elements into the scene |
| `excalidrawplus.content.replace` | `PUT /scenes/{id}/content` | replaces the whole content with 1 to 500 elements |

**Patch.** Excalidraw+ merges the elements by id and the higher element version wins. Every element therefore
states `expected_version`, the version it has now (the `version` that `scenes.content` returns, `0` for a new
element), and Qatlas sends it as the next version. Other elements, the app state, and the files stay as they are, and
connected editors are not forced to reload. An element with `is_deleted: true` is deleted (a soft deletion). If the
scene already holds a newer version of an element, the answer does not show the sent version; the element is then
listed in `not_applied` and nothing of it changed, so read the scene and patch again. An empty `not_applied` means
the answer shows every sent element at the sent version. A patch element is a complete element, not a partial one.

**Replace.** This is an authoritative, final replacement: every element not sent is removed, embedded images and
files are removed (`files` is sent empty), and connected editors are forced to reload instead of merging. The tool
requires `confirm` and is offered only to a connection whose `tools` list names it (it has the effect `delete`, so
the connection also needs the `delete` permission); the `manage` profile does not make it available on its own. An
empty element list is refused. `view_background_color` defaults to `#ffffff`.

**Elements.** An element is a closed, structured subset of the Excalidraw element format, never a free body:

| Field | Rule |
| --- | --- |
| `id`, `type` | id: letters, digits, `-`, `_`, at most 64, unique per request; type: `rectangle`, `diamond`, `ellipse`, `frame`, `text`, `line`, `arrow` |
| `x`, `y`, `width`, `height` | required numbers within +/- 10 000 000 (size from 0) |
| `angle`, `stroke_color`, `background_color`, `fill_style`, `stroke_width`, `stroke_style`, `roughness`, `opacity`, `locked` | optional style; colors are `transparent`, `#RGB`, `#RRGGBB`, or `#RRGGBBAA` |
| `group_ids`, `frame_id`, `bound_elements` | optional references: at most 8 group ids, at most 16 bound elements (`arrow` or `text`) |
| `text` | text elements only, required, 1 to 2000 printable characters; with `font_size`, `font_family`, `text_align`, `vertical_align`, `container_id` |
| `name` | frames only, 1 to 250 printable characters |
| `points`, `start_arrowhead`, `end_arrowhead` | lines and arrows only; 2 to 200 `[x, y]` pairs |
| `expected_version`, `is_deleted` | patch only |

Links, images, embedded frames, freehand strokes, custom data, and bindings are not accepted, and no other field
passes. The request body is at most 1 MiB. After a timeout, a reset connection, a 5xx answer, or an unreadable
answer, the error says the change may have taken effect; read the scene before repeating it. Idempotency is reported as
`unknown`. The answer reports `scene_id`, `scene_version`, `sent`, `element_count` (not deleted, as the answer shows),
and, for a patch, `not_applied`.

## Deleting scenes and collections

The `manage` profile also lists two deleting tools. Both have the effect `delete`, need `confirm`, are offered only to
a connection whose `tools` list names them (and with the `delete` permission), send exactly one request, and are never
repeated.

| Tool | Request | Does |
| --- | --- | --- |
| `excalidrawplus.scenes.delete` | `DELETE /scenes/{id}` | moves a scene to the trash: `scene_id` |
| `excalidrawplus.collections.delete` | `DELETE /collections/{id}` | moves a collection to the trash: `collection_id` |

- Excalidraw+ soft-deletes: the scene or collection goes to the trash of Excalidraw+. **Shared links and embeds of the
  scene, or of all scenes in the collection, stop working.** Restoring is only possible inside Excalidraw+ (trash in
  the app), not through Qatlas.
- `scenes.delete` first reads the scene's metadata and continues only when its collection is allowed; a scene of
  another collection receives no `DELETE` and the refusal does not name its collection.
- `collections.delete` checks the `collection_id` against the targets before any secret is read or request is sent,
  then reads the collection once. The `private` collection is never deletable, and the default collection is refused
  (`is_default`); both are narrower than the API requires.
- The answer is `id` and `deleted: true`; the response body of Excalidraw+ is not read. After a timeout, a reset
  connection, a 5xx answer, or an unreadable answer, the error says the change may have taken effect; read the state
  in Excalidraw+ before repeating it. Idempotency is reported as `unknown`.

## Pagination

Lists take `offset` (0 to 1 000 000) and `limit` (1 to 100, 50 when omitted) and answer `has_next_page` and, when
true, `next_offset`. A page is filtered after Excalidraw+ answers, so it can hold fewer items than `limit` while
`has_next_page` is true.

## Scene content and search

Scene content is untrusted data. It is reduced to elements with `id`, `type`, `text`, `name`, position, size,
`version`, `frame_id`, and `container_id`; deleted elements, links, images, and embedded files are not returned (files are
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

The content endpoints follow the API reference pages for scene content and its schema: `PATCH` takes a partial scene
content with `elements`, `appState`, and `files` (at least one), merges elements by id with the higher `version`
winning (a tie is broken by `versionNonce`), deletes through `isDeleted: true`, answers 200 with the merged content,
and does not force editors to reload; `PUT` takes `type`, `version`, `source`, `appState`, `elements`, and `files`,
replaces everything, forces editors to reload, and recomputes `sceneVersion`; both answer 400, 401, 403, or 404 as
`{statusCode, error, message}`. Not documented, and therefore assumed: that a patch element must be complete, that
the server rejects or ignores a stale version without an error (Qatlas detects it from the answer), the defaults
Qatlas fills for omitted element fields, the accepted `source` value, a size limit, and the effect of a repeated
request. The local limits (100 and 500 elements, 2000 text characters, 200 points, 1 MiB) are Qatlas choices.

The delete endpoints follow the API reference, which lists `DELETE` for a scene and for a collection as a soft deletion
into the trash. Not documented, and therefore assumed: the exact paths (`DELETE /scenes/{id}`, `DELETE /collections/{id}`,
in line with the other endpoints), the success status and body (any 2xx counts, the body is dropped), the error
bodies, what happens to the scenes of a deleted collection (assumed to leave with it), whether the default
collection may be deleted (refused locally), the shape of `GET /collections/{id}` (assumed to be the collection object
as in the list, with `id`, `isDeleted`, and `isDefault`), and the effect of a repeated request. No live call has been
made.
