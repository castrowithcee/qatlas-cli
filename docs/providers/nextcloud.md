---
description: >
  Describes Nextcloud files, file comments, shares, Deck boards, stacks, and cards, Talk conversations and messages,
  notes, calendars and events, contacts, account data, status, notifications, search, and read-only user and group
  administration, with typed targets, connection effects, and safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-21
---

# Nextcloud

A connection binds one identity to typed targets (see Targets). In a tool ID such as `nextcloud.deck.cards.get`,
the second segment is the Nextcloud service and at the same time the tool group. The Files tools form the tool
group `files` and work in the bound Files folder: they list and read metadata or up to 4 MiB of file content (`read`), create
files only when absent (`create`), replace an existing version with an ETag precondition (`update`), and
organise (see Organising). Deleting is separate (see Deleting).

The instance URL must use `https`, without exception. Qatlas follows no redirect: a 3xx answer to any request
is a clear failure that did not act on the server, and the app password only ever travels to the configured URL.

## Deleting

`nextcloud.files.files.delete` deletes one file and `nextcloud.files.folders.delete` one folder with everything in it, each
bound to the ETag of the version read (`*` is refused) and confirmed. The root folder is never deletable. When
the `files_trashbin` app is active Nextcloud moves the deleted item to the trash bin, otherwise it deletes it
for good; Qatlas cannot tell which applies (see Trash bin). Both tools require a tools list: no profile and no
effect offers them.

## Trash bin

`nextcloud.files.trash.list`, `nextcloud.files.trash.restore`, and `nextcloud.files.trash.delete` work on the deleted items of the
identity whose original location lies below the bound root folder (`/` binds all). Everything else in the trash
bin is neither listed nor touched, and `restore` and `delete` answer such an item like a missing one without
naming it. Items inside a deleted folder, emptying the trash bin, and restoring elsewhere are not offered.

`restore` puts an item from `list` back to its original location and `delete` removes it for good, each after one
read with one request; `delete` requires a tools list, and no profile contains any of the three tools. Unclear
outcomes are handled as under Local files.

## Targets

The `targets` list holds typed entries, each a kind name alone (everything of that kind the identity reaches) or
`kind/ID`:

- `folder` (the whole Files root) or `folder/PATH`; at most one. Only it enables the Files tools; without it they
  refuse locally, before any credential access or request. Relative paths cannot escape the bound folder.
- `calendar[/URI]`, `addressbook[/URI]` (the kind alone excludes the system address book), `talk[/TOKEN]`,
  `deck[/BOARD_ID]` (numeric), `notes[/PATH]` (a sub-folder such as `Work/Plans` is allowed).
- `account`: the account-wide and instance-wide reach of the identity (account data, status, notifications,
  activity, search, directory, incoming shares, system tag catalog and its administration).
- `admin`: the read-only administration of users and groups; only as the sole target.

