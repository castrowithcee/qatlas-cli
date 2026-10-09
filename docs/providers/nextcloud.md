---
description: >
  Describes Nextcloud file operations, share reads, Deck, Talk, and note reads, typed targets, connection
  permissions, and safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-09
---

# Nextcloud

A connection binds one identity to typed targets (see Targets). The Files tools form the tool group `files` and
work in the bound Files folder: they list and read metadata or up to 4 MiB of file content (`read`), create
files only when absent (`create`), replace an existing version with an ETag precondition (`update`), and
organise (see Organising). Deleting is separate (see Deleting).

The instance URL must use `https`, without exception. Qatlas follows no redirect: a 3xx answer to any request
is a clear failure that did not act on the server, and the app password only ever travels to the configured URL.

## Deleting

`nextcloud.files.delete` deletes one file and `nextcloud.folders.delete` one folder with everything in it, each
bound to the ETag of the version read (`*` is refused) and confirmed. The root folder is never deletable. When
the `files_trashbin` app is active Nextcloud moves the deleted item to the trash bin, otherwise it deletes it
for good; Qatlas cannot tell which applies. The trash bin is read and handled with the tools under Trash bin.

Both tools require a tools list: no profile and no permission offers them. A connection without a `tools` list
therefore offers neither; it no longer offers `nextcloud.files.delete` as before.

## Trash bin

`nextcloud.trash.list`, `nextcloud.trash.restore`, and `nextcloud.trash.delete` work on the deleted items of the
identity whose original location lies below the bound root folder (`/` binds all). Everything else in the trash
bin is neither listed nor touched, and `restore` and `delete` answer such an item like a missing one without
naming it. Items inside a deleted folder, emptying the trash bin, and restoring to another place are not offered.

`list` reports at most 500 items, marks a cut list as `truncated`, and refuses an answer larger than 4 MiB.
`restore` and `delete` take the `trash_id` from `list`, read the item once, and then send one `MOVE` to the
`restore` collection or one `DELETE`. `restore` puts the item back to its original location. `delete` removes it
for good and requires a tools list like the delete tools above. No profile contains any of the three tools. An
unclear outcome is reported as for the file mutations under Local files and never repeated.

## Targets

The `targets` list holds typed entries; each kind is either its name alone (everything of that kind the
identity reaches) or `kind/ID`:

- `folder` (the whole Files root) or `folder/PATH`; at most one per connection. Only a `folder` target enables the
  Files tools; without one they refuse locally, before any credential access or request.
- `calendar` or `calendar/URI`, `addressbook` (all but the system address book) or `addressbook/URI`,
  `talk` or `talk/TOKEN`, `deck` or `deck/BOARD_ID` (numeric).
- `notes` (all notes of the identity) or `notes/CATEGORY` (that category and everything below it, such as
  `Work/Plans`; see Notes); may be listed more than once. Without a `notes` target the Notes tools refuse
  locally, before any credential access or request.
- `account`: the account-wide and instance-wide reach of the identity (notifications, activity, search,
  directory, incoming shares, system tag catalog and its administration).
- `admin`: provisioning reads; only as the sole target of a connection.

