---
description: >
  Describes creating, renaming, and moving Excalidraw+ scenes with the manage profile.
type: knowledge
edit: shared
created: 2026-10-28
updated: 2026-10-28
---

# Excalidraw+ scene changes

Configuration, scope, targets, error classes, and cross-group limits are in [README.md](README.md).

## Creating, renaming, and moving scenes

The `manage` profile (not recommended) adds two tools that change data. Each needs `confirm`, sends exactly one
request, and is never repeated.

| Tool | Does |
| --- | --- |
| `excalidraw-plus.scenes.create` | creates an empty, unpinned scene: `name` and `collection_id` |
| `excalidraw-plus.scenes.update` | renames and/or moves a scene: `scene_id` and `name` and/or `collection_id` |

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

## Boundary

The bodies of `POST /scenes` and `PATCH /scenes/{sceneId}` (`name`, `pinned`,
`collectionId`) follow the API reference; the effect of a repeated request is not documented.
