---
description: >
  Describes moving Excalidraw+ scenes and collections to the trash.
type: knowledge
edit: shared
created: 2026-10-28
updated: 2026-10-28
---

# Excalidraw+ deletion

Configuration, scope, targets, error classes, and cross-group limits are in [README.md](README.md).

## Deleting scenes and collections

The `manage` profile also lists two deleting tools. Both have the effect `delete`, need `confirm`, are offered only to
a connection whose `tools` list names them (and with the `delete` effect), send exactly one request, and are never
repeated.

| Tool | Request | Does |
| --- | --- | --- |
| `excalidraw-plus.scenes.delete` | `DELETE /scenes/{id}` | moves a scene to the trash: `scene_id` |
| `excalidraw-plus.collections.delete` | `DELETE /collections/{id}` | moves a collection to the trash: `collection_id` |

- Excalidraw+ soft-deletes: the scene or collection goes to the trash of Excalidraw+. **Shared links and embeds of the
  scene, or of all scenes in the collection, stop working.** Restoring is only possible inside Excalidraw+ (trash in
  the app), not through Qatlas.
- `scenes.delete` first reads the scene's metadata and continues only when its collection is allowed; a scene of
  another collection receives no `DELETE` and the refusal does not name its collection.
- `collections.delete` checks the `collection_id` against the targets before any secret is read or request is sent,
  then reads the collection once. The `private` collection is never deletable, and the default collection is refused
  (`is_default`); both are narrower than the API requires.
- The answer is `id` and `deleted: true`; the response body of Excalidraw+ is not read. An uncertain outcome is
  reported as for scene changes, with idempotency `unknown`.

## Boundary

The delete endpoints follow the API reference (`DELETE` of a scene or collection is a soft deletion into the trash).
Assumed: the paths `DELETE /scenes/{id}` and `DELETE /collections/{id}`, that any 2xx counts (the body is dropped), that
the scenes of a deleted collection leave with it, whether the default collection may be deleted (refused locally), that
`GET /collections/{id}` answers the collection object of the list, and the effect of a repeated request.
