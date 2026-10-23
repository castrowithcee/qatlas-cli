---
description: >
  Describes the Infomaniak kChat file info, search, upload, and download tools with their binding and behaviour
  rules.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-23
---

# Infomaniak kChat files

The files tools of the [Infomaniak kChat provider](README.md). The effects and tool lists are in the
[tool overview](README.md#tools); the target checks are described under [Scope](README.md#scope).

- A file (`files.info`, `files.download`) is bound only through the `post_id` kChat reports in its info:
  Qatlas reads the info first, refuses a file without a `post_id` or a deleted file, and applies the post
  binding before any content is requested. `messages.files` binds its `post_id` the same way.
- An upload (`files.upload`) binds its `channel_id` like a message. A file an upload created belongs to no
  message yet, so `messages.send` attaches it only after reading its info: the uploader must be the token's
  own user, no message may hold the file, and, when kChat reports the file's channel, it must be the target
  channel. Any other `file_ids` entry refuses the whole send as an invalid request before the post is sent.
- `files.upload` and `files.download` follow the local file contracts of `infomaniak.drive.files.upload` and
  `.download` (released directories, fixed size limits, no partial file, content never returned). The upload's
  multipart body holds only `channel_id` and the file; kChat cannot list or remove an upload no message holds,
  so it stays. A download redirect, for example to storage, is a `provider-error`.
