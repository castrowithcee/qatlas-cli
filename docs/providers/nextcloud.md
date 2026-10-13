---
description: >
  Describes Nextcloud files, file comments, shares, Deck boards, stacks, and cards, Talk conversations and messages,
  notes, calendars and events, contacts, account data, status, notifications, and search, with typed targets,
  connection permissions, and safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-13
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
for good; Qatlas cannot tell which applies (see Trash bin). Both tools require a tools list: no profile and no
permission offers them.

## Trash bin

`nextcloud.trash.list`, `nextcloud.trash.restore`, and `nextcloud.trash.delete` work on the deleted items of the
identity whose original location lies below the bound root folder (`/` binds all). Everything else in the trash
bin is neither listed nor touched, and `restore` and `delete` answer such an item like a missing one without
naming it. Items inside a deleted folder, emptying the trash bin, and restoring elsewhere are not offered.

`restore` puts an item from `list` back to its original location and `delete` removes it for good, each after one
read with one request; `delete` requires a tools list, and no profile contains any of the three tools. Unclear
outcomes are handled as under Local files.

## Targets

The `targets` list holds typed entries; each kind is either its name alone (everything of that kind the
identity reaches) or `kind/ID`:

- `folder` (the whole Files root) or `folder/PATH`; at most one per connection. Only a `folder` target enables the
  Files tools; without one they refuse locally, before any credential access or request.
- `calendar` or `calendar/URI`, `addressbook` (never the system address book) or `addressbook/URI`,
  `talk` or `talk/TOKEN`, `deck` or `deck/BOARD_ID` (numeric), `notes` or `notes/CATEGORY` (a sub-folder
  such as `Work/Plans` is allowed).
- `account`: the account-wide and instance-wide reach of the identity (account data, status, notifications,
  activity, search, directory, incoming shares, system tag catalog and its administration).
- `admin`: provisioning reads; only as the sole target of a connection.

