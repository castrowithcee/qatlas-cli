---
description: >
  Describes Nextcloud file operations, fixed Files roots, connection permissions, and WebDAV safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-06
---

# Nextcloud

A connection binds one identity to one fixed folder in the Files app. It lists and reads metadata or up to
4 MiB of file content (`read`), creates files only when absent (`create`), replaces an existing version with
an ETag precondition (`update`), and deletes only files with an ETag precondition (`delete`). It never offers
recursive folder deletion; parent folders must already exist.

Credentials provide `user-id` and a revocable `app-password`. Relative paths cannot escape the configured
root. Connection permissions independently hide and block operations, while the identity's WebDAV rights
remain the provider-side ceiling. An optional `tools` list narrows a connection further to named tools, for
example `[nextcloud.files.list, nextcloud.files.stat]` for metadata without file content, and never admits
an effect `permissions` excludes. File content is carried as base64 and never written to audit records. The
terminal editor starts a new connection on the setup profile `read`, which ticks `[read]` and
`[nextcloud.files.list, nextcloud.files.stat, nextcloud.files.get]`. A profile is a visible starting
selection, not a role: only the ticked `permissions` and `tools` are saved, every tick can be changed before
saving, and a saved connection never follows a profile.

## Local files

`nextcloud.files.create` and `nextcloud.files.update` take exactly one of `content_base64` (up to 4 MiB) or
`local_path`. `local_path` is a file inside a directory the connection releases for reading (`files.read`);
it is streamed, not loaded into memory, as one PUT of at most 64 MiB, and the result reports only the path,
name, size, and SHA-256. A larger file is refused until chunked upload exists. `update` keeps its `etag`
precondition. A failed or unclear upload is never retried.

`nextcloud.files.get` reads one file below the root. With `local_path` it writes the file into a directory
released for writing (`files.write`), atomically, and reports only path, name, size, SHA-256, and ETag; an
existing local file is replaced only with confirmation. Without `local_path` it returns up to 4 MiB as
`content_base64` and refuses a larger file with a hint to use `local_path`. Because the tool declares
local write access, it is offered only on a connection that releases a directory for writing.
Transfers with `local_path` use a 30 minute limit instead of the 30 seconds of every other request.
