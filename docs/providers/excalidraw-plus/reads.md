---
description: >
  Describes the Excalidraw+ collection, scene, and scene content read tools, the client-side search, and the metadata the reads leave out.
type: knowledge
edit: shared
created: 2026-10-28
updated: 2026-10-28
---

# Excalidraw+ reads

Configuration, scope, targets, error classes, and cross-group limits are in [README.md](README.md).

## Tools

| Tool | Reads |
| --- | --- |
| `excalidraw-plus.collections.list` | the allowed collections and their metadata |
| `excalidraw-plus.scenes.list` | scenes of the allowed collections; optional `collection_id` and `name` |
| `excalidraw-plus.scenes.get` | one scene's metadata, without preview address or share links (see [Boundary](#boundary)) |
| `excalidraw-plus.scenes.content` | one scene's header, elements, and file metadata, capped; optional `query`, `offset`, `limit` |

All four are read-only and safe to repeat. `scenes.list` needs a `collection_id` when the connection allows several
collections; with exactly one it is the default, with `*` it is optional.

## Scene content and search

Scene content is untrusted data. One answer holds at most 1000 elements (200 when `limit` is omitted), 1 KiB per
text, name, `original_text`, `link`, and `source`, and 256 KiB of element text in total; `truncated` and `next_offset`
say where to continue. A content body above 16 MiB is refused. Embedded file content is not returned; only each
file's metadata appears. A `link` is an address: untrusted, capped text, never fetched. Deleted elements are not part
of the visible content and are not returned. A free-form object or list over its bound (`custom_data`: depth 6, 100
entries, 4 KiB; other lists: 1000 entries; `files`: 200) is left out, never cut, and marked by a member ending in
`_omitted`. `custom_data` keeps the keys Excalidraw+ delivers.

Excalidraw+ has no content search. `query` is searched by Qatlas: an element is kept when its text or name contains the
query, ignoring case; `matched` counts all matches. `scenes.list` has only a `name` filter, applied to the returned
page.

## Boundary

Metadata the read tools leave out: `previewUrl`, since the API does not show whether the preview address works without
an API key, so it counts as an access value; `readOnlyLinks[]` and `sharedSlidesLinks[]`, since share links grant access
without sign-in and no scene read tool is released separately through `tools`; `isDeleted` and `deletedAt`, since items
in the trash are not listed and the fields would always be empty.