The general and the specific form of one kind cannot be combined, and no kind lists more than 100 entries.
IDs are literal single segments without `%`, `\`, or control characters.

The single field `target`, like a bare value in `targets`, names the root folder (`Reports`, `Team/Reports`, `/`).
One whose first segment is a kind name (for example `calendar/2026`) is refused as ambiguous; write
`folder/PATH` in `targets`.

The connection test reads the bound folder (or the Files root) and each bound calendar; it reports no metadata.
Credentials provide `user-id` and a revocable `app-password`. Relative paths cannot escape the bound folder.
Connection permissions independently hide and block operations; the identity's WebDAV rights stay the ceiling.
An optional `tools` list narrows a connection further to named tools, for example `[nextcloud.files.list,
nextcloud.files.stat]` for metadata without file content, and never admits an effect `permissions` excludes.
File content is carried as base64 and never written to audit records. The terminal editor starts a new
connection on the setup profile `read`, a visible starting selection, not a role: only the ticked `permissions`
and `tools` are saved, and a saved connection never follows a profile.

## Organising

`nextcloud.folders.create`, `nextcloud.files.move` (a rename is a move within one folder), and
`nextcloud.files.copy` work on files and folders inside the bound folder and need `confirm` and the permission of
their effect. Parent folders must already exist, and an existing destination is never overwritten. Source and
destination are paths below the root: the root itself is neither, they must differ, and a folder cannot go into
itself or a descendant. Qatlas checks this locally before any credential access.

Each tool sends exactly one request. A taken destination, an existing folder, or a missing parent is a clear
failure that names no path. After an unclear outcome, stat source and target, or the folder, before repeating.

## Search and favorites

`nextcloud.files.search` and `nextcloud.favorites.list` belong to the profile `read`; `nextcloud.files.favorite`
needs `confirm` and the permission `update`. All three work on the bound folder only; the root itself is never a
hit and cannot be marked.

The search takes structured filters that combine with AND, and at least one is required. `name_contains` is a
literal, case-insensitive substring with wildcards masked; no caller XML, sorting, or offset exists. The server
picks which matches a limit keeps, `truncated` says when it was reached, and the result is sorted by path.

Every answered favorite is checked against the bound folder again; a node outside it or refused by the server is
dropped silently, and a cut list is marked `truncated`.

`files.favorite` sends exactly one request without a read before it; a refused property is a clear failure. After
an unclear outcome, stat the path and list the favorites before repeating.

## Shares

The tool group `shares` reads and manages sharing. `nextcloud.shares.list` and `nextcloud.shares.get` (profiles
`read` and `write`) need a `folder` target. A share counts only when its item lies at or below the root: the
`path` of a share the identity owns, or the `file_target` of an incoming one (`shared_with_me`). `list` drops
every other share; `get` answers one outside the root like a missing one. A share the identity passed on without
owning it is neither own nor incoming and is not reported. Without `path`, `list` reads and filters all shares;
with `path` it reads that node, with `subfiles` the items of that folder.

All share types are reported with a stable type name (`user`, `group`, `link`, `email`, `federated`, `team`,
`talk`, other types as `other`), rights as flags, expiry, note, and label. A share never reports its link
token, its link URL, or any password; `has_password` only says that one is set. The recipient ID is reported
for `user`, `group`, `email`, `federated`, and `team` shares; a `talk` share reports only the conversation name
and `link` and `other` shares no recipient. Recipients are personal data and untrusted.

`nextcloud.sharees.search` and the `nextcloud.incomingshares.*` and `nextcloud.remoteshares.*` tools are
account-wide: they need an `account` target, not a folder, and without one refuse locally, before any credential
access or request.
`sharees.search` reports only type, identifier, and display name of possible recipients, never Talk
conversations or unknown types (their identifier may be an access token), never uses the global lookup server,
asks for at most 50 candidates per kind (default 10), and is in no setup profile.

`incomingshares.pending` lists the offers the identity has not accepted, from other users of the instance
(internal) and from federated servers (host only), with origin, sender, name, and type. Two limits come from the
API: a federated offer carries no share time (its `mtime` is a file time), so none is reported; an internal one
carries its share time but no status, so offers declined earlier are listed like open ones and can be accepted
again. `accept` takes internal offers only. `decline` names the origin and needs a tools list. Both read the
pending list of that origin once, refuse an ID not pending there without naming anything else, and send exactly
one request; IDs of the two origins are independent, so `accept` refuses a federated ID like an unknown one.

`remoteshares.list`, `get`, and `remove` read and remove the federated shares the identity accepted; none is
created or accepted here. A share reports ID, owner, name, its mount point relative to the Files root, and the
other server only as a host, never a token, password, or remote URL. An instance without federated sharing
answers `list` with a clear error. `remove` needs a tools list, is in no profile, reads the share once, and
refuses an unknown ID without naming a target.

`nextcloud.shares.create`, `nextcloud.shares.update`, and `nextcloud.shares.delete` need a `folder` target, a
confirmation, and a tools list; no profile contains them. `create` shares one existing item below the root, never
the root itself, with one existing user or group (`type` `user` or `group`; find IDs with `sharees.search`).
Reading is always granted; the other rights are flags that default to false. Links, e-mail, federated, team, and
Talk shares cannot be created or changed, and no password or label is set. `update` changes the rights, expiry,
or note of a `user` or `group` share the identity made; rights not given keep their value. `delete` revokes any
share the identity made below the root, whatever its type. Shares made to the identity stay untouched.

`update` and `delete` read the share once and refuse without a change a share outside the root, a share that
is not the identity's own, and (for `update`) any other type; the refusal names no path. They then send exactly
one request. After an unclear outcome (timeout, aborted connection, a 5xx or unreadable answer) the error says
the change may have been applied and to check the share (`shares.get`, `incomingshares.pending`, or
`remoteshares.get`) before repeating; Qatlas never repeats. A refusal by the instance (for example a required
expiry or disabled sharing) is a clear error without the text of the instance.

## Deck

The tool group `deck` reads boards (labels, sharing, members), stacks with cards, card comments, and attachments, and
changes all of them; setup profile `deck-read` holds four board reads, and no profile holds a change. A `deck` target
binds every board, `deck/BOARD_ID` one board; an unbound board is refused locally, unnamed, before any credential access
or request, and listings drop unbound and deleted boards. A stack, card, label, sharing entry, or target counts only if
a read of the bound board shows it. Provider text is untrusted, capped, and marked `truncated` when cut. Only
`deck_file` attachments are downloaded or deleted; uploads take a `local_path` file up to 64 MiB.

Every change needs `confirm` and sends one request after at most two reads. `deckboards.create` needs the general
`deck` target. Board, stack, label, and sharing changes refuse without the manage right; card changes leave the edit
right to Deck. `deckcards.update` refuses a card changed since its `last_modified`; an archived card can only be
restored. Cards take only the board's labels and user members; `deckacl.*` shares with users and groups only and
refuses team and owner entries. Comments work on active cards only; update and delete reach only own comments among
the newest 100. Deck deletes boards, cards, and attachments softly, with no Qatlas restore; a deleted stack takes its
cards. Every `delete` and `deckacl` tool needs a tools list.

## Talk

The tool group `talk` uses the OCS API of the Talk app. Read tools form the setup profile `talk-read`; change tools
are in no profile, and deleting needs a tools list. A `talk` target binds every conversation of the identity,
`talk/TOKEN` one; listings drop unbound conversations, and an unbound or malformed token is refused locally, before
any credential access or request, without naming it. A call first reads the `spreed` capability, or for an existing
conversation the conversation or its participants, and refuses a missing app or feature.

Reading never waits for new messages, moves the read marker, or marks notifications as read. Links, paths, and
previews of rich objects, session IDs, and phone numbers are never reported. Texts and names are untrusted data.

Only internal conversations and existing users and groups are managed, never public conversations, guests, e-mail,
phone, federated, or team entries. `talkrooms.create` needs the `talk` target; `talkrooms.update` and
`talkrooms.delete` need an owner or moderator. `talkparticipants.remove` and `talkparticipants.moderator` take an
`attendee_id` of `talkparticipants.list`, spare owner and self, and need a tools list.

A change needs `confirm` and sends one request after one read. Check the list before repeating an unclear outcome
(`send`: `reference_id`). 429 is rate-limited.

## Calendar

The CalDAV group `calendar` needs a `calendar` target; an unbound calendar is refused locally, unnamed. Only direct
children of the calendar home and of a bound calendar count, never subscriptions or the scheduling and trash
collections. Profile `calendar` reads events, and tasks (VTODO) of calendars that take them; profile `events` also
creates events and replaces one by `etag`. A change needs `confirm`; deleting needs a tools list. Writing tasks,
managing calendars, and `calendartrash.*` are in no profile; a task is replaced only while it holds nothing Qatlas
cannot write, and the trash covers bound calendars only: an entry of another calendar is neither listed nor counted
and is refused like a missing one; whole calendars are not restored.

## Contacts

The group `contacts` (profile `contacts`) reads address books and contacts; contact fields are untrusted personal data
and photos are never requested. The system address book needs its own `addressbook/URI` target and is read-only: it is
never changed or deleted. Writing is in no profile. A contact update replaces by `etag` and refuses a card with a photo,
group kind, or other property Qatlas does not write. Creating an address book needs the general `addressbook` target;
changing or deleting one needs it bound and owned by the identity, never shared. Deleting a contact or an address book
(with all its contacts) needs a tools list.

## Versions

`nextcloud.versions.list`, `nextcloud.versions.get`, and `nextcloud.versions.restore` read and restore older
versions of one file below the connection root. The argument is always the file path, never a file ID. Qatlas
stats the path first, refuses a folder, and addresses the versions only under the file ID of that answer.
`versions.list` reports the newest first and marks a cut with `truncated`; a foreign node fails the call.

`versions.get` returns up to 4 MiB inline or writes to `local_path` exactly as `files.get` does.
`versions.restore` needs `confirm` and the `etag` of the current file and restores the version with one request.
The restore has no documented precondition, so a change made between the ETag check and the restore is not
detected. `versions.list` is in the setup profiles `read` and `write`; `get` and `restore` are in none.

## Notes

The group `notes` (profile `notes-read`) needs a `notes` target. `notes/CATEGORY` binds that category and all below
it. The Notes API filters categories exactly, so the list is read unfiltered and foreign notes are dropped locally,
never shown or counted; a chunk may be empty while a next cursor is present. A foreign note or its attachment reads
like a missing note. Attachments stay relative to their note; `local_path` follows `files.get`.

## System tags

`nextcloud.systemtags.list` reads the visible system tags of the instance and, like the catalog administration,
needs an `account` target, refused locally without one. `nextcloud.filetags.list`, `filetags.add`, and
`filetags.remove` work on one file or folder below the root and need a `folder` target. The argument is always the
path, never the root or a file ID: Qatlas stats it and addresses the relations only under the file ID of that
answer. A `tag_id` is digits only.

Only visible tags are ever listed or touched, and a cut listing is marked `truncated`; `assignable` says whether
the identity may assign a tag. The `filetags` tools only assign and remove tags. `add` and `remove` need
`confirm`, read the tag once after the stat, refuse an invisible or non-assignable tag, and then send exactly one
request; a tag already assigned, or not assigned on removal, is a clear error. Unclear outcomes are checked with
`filetags.list`. `filetags.list` is in the setup profiles `read` and `write`; the other tag tools are in none.

`nextcloud.systemtags.create`, `systemtags.update`, and `systemtags.delete` administer the catalog, need
`confirm`, and are in no setup profile; `delete` needs a tools list and drops every assignment of the tag on the
whole instance. The identity's rights stay the bound: a tag hidden from users (`visible` or `assignable` false)
needs administrator rights. `create` defaults both to true, takes a name of at most 64 characters, and reports
the new `tag_id` only when Nextcloud names it, otherwise the tag is found with `systemtags.list`. `update`
changes name, `visible`, `assignable`, or `color` (six hex digits), at least one, and cannot clear a color.
`update` and `delete` read the tag once and treat an invisible tag as missing but, unlike `filetags.add`, act on
a non-assignable one. Group restrictions are not offered. A taken name is a clear error on `create`; on `update`
it cannot be told apart from a missing right. Unclear outcomes are checked with `systemtags.list`.

## Account

The tool group `account` reads the own account data, status, notifications, activity stream, and unified search of
the identity, changes the status, and dismisses notifications. All its tools need an `account` target and refuse
without one locally, before any credential access or request. Provider text is untrusted data, capped and marked
`truncated` when cut. Links, icons, previews, and rich objects are never reported; an action of a notification shows
only its label and method and is never followed.

`account.get` reports only ID, display name, email, language, locale, up to 50 groups, and storage use as numbers.
Storage paths, backend details, phone, and address are never reported. `status.get` reports type, message, icon,
and end of the message. `status.set` takes exactly one of a `type` (`online`, `away`, `dnd`, `invisible`,
`offline`), a `predefined_id`, or a `message` of at most 80 characters, so one call sends one request. `expires`
(Unix seconds or RFC 3339, in the future) ends a message and is refused with `type`. `status.clear` removes the
message and keeps the type. Both need `confirm` and are in no profile.

`notifications.list` reports at most 100 notifications; with the `etag` of a previous list an unchanged list is
reported as `unchanged`. `activity.list` takes a filter from a fixed list that the instance must also offer,
`limit` up to 100, and `since` from `next_cursor`. `notifications.dismiss` (after reading the notification) and
`notifications.dismissall` need `confirm` and a tools list, are in no profile, and send one request. An unclear
outcome is checked with `notifications.list` or `status.get` before repeating.

`search.providers` lists the search providers of the instance; `search.query` asks one of them for one page of up
to 25 hits (`term`, `limit`, `cursor` from the previous `next_cursor`). The provider ID is checked locally and
against the live list, so an unoffered one is refused without a search. Hits are title, subtitle, and link as
untrusted text; a link is never followed. Both are in no profile.

## File comments

The `nextcloud.comments.*` tools read and write the comments of one file below the bound root and need a `folder`
target; folders are refused. The file ID comes only from a stat of the path, and a comment ID is only ever used
below that file. Comment text and author names are untrusted data, and long text is cut and marked. Writing needs
`confirm`, sends exactly one request after the stat, and is open-world because a mention notifies that user;
Nextcloud lets an identity change or delete only its own comments. `comments.list` is in the `read` and `write`
profiles, `create` and `update` in none, and `delete` is reachable only through a tools list.

## Local files

`nextcloud.files.create` and `nextcloud.files.update` take exactly one of `content_base64` (up to 4 MiB) or
`local_path`, a file inside a directory the connection releases for reading (`files.read`), streamed. The result
reports only path, name, size, SHA-256 of the bytes read, ETag, and `method`: `single` for one PUT of at most
64 MiB, `chunked` above that. `update` replaces only an existing file whose `etag` is that of the version read;
`*` or an unusable `etag` is refused before any credential access, so no connection without `create` creates one.

A chunked upload uses the chunked upload v2 of Nextcloud on the same instance, sends each chunk once, and moves
the result to the target at the end; a failure before that removes the upload best effort and leaves no file.
The final move has no documented precondition, so Qatlas stats the target before the first chunk and again
before the move (`create`: absent, `update`: the given `etag`); a change in between is not detected.

Every change of this provider that ends unclear (timeout, aborted connection, 5xx, unreadable answer) is reported
as possibly applied, to be checked with the matching read before repeating, and is never repeated by Qatlas; for a
file, stat it. A refusal by Nextcloud (4xx) or a redirect is a clear failure.

`nextcloud.files.get` reads one file below the root. With `local_path` it writes the file atomically into a
directory released for writing (`files.write`) and reports only path, name, size, SHA-256, and ETag; an existing
local file is replaced only with confirmation, and the tool is offered only on a connection that releases a
directory for writing. Without `local_path` it returns up to 4 MiB as `content_base64` and refuses a larger file.
