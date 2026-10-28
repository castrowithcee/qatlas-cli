---
description: >
  Describes the Nextcloud provider: connection, tool IDs, configuration (https and redirect rule, credential),
  tool groups, targets, exclusions, choosing targets, and unclear outcomes. Tool groups are described on separate pages for
  files, shares, Deck, Talk, calendar, contacts, notes, account, and administration.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-10-28
---

# Nextcloud

A connection binds one identity to typed targets (see [Targets](#targets)). In a tool ID such as `nextcloud.deck.cards.get`,
the second segment is the Nextcloud service and at the same time the tool group.

[Set up Nextcloud](setup.md) leads through creating an app password and lists the rights, roles, and apps each tool
group needs.

## Configuration

The instance URL must use `https`, without exception. Qatlas follows no redirect: a 3xx answer to any request
is a clear failure that did not act on the server, and the app password only ever travels to the configured URL.

Credentials provide `user-id` and a revocable `app-password`. The connection test reads the bound folder (or the
Files root) and each bound calendar; it reports no metadata.

## Tool groups

| Group | Page |
|---|---|
| `files` | [files.md](files.md) |
| `shares` | [shares.md](shares.md) |
| `deck` | [deck.md](deck.md) |
| `talk` | [talk.md](talk.md) |
| `calendar` | [calendar.md](calendar.md) |
| `contacts` | [contacts.md](contacts.md) |
| `notes` | [notes.md](notes.md) |
| `account` | [account.md](account.md) |
| `admin` | [admin.md](admin.md) |

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

Connection effects independently hide and block operations; the rights of the identity are listed in
[Set up Nextcloud](setup.md#rights-per-tool-group). A connection may instead select named tools with a `tools`
list, for example `[nextcloud.files.files.list, nextcloud.files.files.stat]` for metadata without file content, and
then offers exactly those. File content is base64 and never written to audit records. A tool is offered only to a
binding that holds the kind its call checks; tools that create a calendar, address book, board, or conversation need
the kind alone. The terminal editor starts a new connection on the setup profile `read`, a visible starting
selection, not a role: only the ticked `tools` are saved, and a saved connection never follows a profile.

### Exclusions

`exclude` takes `folder/PATH` and `notes/PATH` entries that take a folder, or a notes category, with everything
below it out of what the targets reach, and `calendar/URI`, `addressbook/URI`, and `deck/BOARD_ID` entries that take
one calendar, address book, or board out of the kind alone. A path entry must lie strictly below a `folder` or `notes`
target of its own kind; an ID entry needs the kind alone as target, never an ID target. Entries must not repeat or
nest in another, and at most 100 are allowed per kind; Qatlas reads them like targets and refuses a connection whose
entry it cannot enforce. The scheduling and trash collections of the calendar home and the system address book are
outside the reach of the kind alone, so an entry naming one is invalid. `talk/ID`, any exclusion on a connection with
an `account` target, and a folder exclusion next to a `talk` target are refused as not supported. An exclusion beats a
target and removes no tool from the offer; the tools that create a calendar, address book, or board stay available,
as their IDs are generated.

- Entries are compared after Unicode normalisation and case folding, so `ü`, `u` plus a combining mark, and `Ü`
  name one folder, and `ß` meets `SS`; calendar and address book URIs compare the same way. A board ID compares as a
  number, so `07` names board 7. An exclusion may hide more than it names, never less. Targets stay exact.
- A category is first normalised as the Notes app does before it names a folder (it drops `* | / \ : " < > ?`,
  leading dots, and surrounding white space), so a category that only matches after that is excluded too.
- A refused call answers as the server answers for something that does not exist, or with `permission`; no
  answer names the object or the exclusion. A refusal that needs no read is raised before any credential access.
  An excluded calendar or address book answers as one that does not exist, an excluded board as a board the
  connection is not bound to. Lists leave excluded objects out and do not count them.
- Notes live as files below the notes path of the identity. A connection that binds both a folder and notes
  therefore carries each kind of exclusion over to the other: a folder exclusion below the notes path hides the
  notes of that category, one on or above the notes path hides every note, and a notes exclusion hides the folder `NOTES_PATH/CATEGORY`
  in the Files and Shares tools. The notes path is read from the Notes settings on every call that needs it; if
  that read fails, the call is refused.
- Totals Nextcloud keeps, such as the size and ETag of a folder and the quota, still count excluded content; see
  [files](files.md#exclusions).

## Choosing targets

The picker lists what the credential reaches, also on a draft. `folder` is browsed folder by folder, `notes` category
by category (from at most 2000 notes); the other kinds are one list each, first offering the kind alone. A path a
configured path would refuse is not offered. `account` and `admin` have no list.

A URL of the service's own origin (installation path and `index.php` allowed) becomes a target without a request:
`/apps/files/files/FILE_ID?dir=/PATH` gives `folder/PATH` (not the Files root), `/apps/deck/board/ID` gives
`deck/ID`, `/call/TOKEN` gives `talk/TOKEN`; anything else gives nothing.

## Unclear outcomes

Every change of this provider that ends unclear (timeout, aborted connection, 5xx, unreadable answer) is reported
as possibly applied, to be checked with the matching read before repeating, and is never repeated by Qatlas; for a
file, stat it. A refusal by Nextcloud (4xx) or a redirect is a clear failure.

## Errors

Qatlas' message of a failed request names no path and carries no text of Nextcloud. What Nextcloud answered goes
apart in the `provider` field of the error detail, marked untrusted, redacted, and bounded: the HTTP status and,
by API, the error document of WebDAV (`files`, `contacts`, `calendar`; its conditions and the SabreDAV exception and
message), the status code and message of the OCS answer (never its data), the status and message of Deck, or the
plain text of Notes. A redirect passes on only its status.
