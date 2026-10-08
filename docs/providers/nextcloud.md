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

The instance URL must use `https`, without exception. Qatlas follows no redirect: a 3xx answer to any request
is a clear failure that did not act on the server, and the app password only ever travels to the configured URL.

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
it is streamed, not loaded into memory. The result reports only the path, name, size, SHA-256 of the bytes
read, ETag, and `method`: `single` for one PUT of at most 64 MiB, `chunked` above that.

A chunked upload uses the chunked upload v2 of Nextcloud (developer manual, read 2026-10-06): `MKCOL` of one
folder with a random ID below `remote.php/dav/uploads/<user>/` of the same instance, one `PUT` per chunk
(10 MiB each, the last one smaller, at most 10000 chunks), and a final `MOVE` of `<folder>/.file` to the
target. Every request but the cleanup carries `Destination` for the already checked target and
`OC-Total-Length`. Each chunk is sent once. A failure before the `MOVE` removes the folder best effort and
leaves no file at the target. The manual documents no `If-Match` or `If-None-Match` for the `MOVE`, so Qatlas
stats the target before the first chunk and again before the `MOVE`: `create` requires it to be absent,
`update` requires the given `etag`. A file created or changed in the short time between that last check and
the `MOVE` is not detected. Nextcloud removes an unfinished upload folder after about 24 hours.

If the outcome of a file mutation is unclear (timeout, aborted connection, a 5xx answer), the error says the
change may have been applied (the file may have been stored by `create` or `update`, deleted by `delete`) and
that the file must be stat-ed before repeating. This applies to `content_base64`, single, and chunked writes.
Qatlas never repeats such a request itself. A refusal by Nextcloud (4xx) or a redirect is a clear failure.

`update` replaces only an existing file: its `etag` must be the ETag of that version, sent as `If-Match`. `*` or
another unusable `etag` is refused before any credential access or request, so a connection without the `create`
permission never creates a file. `create` always sends `If-None-Match: *`.

`nextcloud.files.get` reads one file below the root. With `local_path` it writes the file into a directory
released for writing (`files.write`), atomically, and reports only path, name, size, SHA-256, and ETag; an
existing local file is replaced only with confirmation. Without `local_path` it returns up to 4 MiB as
`content_base64` and refuses a larger file with a hint to use `local_path`. Because the tool declares
local write access, it is offered only on a connection that releases a directory for writing.
Transfers with `local_path` use a 30 minute limit instead of the 30 seconds of every other request.
