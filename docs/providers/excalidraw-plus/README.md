---
description: >
  Describes the Excalidraw+ provider (beta): the workspace API key and fixed API host, the collection and scene
  targets, choosing them, pagination, the rate limit, the errors, and the boundary; the tool groups are on their own
  pages.
type: knowledge
edit: shared
created: 2026-10-02
updated: 2026-10-28
---

# Excalidraw+

This provider reads the collections, scenes, and scene content of one Excalidraw+ workspace through its public
REST API (`https://api.excalidraw.com/api/v1`). The recommended `read` profile changes nothing. The optional `manage`
profile can create a scene, rename or move one, patch its elements, replace its whole content, and move a scene or a
collection to the trash. The optional `people` profile reads and manages workspace users, and the optional `activity`
profile reads the workspace activity log. There is no tool for team management, workspace settings, or restoring from
the trash.

**Beta.** The Excalidraw+ API is a public beta whose names and schemas may change. Every tool descriptor carries a
`version` that increases with a breaking change.

[Set up Excalidraw+](setup.md) leads through creating the API key, the rights it needs, and fixing errors.

## Credential and base URL

The credential provides `api-key`: a workspace API key from the workspace settings. A key belongs to one workspace.
It is sent as `Authorization: Bearer ...` and registered with the redactor. Qatlas does not narrow the key; it narrows
a connection through its targets, its `tools` list or `effects`.

`base_url` is `https://api.excalidraw.com`, the only accepted value: any other host, `http`, a port, path, user info,
query, or fragment is refused before a secret is read. Redirects are never followed.

```yaml
services:
  excalidraw-customer-a:
    provider: excalidraw-plus

credentials:
  excalidraw-customer-a-reader:
    provider: excalidraw-plus
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
| `scene/SCENE_ID` | this single scene, optionally narrowing the connection; repeatable; it does not open its collection |
| `*` | every collection the key reads; must be the only target |

Collection and scene targets together form one allow-list: a scene is reachable when it is listed or its collection
is allowed. A connection without a scene target behaves as a pure collection binding. An identifier is limited to
ASCII letters, digits, hyphen, and underscore, at most 64 characters.

- A `collection_id` outside the targets is refused before any secret is read or request is sent, without naming the
  collection.
- A scene is bound through its own metadata: `scenes.get` and `scenes.content` first read it and continue only when
  the scene is listed or the collection it reports is allowed. A scene of another collection, a scene without a
  collection (only reachable with `*` or when listed), and a scene in the trash are refused, and the content is not
  requested. The refusal never names the real collection or the scene.
- With scene targets only, a scene that is not listed is refused locally without a request.
- Lists drop collections and scenes outside the targets and in the trash. `scenes.list` lists the scenes of allowed
  collections; a listed scene of another collection is not listed.
- A connection bound to scenes only does not reach the tools on collections and the workspace: `collections.list`,
  `scenes.list`, `scenes.create`, `collections.delete`, the activity log, and the user tools. They are not offered
  and refused at the call; moving a scene needs an allowed target collection.

## Choosing targets

The picker offers two levels: `collection`, and `scene` below a collection. It lists what the key reaches, not only
the current targets, and works on a draft connection without targets. Both levels are paged by the API's offset;
the API has no name search, so the picker filters the pages it has loaded. Names are untrusted data. Excalidraw+
documents no web interface URL with collection or scene identifiers, so no URL is resolved; choose from the lists
or enter the identifier.
## Tool groups

| Page | Covers |
| --- | --- |
| [reads.md](reads.md) | the collection, scene, and scene content reads and the client-side search |
| [activity-log.md](activity-log.md) | the workspace activity log |
| [scenes.md](scenes.md) | creating, renaming, and moving scenes |
| [content.md](content.md) | patching and replacing scene content and the element format |
| [deletion.md](deletion.md) | moving scenes and collections to the trash |
| [users.md](users.md) | workspace users |

## Pagination

Lists take `offset` (0 to 1 000 000) and `limit` (1 to 100, 50 when omitted) and answer `has_next_page` and, when true,
`next_offset`. A page is filtered after Excalidraw+ answers, so it can hold fewer items than `limit` while
`has_next_page` is true.

## Rate limit and errors

Excalidraw+ allows 600 requests per minute and IP; Qatlas spaces requests at least 100 ms apart. After a 429 it holds
until `X-RateLimit-Reset`, for at most 60 seconds.

| Class | Cause |
| --- | --- |
| `auth` | the key was rejected (401) or is unusable; see [Fixing errors](setup.md#fixing-errors) |
| `permission` | the key may not perform this (403); see [Fixing errors](setup.md#fixing-errors) |
| `not-found` | Excalidraw+ does not hold the scene or does not show it to this key |
| `rate-limited` | Excalidraw+ answered 429 |
| `timeout`, `unreachable` | no answer in time, or the service is unavailable |
| `invalid-provider-response` | unreadable, too large, or a different scene than requested |
| `provider-error` | every other rejection, including a redirect |

A `collection_id` or scene outside the targets is an invalid request, not a provider error. Errors never carry the key,
and Qatlas' message carries no provider text; the answer of Excalidraw+ travels apart from it as the provider's
original statement in the `provider` field of the error detail, marked untrusted, redacted, and bounded.

## Boundary

The response shapes follow the API reference's pages for collections, scenes, users, logs, scene content, and rate
limiting. Error bodies and the identifier format are not documented there. What each tool group assumes beyond that
is on its page.
