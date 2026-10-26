---
description: >
  Describes Nextcloud Files tools: reading, creating, updating, organising, deleting, trash bin, search and favorites, versions, system tags, file comments, and local files.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-26
---

# Nextcloud files

Group `files`: files, folders, trash bin, favorites, versions, system tags, and file comments.

The Files tools work in the bound Files folder: they list and read metadata or up to 4 MiB of file content (`read`),
create files only when absent (`create`), replace an existing version with an ETag precondition (`update`), and
organise (see [Organising](#organising)). Deleting is separate (see [Deleting](#deleting)).

The metadata of a file or folder covers the WebDAV properties Nextcloud documents, including those an app or server
feature such as group folders, file locking, or reminders adds when it is active, with these limits. The temporary
direct download address grants access without login and is not reported, nor is its expiry. Lock tokens are access
values and are never reported. The class 2 lock properties are placeholders with fixed values in Nextcloud and are not
reported. The folder workspace text is file content, which metadata reads never return; its file ID is reported.
App-registered metadata properties have no documented set and are not reported. A nested or multi-valued property over
a bound is left out whole and named in `omitted_fields`, never cut.

## Organising

`nextcloud.files.folders.create`, `nextcloud.files.files.move` (a rename is a move within one folder), and
`nextcloud.files.files.copy` work on files and folders inside the bound folder and need `confirm` and their effect. Parent folders must already exist, and an existing destination is never overwritten. Source and
destination are paths below the root: the root itself is neither, they must differ, and a folder cannot go into
itself or a descendant. Qatlas checks this locally before any credential access.

Each tool sends exactly one request. A taken destination, an existing folder, or a missing parent is a clear
failure that names no path. After an unclear outcome, stat source and target, or the folder, before repeating.

## Deleting

`nextcloud.files.files.delete` deletes one file and `nextcloud.files.folders.delete` one folder with everything in it, each
bound to the ETag of the version read (`*` is refused) and confirmed. The root folder is never deletable. When
the `files_trashbin` app is active Nextcloud moves the deleted item to the trash bin, otherwise it deletes it
for good; Qatlas cannot tell which applies (see [Trash bin](#trash-bin)). Both tools require a tools list: no profile and no
effect offers them.

## Trash bin

`nextcloud.files.trash.list`, `nextcloud.files.trash.restore`, and `nextcloud.files.trash.delete` work on the deleted items of the
identity whose original location lies below the bound root folder (`/` binds all). Everything else in the trash
bin is neither listed nor touched, and `restore` and `delete` answer such an item like a missing one without
naming it. Items inside a deleted folder, emptying the trash bin, and restoring elsewhere are not offered.

`restore` puts an item from `list` back to its original location and `delete` removes it for good, each after one
read with one request; `delete` requires a tools list, and no profile contains any of the three tools. Unclear
outcomes are handled as under [Unclear outcomes](README.md#unclear-outcomes).

## Search and favorites

`nextcloud.files.files.search` and `nextcloud.files.favorites.list` belong to the profile `read`; `nextcloud.files.files.favorite`
needs `confirm` and the `update` effect. All three work on the bound folder only; the root itself is never a
hit and cannot be marked.

The search takes structured filters that combine with AND, and at least one is required. `name_contains` is a
literal, case-insensitive substring with wildcards masked; no caller XML or offset exists, and `order_by` is
no filter. The server picks which matches a limit keeps and `truncated` says when it was reached. With
`order_by` the result keeps the server's order; without it the result is sorted by path.
Search returns only the properties Nextcloud documents as selectable; other entry fields stay absent there.

Every answered favorite is checked against the bound folder again; a node outside it or refused by the server is
dropped silently, and a cut list is marked `truncated`.

`files.files.favorite` sends exactly one request without a read before it; a refused property is a clear failure. After
an unclear outcome, stat the path and list the favorites before repeating.

## Versions

`nextcloud.files.versions.list`, `nextcloud.files.versions.get`, and `nextcloud.files.versions.restore` read and restore older
versions of one file below the connection root. The argument is always the file path, never a file ID. Qatlas
stats the path first, refuses a folder, and addresses the versions only under the file ID of that answer.
`files.versions.list` reports the newest first and marks a cut with `truncated`; a foreign node fails the call.

`files.versions.get` returns up to 4 MiB inline or writes to `local_path` exactly as `files.files.get` does.
`files.versions.restore` needs `confirm` and the `etag` of the current file and restores the version with one request.
The restore has no documented precondition, so a change made between the ETag check and the restore is not
detected. `files.versions.list` is in the setup profiles `read` and `write`; `get` and `restore` are in none.

## System tags

`nextcloud.files.system-tags.list` reads the visible system tags of the instance and, like the catalog administration,
needs an `account` target, refused locally without one. `nextcloud.files.file-tags.list`, `files.file-tags.add`, and
`files.file-tags.remove` work on one file or folder below the root and need a `folder` target. The argument is always the
path, never the root or a file ID: Qatlas stats it and addresses the relations only under the file ID of that
answer. A `tag_id` is digits only.

Only visible tags are ever listed or touched, and a cut listing is marked `truncated`; `assignable` says whether
the identity may assign a tag. The `files.file-tags` tools only assign and remove tags. `add` and `remove` need
`confirm`, read the tag once after the stat, refuse an invisible or non-assignable tag, and then send exactly one
request; a tag already assigned, or not assigned on removal, is a clear error. Unclear outcomes are checked with
`files.file-tags.list`. `files.file-tags.list` is in the setup profiles `read` and `write`; the other tag tools are in none.

`nextcloud.files.system-tags.create`, `files.system-tags.update`, and `files.system-tags.delete` administer the catalog, need
`confirm`, and are in no setup profile; `delete` needs a tools list and drops every assignment of the tag on the
whole instance. The rights for hidden tags are under [Files](setup.md#files).
`create` defaults both to true, takes a name of at most 64 characters, and reports
the new `tag_id` only when Nextcloud names it, otherwise the tag is found with `files.system-tags.list`. `update`
changes name, `visible`, `assignable`, or `color` (six hex digits), at least one, and cannot clear a color.
`update` and `delete` read the tag once and treat an invisible tag as missing but, unlike `files.file-tags.add`, act on
a non-assignable one. Group restrictions are not offered. A taken name is a clear error on `create`; on `update`
it cannot be told apart from a missing right. Unclear outcomes are checked with `files.system-tags.list`.

## File comments

The `nextcloud.files.comments.*` tools read and write the comments of one file below the bound root and need a `folder`
target; folders are refused. The file ID comes only from a stat of the path, and a comment ID is only ever used
below that file. Comment text and author names are untrusted data, and long text is cut and marked. Writing needs
`confirm`, sends exactly one request after the stat, and is open-world because a mention notifies that user;
`files.comments.list` is in the `read` and `write` profiles, `create` and `update` in none, and `delete` is
reachable only through a tools list.

## Local files

`nextcloud.files.files.create` and `nextcloud.files.files.update` take exactly one of `content_base64` (up to 4 MiB) or
`local_path`, a file inside a directory the connection releases for reading (`files.read`), streamed. The result
reports only path, name, size, SHA-256 of the bytes read, ETag, and `method`: `single` for one PUT of at most
64 MiB, `chunked` above that. `update` replaces only an existing file whose `etag` is that of the version read;
`*` or an unusable `etag` is refused before any credential access, so no connection without `create` creates one.

A chunked upload uses the chunked upload v2 of Nextcloud on the same instance, sends each chunk once, and moves
the result to the target at the end; a failure before that removes the upload best effort and leaves no file.
The final move has no documented precondition, so Qatlas stats the target before the first chunk and again
before the move (`create`: absent, `update`: the given `etag`); a change in between is not detected.

`nextcloud.files.files.get` reads one file below the root. With `local_path` it writes the file atomically into a
directory released for writing (`files.write`) and reports only path, name, size, SHA-256, and ETag; an existing
local file is replaced only with confirmation, and the tool is offered only on a connection that releases a
directory for writing. Without `local_path` it returns up to 4 MiB as `content_base64` and refuses a larger file.
