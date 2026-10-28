---
description: >
  Describes patching and replacing Excalidraw+ scene content, the accepted element format, and what the endpoints are assumed to do.
type: knowledge
edit: shared
created: 2026-10-28
updated: 2026-10-28
---

# Excalidraw+ scene content editing

Configuration, scope, targets, error classes, and cross-group limits are in [README.md](README.md).

## Editing scene content

The `manage` profile also offers two content tools. Both need `confirm`, send exactly one request, and are never
repeated. Both first read the scene's metadata and continue only when its collection is allowed; a scene of another
collection receives no `PATCH` or `PUT`.

| Tool | Request | Does |
| --- | --- | --- |
| `excalidraw-plus.content.patch` | `PATCH /scenes/{id}/content` | merges 1 to 100 elements into the scene |
| `excalidraw-plus.content.replace` | `PUT /scenes/{id}/content` | replaces the whole content with 1 to 500 elements |

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
the connection also needs the `delete` effect); the `manage` profile does not make it available on its own. An
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
passes. The request body is at most 1 MiB. An uncertain outcome is reported as for scene changes, with idempotency
`unknown`. The answer reports `scene_id`, `scene_version`, `sent`, `element_count` (not deleted, as the answer shows),
and, for a patch, `not_applied`.

## Boundary

The content endpoints follow the API reference pages for scene content and its schema: `PATCH` takes a partial scene
content with `elements`, `appState`, and `files` (at least one), merges elements by id with the higher `version`
winning (a tie is broken by `versionNonce`), deletes through `isDeleted: true`, answers 200 with the merged content,
and does not force editors to reload; `PUT` takes `type`, `version`, `source`, `appState`, `elements`, and `files`,
replaces everything, forces editors to reload, and recomputes `sceneVersion`; both answer 400, 401, 403, or 404 as
`{statusCode, error, message}`. Not documented, and therefore assumed: that a patch element must be complete, that
the server rejects or ignores a stale version without an error (Qatlas detects it from the answer), the defaults
Qatlas fills for omitted element fields, the accepted `source` value, a size limit, and the effect of a repeated
request.