The general and the specific form of one kind cannot be combined, and no kind lists more than 100 entries. IDs are
literal single segments without `%`, `\`, or control characters. The single field `target`, like a bare value in
`targets`, names the root folder (`Reports`, `Team/Reports`, `/`); one whose first segment is a kind name (such as
`calendar/2026`) is refused as ambiguous: write `folder/PATH`.

Credentials provide `user-id` and a revocable `app-password`. The connection test reads the bound folder (or the
Files root) and each bound calendar; it reports no metadata. Connection effects independently hide and block
operations; the identity's WebDAV rights stay the ceiling. A connection may instead select named tools with a `tools`
list, for example `[nextcloud.files.files.list, nextcloud.files.files.stat]` for metadata without file content, and
then offers exactly those. File content is base64 and never written to audit records. A tool is offered only to a
binding that holds the kind its call checks; tools that create a calendar, address book, board, or conversation need
the kind alone. The terminal editor starts a new connection on the setup profile `read`, a visible starting
selection, not a role: only the ticked `tools` are saved, and a saved connection never follows a profile.

## Choosing targets

The picker lists what the credential reaches, also on a draft. `folder` is browsed folder by folder, `notes` category
by category (from at most 2000 notes); the other kinds are one list each, first offering the kind alone. A path a
configured path would refuse is not offered. `account` and `admin` have no list.

A URL of the service's own origin (installation path and `index.php` allowed) becomes a target without a request:
`/apps/files/files/FILE_ID?dir=/PATH` gives `folder/PATH` (not the Files root), `/apps/deck/board/ID` gives
`deck/ID`, `/call/TOKEN` gives `talk/TOKEN`; anything else gives nothing.

## Organising

`nextcloud.files.folders.create`, `nextcloud.files.files.move` (a rename is a move within one folder), and
`nextcloud.files.files.copy` work on files and folders inside the bound folder and need `confirm` and their effect. Parent folders must already exist, and an existing destination is never overwritten. Source and
destination are paths below the root: the root itself is neither, they must differ, and a folder cannot go into
itself or a descendant. Qatlas checks this locally before any credential access.

Each tool sends exactly one request. A taken destination, an existing folder, or a missing parent is a clear
failure that names no path. After an unclear outcome, stat source and target, or the folder, before repeating.

## Search and favorites

`nextcloud.files.files.search` and `nextcloud.files.favorites.list` belong to the profile `read`; `nextcloud.files.files.favorite`
needs `confirm` and the `update` effect. All three work on the bound folder only; the root itself is never a
hit and cannot be marked.

The search takes structured filters that combine with AND, and at least one is required. `name_contains` is a
literal, case-insensitive substring with wildcards masked; no caller XML, sorting, or offset exists. The server
picks which matches a limit keeps, `truncated` says when it was reached, and the result is sorted by path.

Every answered favorite is checked against the bound folder again; a node outside it or refused by the server is
dropped silently, and a cut list is marked `truncated`.

`files.files.favorite` sends exactly one request without a read before it; a refused property is a clear failure. After
an unclear outcome, stat the path and list the favorites before repeating.

## Shares

The tool group `shares` reads and manages sharing. `nextcloud.shares.shares.list` and `nextcloud.shares.shares.get` (profiles
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

`nextcloud.shares.sharees.search` and the `nextcloud.shares.incoming-shares.*` and `nextcloud.shares.remote-shares.*` tools are
account-wide: they need an `account` target, not a folder, and without one refuse locally, before any credential
access or request.
`shares.sharees.search` reports only type, identifier, and display name of possible recipients, never Talk
conversations or unknown types (their identifier may be an access token), never uses the global lookup server,
asks for at most 50 candidates per kind (default 10), and is in no setup profile.

`shares.incoming-shares.pending` lists the offers the identity has not accepted, from other users of the instance
(internal) and from federated servers (host only), with origin, sender, name, and type. Two limits come from the
API: a federated offer carries no share time (its `mtime` is a file time), so none is reported; an internal one
carries its share time but no status, so offers declined earlier are listed like open ones and can be accepted
again. `accept` takes internal offers only. `decline` names the origin and needs a tools list. Both read the
pending list of that origin once, refuse an ID not pending there without naming anything else, and send exactly
one request; IDs of the two origins are independent, so `accept` refuses a federated ID like an unknown one.

`shares.remote-shares.list`, `get`, and `remove` read and remove the federated shares the identity accepted; none is
created or accepted here. A share reports ID, owner, name, its mount point relative to the Files root, and the
other server only as a host, never a token, password, or remote URL. An instance without federated sharing
answers `list` with a clear error. `remove` needs a tools list, is in no profile, reads the share once, and
refuses an unknown ID without naming a target.

`nextcloud.shares.shares.create`, `nextcloud.shares.shares.update`, and `nextcloud.shares.shares.delete` need a `folder` target, a
confirmation, and a tools list; no profile contains them. `create` shares one existing item below the root, never
the root itself, with one existing user or group (`type` `user` or `group`; find IDs with `shares.sharees.search`).
Reading is always granted; the other rights are flags that default to false. Links, e-mail, federated, and team
shares cannot be created or changed, Talk shares only by `talk.files.share`; no password or label is set. `update`
changes rights, expiry, or note of a `user` or `group` share the identity made; rights not given keep their value.
`delete` revokes any share the identity made below the root, whatever its type; shares made to it stay untouched.

`update` and `delete` read the share once and refuse without a change a share outside the root, a share that
is not the identity's own, and (for `update`) any other type; the refusal names no path. They then send exactly
one request. After an unclear outcome (timeout, aborted connection, a 5xx or unreadable answer) the error says
the change may have been applied and to check the share (`shares.shares.get`, `shares.incoming-shares.pending`, or
`shares.remote-shares.get`) before repeating; Qatlas never repeats. A refusal by the instance (for example a required
expiry or disabled sharing) is a clear error without the text of the instance.

## Deck

The tool group `deck` reads boards (labels, sharing, members), stacks with cards, card comments, and attachments, and
changes all of them; setup profile `deck-read` holds four board reads, and no profile holds a change. A `deck` target
binds every board, `deck/BOARD_ID` one board; an unbound board is refused locally, unnamed, before any credential access
or request, and listings drop unbound and deleted boards. A stack, card, label, sharing entry, or target counts only if
a read of the bound board shows it. Provider text is untrusted, capped, and marked `truncated` when cut. Only
`deck_file` attachments are downloaded or deleted; uploads take a `local_path` file up to 64 MiB.

Every change needs `confirm` and sends one request after at most two reads. `deck.boards.create` needs the general
`deck` target. Board, stack, label, and sharing changes refuse without the manage right; card changes leave the edit
right to Deck. `deck.cards.update` refuses a card changed since its `last_modified`; an archived card can only be
restored. Cards take only the board's labels and user members; `deck.acl.*` shares with users and groups only and
refuses team and owner entries. Comments work on active cards only; update and delete reach only own comments among
the newest 100. Deck deletes boards, cards, and attachments softly, with no Qatlas restore; a deleted stack takes its
cards. Every `delete` and `deck.acl` tool needs a tools list.

## Talk

The tool group `talk` uses the OCS API of Talk. Only reads are in the setup profile `talk-read`; deleting and closing a
poll for good need a tools list. A `talk` target binds every conversation of the identity, `talk/TOKEN` one; an unbound
or malformed token is refused locally, before credential access or any request, unnamed. Reads omit links, paths,
previews, session IDs, and phone numbers; texts, names, and questions are untrusted data.

Only internal conversations and existing users and groups are managed: no public, guest, e-mail, phone, federated, or
team entries. `talk.rooms.create` needs the `talk` target; `talk.rooms.update` and `.delete` need an owner or moderator.
Only `talk.rooms.mark-read` moves the read marker. `talk.participants.remove` and `.moderator` take an `attendee_id` of
`talk.participants.list`, spare owner and self, and need a tools list. `talk.polls.vote` takes option IDs of
`talk.polls.get`. `talk.files.share` needs `folder`, `talk`, a tools list and shares read-only into private
conversations; `shares.shares.delete` revokes. Every call first reads the `spreed` capability or the conversation or poll it
needs and refuses a missing app or feature; a change needs `confirm` and then sends one request. Check the list before
repeating an unclear outcome (`send`: `reference_id`; `talk.files.share`: `shares.shares.list`). 429 is rate-limited.

## Calendar

The CalDAV group `calendar` needs a `calendar` target; an unbound calendar is refused locally, unnamed. Only direct
children of the calendar home and of a bound calendar count, never subscriptions, scheduling, or trash collections.
Profile `calendar` reads events and tasks; `events` adds creating events and replacing one by `etag`. Changes need
`confirm`, deleting and sharing a tools list. Writing tasks, calendar management and sharing, and `calendar.trash.*` are
in no profile. A task is replaced only if Qatlas can write all of it. The trash covers bound calendars only: another
calendar's entry is neither listed nor counted, and refused like a missing one; whole calendars are not restored.
Sharing gives one user or group access to an own calendar, never by link, e-mail, or team; Nextcloud ignores an
unknown recipient, so check `shares` of `calendar.calendars.list`.

## Contacts

The group `contacts` (profile `contacts`) reads address books and contacts (untrusted personal data, no photos). The
system address book needs its own `addressbook/URI` target and is never written or shared. Writing is in no profile;
deleting and sharing need a tools list. A contact update replaces by `etag` and refuses a card it cannot write whole.
A new address book needs the general `addressbook` target; changing, deleting (with all contacts), or sharing one
needs it bound and owned. Sharing: one user or group, never link, e-mail, or team; unknown ones are ignored (`shares`).

## Versions

`nextcloud.files.versions.list`, `nextcloud.files.versions.get`, and `nextcloud.files.versions.restore` read and restore older
versions of one file below the connection root. The argument is always the file path, never a file ID. Qatlas
stats the path first, refuses a folder, and addresses the versions only under the file ID of that answer.
`files.versions.list` reports the newest first and marks a cut with `truncated`; a foreign node fails the call.

`files.versions.get` returns up to 4 MiB inline or writes to `local_path` exactly as `files.files.get` does.
`files.versions.restore` needs `confirm` and the `etag` of the current file and restores the version with one request.
The restore has no documented precondition, so a change made between the ETag check and the restore is not
detected. `files.versions.list` is in the setup profiles `read` and `write`; `get` and `restore` are in none.

## Notes

The group `notes` (profile `notes-read`) needs a `notes` target; `notes/PATH` binds it and all below. The Notes API
filters categories exactly, so the list is read unfiltered and foreign notes are dropped locally, never shown or
counted; a chunk may be empty despite a next cursor. A foreign note or attachment reads like a missing note; attachments
stay note-relative, `local_path` follows `files.files.get`. Writes are in no profile: create, update, and
`notes.attachments.upload` need a bound category, update also its new one and the `etag`; delete needs a tools list.

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
whole instance. The identity's rights stay the bound: a tag hidden from users (`visible` or `assignable` false)
needs administrator rights. `create` defaults both to true, takes a name of at most 64 characters, and reports
the new `tag_id` only when Nextcloud names it, otherwise the tag is found with `files.system-tags.list`. `update`
changes name, `visible`, `assignable`, or `color` (six hex digits), at least one, and cannot clear a color.
`update` and `delete` read the tag once and treat an invisible tag as missing but, unlike `files.file-tags.add`, act on
a non-assignable one. Group restrictions are not offered. A taken name is a clear error on `create`; on `update`
it cannot be told apart from a missing right. Unclear outcomes are checked with `files.system-tags.list`.

## Account

The tool group `account` reads the own account data, status, notifications, activity stream, and unified search of
the identity, changes the status, and dismisses notifications. All its tools need an `account` target and refuse
without one locally, before any credential access or request. Provider text is untrusted data, capped and marked
`truncated` when cut. Icons, previews, and rich objects are never reported, nor are links except a search hit's, which
is untrusted text and never fetched; an action of a notification shows only its label and method and is never followed.
`account.status.set` and `account.status.clear` send one request, need `confirm`, and are in no profile.

`account.notifications.list` reports at most 100 notifications; with the `etag` of a previous list an unchanged list is
reported as `unchanged`. `account.activity.list` takes a filter from a fixed list that the instance must also offer,
`limit` up to 100, and `since` from `next_cursor`. `account.notifications.dismiss` (after reading the notification) and
`account.notifications.dismiss-all` need `confirm` and a tools list, are in no profile, and send one request. An unclear
outcome is checked with `account.notifications.list` or `account.status.get` before repeating.

`account.search.providers` lists the search providers of the instance; `account.search.query` asks one of them for one page of up
to 25 hits (`term`, `limit`, `cursor` from the previous `next_cursor`). The provider ID is checked locally and
against the live list, so an unoffered one is refused without a search. Hits are title, subtitle, and link as
untrusted text; a link is never followed. Both are in no profile.

## Administration

`nextcloud.admin.users.*` and `nextcloud.admin.groups.*` read users, groups, and members as an administrator or as a
subadmin within its groups and need a tools list; an `admin` connection test reads one user page. Users are never
created or invited, and no change is offered: Nextcloud demands a password confirmation an app password cannot give.

## File comments

The `nextcloud.files.comments.*` tools read and write the comments of one file below the bound root and need a `folder`
target; folders are refused. The file ID comes only from a stat of the path, and a comment ID is only ever used
below that file. Comment text and author names are untrusted data, and long text is cut and marked. Writing needs
`confirm`, sends exactly one request after the stat, and is open-world because a mention notifies that user;
Nextcloud lets an identity change or delete only its own comments. `files.comments.list` is in the `read` and `write`
profiles, `create` and `update` in none, and `delete` is reachable only through a tools list.

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

Every change of this provider that ends unclear (timeout, aborted connection, 5xx, unreadable answer) is reported
as possibly applied, to be checked with the matching read before repeating, and is never repeated by Qatlas; for a
file, stat it. A refusal by Nextcloud (4xx) or a redirect is a clear failure.

`nextcloud.files.files.get` reads one file below the root. With `local_path` it writes the file atomically into a
directory released for writing (`files.write`) and reports only path, name, size, SHA-256, and ETag; an existing
local file is replaced only with confirmation, and the tool is offered only on a connection that releases a
directory for writing. Without `local_path` it returns up to 4 MiB as `content_base64` and refuses a larger file.