The general and the specific form of one kind cannot be combined, and no kind lists more than 100 entries.
IDs are literal single segments without `%`, `\`, or control characters.

The single field `target` keeps its meaning as the root folder: `Reports`, `Team/Reports`, and `/` (the whole
Files root) bind exactly as before, and a bare value in the `targets` list is read the same way. A single
`target` whose first path segment is a kind name (for example `calendar/2026`, or a folder named `talk`) is
refused, because it could mean a folder or a typed target; write it as `folder/PATH` in `targets`. This
refusal changes the behaviour of such existing values.

The connection test reads the bound folder, or the Files root of the identity when the connection binds none;
it reports no metadata.

Credentials provide `user-id` and a revocable `app-password`. Relative paths cannot escape the bound
folder. Connection permissions independently hide and block operations, while the identity's WebDAV rights
remain the provider-side ceiling. An optional `tools` list narrows a connection further to named tools, for
example `[nextcloud.files.list, nextcloud.files.stat]` for metadata without file content, and never admits
an effect `permissions` excludes. File content is carried as base64 and never written to audit records. The
terminal editor starts a new connection on the setup profile `read`, which ticks `[read]` and
`[nextcloud.files.list, nextcloud.files.stat, nextcloud.files.get]`; the profile `write` adds
`nextcloud.folders.create`, `nextcloud.files.move`, and `nextcloud.files.copy`. A profile is a visible starting
selection, not a role: only the ticked `permissions` and `tools` are saved, every tick can be changed before
saving, and a saved connection never follows a profile.

## Organising

`nextcloud.folders.create` (`MKCOL`, effect `create`), `nextcloud.files.move` (`MOVE`, effect `update`; a
rename is a move within one folder), and `nextcloud.files.copy` (`COPY`, effect `create`) work on files and
folders inside the bound folder and need `confirm`. A connection without `create` can run neither
`folders.create` nor `files.copy`; without `update` it cannot run `files.move`. Parent folders must already
exist (no automatic creation), and an existing destination is never overwritten (`Overwrite: F`). Source and
destination are paths below the root: the root itself is neither, they must differ, and a folder cannot go
into itself or a descendant. Qatlas checks this locally before any credential access, and builds the
`Destination` URL on the configured origin from the validated segments.

Each tool sends exactly one request. A taken destination, an existing folder, or a missing parent is a clear
failure that names no path. After an unclear outcome (timeout, aborted connection, a 5xx answer) the error says the
change may have been applied and that source and target, or the folder, must be stat-ed before repeating.

## Search and favorites

`nextcloud.files.search` (`SEARCH`) and `nextcloud.favorites.list` (`REPORT`) belong to the profile `read`;
`nextcloud.files.favorite` (`PROPPATCH` of `oc:favorite`, effect `update`, idempotent) needs `confirm` and the
permission `update`. All three work on the bound folder only; the root itself is never a hit and cannot be
marked.

The search takes structured filters that combine with AND, and at least one is required (an empty search is
refused). `name_contains` is a literal, case-insensitive substring: Qatlas masks `%`, `_`, and the escape
character `\` of Nextcloud's `d:like`. `content_type_prefix` matches the start of the MIME type,
`modified_after` and `modified_before` take RFC 3339 times (exclusive, to the second), `size_min` and `size_max`
are inclusive bytes (a folder counts with its subtree), `type` is `file` or `folder`, and `favorite` selects
favorites or non-favorites. Qatlas writes the whole request XML itself and escapes every value; no caller XML,
sorting, or offset exists. `limit` is 1 to 200 (default 50), the server picks which matches a limit keeps, and
`truncated` is true when the limit was reached. The result is sorted by path.

The favorites list reads at most 500 favorites below the root; more are cut and `truncated` says so. Every
answered node is checked against the bound folder again; a node outside it, and a node the server refused,
is dropped silently.

`files.favorite` takes `path` and `favorite` (both required) and sends exactly one request, without a read
before it. A `207` answer whose property status is an error is a clear failure. After an unclear outcome
(timeout, aborted connection, a 5xx or unreadable answer) the error says the flag may have been changed and
that the path must be stat-ed and the favorites listed before repeating; Qatlas never repeats the request.

## Shares

The tool group `shares` reads sharing. `nextcloud.shares.list` and `nextcloud.shares.get` (profiles `read` and
`write`) need a `folder` target. A share counts only when its item lies at or below the root: the `path` of a
share the identity owns, or the `file_target` of an incoming one (`shared_with_me`) in the Files tree of the
identity. `list` drops every other share; `get` answers one outside the root exactly like a missing one. A share
the identity passed on without owning it is neither own nor incoming and is not reported. Without `path`, `list`
reads all shares and filters them, so the whole subtree below the root is covered; with `path` it reads that
node, and with `subfiles` the items of that folder.

All share types are reported with a stable type name (`user`, `group`, `link`, `email`, `federated`, `team`,
`talk`, other types as `other`), rights as flags, expiry, note, and label. A share never reports its link
token, its link URL, or any password; `has_password` only says that one is set. The recipient ID is reported
for `user`, `group`, `email`, `federated`, and `team` shares; a `talk` share reports only the conversation name,
because its identifier is the conversation token, and `link` and `other` shares report no recipient. Recipients
are personal data and untrusted, like every provider string.

`nextcloud.sharees.search` searches the possible recipients of the whole instance and needs an `account`
target, not a folder. It reports only type, identifier, and display name, and never Talk conversations or
unknown types, whose identifier may be an access token. It asks for at most 50 candidates per kind (default
10), never uses the global lookup server, and is in no setup profile. Without an `account` target it refuses
locally, before any credential access or request.

Sharing is read through the OCS API of the Sharing app (`/ocs/v2.php/apps/files_sharing/api/v1`), with the same
basic authentication as WebDAV and without redirects. The client in `ocs.go` takes fixed path segments and
typed query values and accepts an answer only when the envelope reports `ok` and 200; it forwards no message
of the instance.

## Deck

The tool group `deck` reads Deck boards, stacks, and cards: `nextcloud.deckboards.list`, `nextcloud.deckboards.get`
(with labels, sharing entries, and members), `nextcloud.deckstacks.list` (with the cards of each stack; `archived`
lists the archived cards instead), and `nextcloud.deckcards.get`. The setup profile `deck-read` holds exactly these
four. They need a `deck` target and never a folder target; without one they refuse locally.

The target `deck` binds every board of the identity, `deck/BOARD_ID` only that board. `list` drops every other
board and every deleted one. A board ID that is not bound is refused locally, before any credential access or
request, without naming it. Stack and card IDs are accepted only through the board hierarchy: Deck resolves a card
by its ID alone, so `deckcards.get` first reads the board's stacks (the archived ones if needed) and refuses a card
that the named stack of the bound board does not hold, as if it did not exist; it also rejects an answer whose card
or stack differs from the requested one.

Requests go to the Deck REST API below `/index.php/apps/deck/api/v1.1/` with `OCS-APIRequest` and JSON, and follow
no redirect. A 404, which a missing Deck app causes as well as a missing object, is one clear not-found failure.
Titles, names, labels, and descriptions are untrusted: strings are cut at a fixed length (descriptions at 8 KiB, at
1 KiB in a stack listing), lists are capped, and every cut sets `truncated`. Comments and attachments are not read,
and nothing in Deck can be changed.

## Talk

The tool group `talk` reads Talk conversations through the OCS API of the Talk app (`/ocs/v2.php/apps/spreed`),
with the same client and limits as sharing: `talkrooms.list`, `talkrooms.get`, `talkparticipants.list`, and
`talkmessages.list`, all in the setup profile `talk-read` and in no other. They need a `talk` target and refuse
locally without one. A conversation counts only when the target binds it: `talk` binds every conversation of the
identity, `talk/TOKEN` one. `talkrooms.list` drops every other conversation. A tool that takes a `token`
refuses an unbound or malformed one locally, before any credential access or request, and its message names
no token; the token never reaches a path unvalidated.

Each call first reads the `spreed` capability of the instance and refuses a missing Talk app or a missing
feature clearly, instead of assuming a version; this costs one extra request per call. Participants report
actor type, actor ID, display name, role, and call state; session IDs and phone numbers are not read.

`talkmessages.list` reads one page of the history, newest first, with at most 100 messages (default 50) per
page. The server is asked for no waiting, and not to move the read marker or mark notifications as read. `next_cursor`
(the `X-Chat-Last-Given` header) continues with the older messages and is absent on the last page. A message
text is cut at 4 KiB and marked `truncated`. Placeholders such as `{actor}` or `{file}` are replaced by the name
of the rich object and listed in `objects`; links, paths, previews, and sizes of objects are never reported,
because a file shared into a conversation carries an access token in them. Message texts and names are untrusted
data.

## Versions

`nextcloud.versions.list`, `nextcloud.versions.get`, and `nextcloud.versions.restore` read and restore older
versions of one file below the connection root (developer manual, WebDAV versions: `remote.php/dav/versions/<user>/`
of the same instance). The argument is always the file path; a file ID is never accepted. Qatlas stats the
path first, refuses a folder, and addresses the versions only under the file ID of that answer. A `version_id`
is a timestamp name from `versions.list`, digits only. `versions.list` reports at most 100 versions, newest
first, and marks a cut with `truncated`; a node outside that file's version folder fails the call.

`versions.get` returns up to 4 MiB inline or writes to `local_path` exactly as `files.get` does.
`versions.restore` needs `confirm` and the `etag` of the current file and moves the version onto the
restore target of the identity with one `MOVE`. The manual documents no condition for the `MOVE`, so a change
made between the ETag check and the `MOVE` is not detected. An unclear outcome is reported as possibly
applied and never repeated. `versions.list` is in the setup profiles `read` and `write`; `get` and `restore`
are in none.

## Notes

The tool group `notes` reads the Notes app and is in the setup profile `notes-read`, which holds exactly its
tools and changes nothing. Notes are personal data and everything the tools return is untrusted.

A `notes/CATEGORY` target binds the notes whose category equals `CATEGORY` or lies below it; the comparison is
exact and case-sensitive. The category filter of the Notes API misses sub-categories, so the list is read
without a server-side filter and every note outside the binding is dropped locally: a foreign note never
appears and is not counted. Because the binding is applied after the server has chosen the chunk, a chunk may
be empty while a next cursor is still present. Reading a note outside the binding, or an attachment of one, is
answered like a missing note, and no attachment request is sent for it. An attachment is addressed relative to
its note and cannot leave it; it is returned inline or written with `local_path` as `files.get` does. The
Notes settings are account-wide and need any `notes` target.

## System tags

`nextcloud.systemtags.list` reads the visible system tags of the instance (developer manual, WebDAV system
tags: `remote.php/dav/systemtags`); the catalog belongs to the whole instance, so the tool needs an `account`
target and refuses without one before any credential access. `nextcloud.filetags.list`, `filetags.add`, and
`filetags.remove` work on one file or folder below the connection root and need a `folder` target. The
argument is always the path, never the root itself and never a file ID: Qatlas stats the path first and
addresses the relations (`remote.php/dav/systemtags-relations/files/<file_id>`) only under the file ID of that
answer. A `tag_id` is digits only, as `systemtags.list` reports it.

Only visible tags are ever listed or touched; a listing reports at most 200 tags and marks a cut with
`truncated`. `assignable` is true when the identity may assign the tag. The `filetags` tools only assign and
remove tags; they never create, change, or delete one. `filetags.add` and `filetags.remove` need `confirm`, read the tag
once after the stat, and refuse an invisible or non-assignable tag without a change request; then they send
exactly one `PUT` or `DELETE`. A tag that is already assigned, or not assigned on removal, is a clear error. An
unclear outcome is reported as possibly applied, to be checked with `filetags.list`, and never repeated.
`filetags.list` is in the setup profiles `read` and `write`; `systemtags.list`, `add`, and `remove` are in none.

`nextcloud.systemtags.create`, `systemtags.update`, and `systemtags.delete` administer the catalog, need an
`account` target and `confirm`, and are in no setup profile; `systemtags.delete` is reachable only through a
tools list and drops every assignment of the tag on the whole instance. The rights of the identity stay the
upper bound: creating or hiding a tag from users (`visible` or `assignable` false) needs administrator rights in
Nextcloud. `create` defaults both to true, takes a name of at most 64 characters, and sends one `POST`; it
reports the new `tag_id` only when Nextcloud names it as a direct child of the catalog, otherwise it reports
the tag as created without an ID, to be found with `systemtags.list`. `update` changes name, `visible`,
`assignable`, or `color` (six hex digits) with one `PROPPATCH`; it takes at least one field and cannot clear a
color. `update` and `delete` read the tag once first and treat an invisible tag as missing, but, unlike
`filetags.add`, also act on a tag the identity may not assign. Group restrictions are not offered. A taken
name is a clear error on `create`; on `update` Nextcloud reports it inside the answer, so a refused change
cannot be told apart from a missing right. An unclear outcome is reported as possibly applied, to be checked
with `systemtags.list`, and never repeated.

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
