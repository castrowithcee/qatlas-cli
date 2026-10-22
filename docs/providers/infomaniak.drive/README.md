---
description: >
  Describes the Infomaniak kDrive provider as a whole: API token setup, the account, drive, and folder or file
  binding with its ownership and subtree checks, target picking, the tool overview with effects, allow-list
  requirements and setup profiles, pagination and cursor contracts, content and size limits with redirect handling,
  error classes, untrusted data, the Business kSuite versus personal my kSuite compatibility cases, and the boundary
  to Mail, CalDAV/CardDAV, and kChat. The tool groups are described on the pages for files, trash, and sharing.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-22
---

# Infomaniak kDrive

Infomaniak kDrive is a provider for the Infomaniak kDrive REST API. It lists the drives of one Infomaniak
account, lists the immediate children of one folder page by page, reads the metadata of one file or folder,
and reads bounded file content or writes one file to a local path, all through `https://api.infomaniak.com`.
On a connection that holds the `create` or `update` effect it also creates one folder, renames, moves, or
copies one file or folder inside one drive, uploads one file or replaces the content of one, and restores a
trash entry, each only after an explicit confirmation. It reads the trash too. Moving a file or folder to the
trash, deleting a trash entry for good, and emptying the trash are separate tools that need the `delete`
effect and an explicit tools list. It reads the share link of a file or folder and lists the files that have
one; deleting a share link is a separate, confirmed tool, and all share link tools,
the reading ones included, need an explicit tools list. Qatlas creates no permanent access or data paths to the
outside (webhooks, invitations, public links, dropboxes); reading, pausing, revoking, and deleting remain, so no tool creates or
changes a share link or a dropbox and no tool invites by e-mail. It reads the dropbox (upload link) of a folder and
deletes it with a separate, confirmed tool that likewise needs an explicit tools list. It reads the access of a file or folder and grants, changes, or revokes the access of users and teams
to it; these are separate tools that each need an explicit tools list, and a user or
team is only named after it is proven to belong to the same drive.

## Configuration

The service is always the official API root `https://api.infomaniak.com`, which the terminal editor fills in
and which applies when `base_url` is left out. Qatlas refuses any other URL before a secret is read.

The credential provides `token`, an Infomaniak API token created in the Infomaniak Manager under Account
settings, API tokens. That token can reach every Infomaniak account and every kDrive its owner administers,
which matters for a person who holds one Infomaniak login but manages kDrive for several customers: the
connection target, not the token, decides which single account is reachable.

```yaml
services:
  infomaniak:
    provider: infomaniak.drive
    base_url: https://api.infomaniak.com

credentials:
  infomaniak-reader:
    provider: infomaniak.drive
    type: keyring
```

## Scope

A connection binds exactly one account and, optionally, drives and folders or files of them:

| Target | Binds |
| --- | --- |
| `account/ACCOUNT_ID` | the Infomaniak account this connection may reach; required, exactly one |
| `drive/DRIVE_ID` | one whole kDrive of that account; optional, repeatable |
| `drive/DRIVE_ID/files/FILE_ID` | one folder (with everything below it) or file of a drive; optional, repeatable; a file identifier is unique only inside its drive, the drive's root is `1` |

```yaml
connections:
  customer-a:
    service: infomaniak
    credential: infomaniak-reader
    targets: [account/1001, drive/5001, drive/5002/files/77]
```

Without a `drive` or `files` target, every kDrive of the bound account is reachable. Otherwise the reach is the
union: `drive/ID` is the whole drive, a `files` target only its subtree. A `drive_id` argument goes through
these checks before any file endpoint is reached:

1. A `drive_id` outside the drives of the targets is refused locally, as an invalid request, before any secret
   is read or request is sent.
2. Every `infomaniak.drive.files.*` (upload and download included), `infomaniak.drive.trash.*`, `infomaniak.drive.links.*`, `infomaniak.drive.dropbox.*`, `infomaniak.drive.access.*`, and `infomaniak.drive.folders.create` call then confirms with Infomaniak's own drive detail endpoint (`GET /2/drive/{drive_id}`, which needs no `account_id` of its own) that the drive belongs to the bound account, in one extra request before the file endpoint. The targets are local configuration and never trusted on their own, because the same token can otherwise reach a drive of another account. A drive of another account, or a check that failed or came back unreadable, aborts the request; there is no silent fallback.
3. Under a drive bound only by `files` targets, every `file_id`, `folder_id`, `parent_id`, `destination_id`, and `directory_id` of a call is proven to be a target or to lie below one, with one more read (`with=parents`) per identifier, counted against the shared limit; for `files.stat` and `files.download` the read they make anyway is the proof. The proof fails closed. The refusal is an invalid request that does not name the object, and a 404, a 403, or an unusable answer is not told apart from "outside".

A target itself cannot be moved or put in the trash; renaming it is allowed. `trash.list`, `trash.restore`,
`trash.delete`, `trash.empty`, and `links.list` act on a whole drive and are not reachable under a drive bound
only by `files` targets. Without `folder_id`, `files.list` lists the one `files` target of such a drive; with
several, `folder_id` is required. An object moved between the proof and the action is not caught.

The ownership check doubles the number of requests a single `files.*` call spends against the shared 60
requests per minute budget. `infomaniak.drive.drives.list` needs none: Infomaniak answers it scoped to one
`account_id`, and every returned drive is matched against it again before it is reported. A successful
`qatlas connection test` reads one page of at most one drive of the bound account: it proves the token is
accepted and may list drives of that account, not that every bound drive exists or belongs to it.

## Choosing targets

The target picker offers `account`, then `drive` below the chosen account, then `files` below a drive or below a
folder: the entries directly inside, folders marked as having children, each with the path of the listed folder
(`/` at the root; below a folder one extra read of that folder with its parents per page, no path if the chain
is unclear). It works on a draft connection. Accounts are grouped from the drives listing without an
`account_id`; names come from the accounts endpoint if readable, else the ID. Names are untrusted. Account and
drive lists are paged by number, `files` by the folder cursor. It never searches on the server.

A URL on `kdrive.infomaniak.com` or `ksuite.infomaniak.com` is turned into `drive/DRIVE_ID` (on
`ksuite.infomaniak.com` with an account number before the drive also `account/ACCOUNT_ID`), plus
`drive/DRIVE_ID/files/FILE_ID` for `…/files/FILE_ID` or the one numeric segment after `…/files/FOLDER/preview`.
Other hosts and forms give nothing; entries are named by their IDs. Reading a URL sends no request; every list
read counts against the shared 60 requests per minute budget (an account page costs two).

## Tools

| Tool | Does |
| --- | --- |
| `infomaniak.drive.drives.list` | the kDrives of the bound account, filtered to the drives of the targets, page by page |
| `infomaniak.drive.files.list` | the immediate children of one folder of one drive, cursor-paginated |
| `infomaniak.drive.files.stat` | the metadata of exactly one file or folder |
| `infomaniak.drive.files.get` | the bounded content of exactly one file, as base64 |
| `infomaniak.drive.files.download` | writes exactly one file to a local path, returning only its metadata; needs a directory the connection releases for writing |
| `infomaniak.drive.files.upload` | uploads one new file into a folder, or replaces the content of one file given its etag, from a local path or inline |
| `infomaniak.drive.folders.create` | creates exactly one folder below an existing folder (`POST /3/drive/{d}/files/{id}/directory`) |
| `infomaniak.drive.files.rename` | renames exactly one file or folder (`POST /2/drive/{d}/files/{id}/rename`) |
| `infomaniak.drive.files.move` | moves exactly one file or folder into another folder of the same drive (`POST /3/drive/{d}/files/{id}/move/{dest}`) |
| `infomaniak.drive.files.copy` | copies exactly one file or folder into another folder of the same drive (`POST /3/drive/{d}/files/{id}/copy/{dest}`) |
| `infomaniak.drive.trash.list` | the top-level entries of the trash of one drive, cursor-paginated (`GET /3/drive/{d}/trash`) |
| `infomaniak.drive.files.trash` | moves exactly one file or folder to the trash (`DELETE /2/drive/{d}/files/{id}`) |
| `infomaniak.drive.trash.restore` | restores exactly one trash entry into a folder of the same drive (`POST /2/drive/{d}/trash/{id}/restore`) |
| `infomaniak.drive.trash.delete` | deletes exactly one trash entry for good (`DELETE /2/drive/{d}/trash/{id}`) |
| `infomaniak.drive.trash.empty` | deletes every entry of the trash of one drive for good (`DELETE /2/drive/{d}/trash`) |
| `infomaniak.drive.links.get` | the share link of exactly one file or folder (`GET /2/drive/{d}/files/{id}/link`) |
| `infomaniak.drive.links.list` | the files and folders of one drive that have a share link, cursor-paginated (`GET /3/drive/{d}/files/links`) |
| `infomaniak.drive.links.delete` | deletes exactly one share link (`DELETE /2/drive/{d}/files/{id}/link`) |
| `infomaniak.drive.dropbox.get` | the dropbox of exactly one folder (`GET /2/drive/{d}/files/{id}/dropbox`) |
| `infomaniak.drive.dropbox.delete` | deletes exactly one dropbox (`DELETE /2/drive/{d}/files/{id}/dropbox`) |
| `infomaniak.drive.access.get` | the users, teams, and invited people with access to exactly one file or folder (`GET /2/drive/{d}/files/{id}/access`) |
| `infomaniak.drive.access.grant` | gives existing users and teams of the drive access in one request (`POST /2/drive/{d}/files/{id}/access`) |
| `infomaniak.drive.access.update` | changes the right of exactly one user or team (`PUT /2/drive/{d}/files/{id}/access/users/{uid}` or `.../teams/{tid}`) |
| `infomaniak.drive.access.revoke` | removes the access of exactly one user or team (`DELETE /2/drive/{d}/files/{id}/access/users/{uid}` or `.../teams/{tid}`) |

`drives.list`, `files.list`, `files.stat`, `files.get`, `files.download`, `trash.list`, `links.get`, and `links.list`
are `read`, safe, and need no confirmation. `folders.create` and `files.copy` have the effect `create`; `files.rename`, `files.move`, and
`files.upload` have the effect `update`. `trash.restore` has the effect `update`. `files.trash`, `trash.delete`, and `trash.empty` have the effect
`delete`, and each of them requires the tool allow-list: no effect alone offers it, a connection must name
the tool in its `tools` list. `links.delete` has the effect `delete`; a share link is access for whoever holds its
URL, so it requires the tool allow-list as well, whichever right the link has. `links.get` and `links.list` are
`read` and safe, but their URLs are that same access, so they require the tool allow-list too. `dropbox.get` is `read` and safe and `dropbox.delete` has the effect `delete`; a dropbox URL lets
anyone without an account upload into the folder, so both require the tool allow-list and the deletion
needs `confirm`; both are `open_world`, and the deletion is idempotent. `access.get` is `read` and safe, but it discloses people and
their rights, so it requires the tool allow-list too. `access.grant` has the effect `create`, `access.update` the
effect `update`, and `access.revoke` the effect `delete`; all three widen or narrow who reaches a file, so all
three require the tool allow-list and need `confirm`. All changes need `confirm`; they are non-idempotent except `links.delete`, `dropbox.delete`, `access.update`, and `access.revoke`, which are idempotent; `access.grant` is `create` and stays non-idempotent. The provider's default
effect stays `read`: a connection runs a change only after it offers the tool, by its `effects` or, where a
list is required, by naming it in its `tools` list.

The terminal editor offers three setup profiles. `read` is the recommended one and selects the six
read tools, `trash.list` included. `write` also selects the four folder and file organisation tools.
`upload` also selects `files.upload`. Neither `write` nor `upload` is ever
preselected. No profile contains `files.trash`, `trash.restore`, `trash.delete`, `trash.empty`, or any of the share link
tools, not even `links.get` and `links.list`, since a link URL is access for whoever holds it. No profile contains a
`dropbox.*` tool either, for the same reason. No profile contains
an `access.*` tool either, not even `access.get`, since it discloses people, their rights, and invited e-mail
addresses: a connection adds them by naming them in its `tools` list.
The profile descriptions of `write` and `upload` stay true: neither shares, grants access, nor deletes. `files.download` is only offered to a connection that releases a directory for writing, and
`files.upload` with a `local_path` only for one that releases a directory for reading, through the `files`
setting of the connection.


The tool groups are described on [files](files.md), [trash](trash.md), and [sharing](sharing.md) (share links,
dropbox, and access).

## Pagination and cursors

`infomaniak.drive.files.list`, `infomaniak.drive.trash.list`, and `infomaniak.drive.links.list` take `limit` (5 to 1000, default 10) and an opaque `cursor`, and answers
`has_more` and a `cursor` for the next page whenever Infomaniak announced one. Each call reads exactly one
Infomaniak page; Qatlas never follows `has_more` on its own, and a complete-looking page can still have
`has_more` true. The cursor is Infomaniak's own opaque value, passed back unchanged; Qatlas adds no binding
of its own.

`infomaniak.drive.drives.list` takes `page` (default 1) and reports the account's own `page`, `pages`, and
`total` transparently, exactly as Infomaniak answers them for the bound account. `drives` and `count` are
filtered to the drives of the connection's targets afterwards, so `count` can be lower than what an incomplete page
alone would suggest, and a further page of the account may still hold a bound drive that this page
did not. Its filters (`in_maintenance`, `maintenance_reasons`, `tags`) are sent to Infomaniak and narrow the
account's page only; they never widen the targets, which are still applied to every row. The page size is
fixed at 100 and the account is the connection's binding, so neither is an argument.

## Content and size limits

`infomaniak.drive.files.get` reads at most 4 MiB and returns it as base64; for a larger file, or to keep the
content out of the answer, use `infomaniak.drive.files.download`, which writes up to 10 GiB to a path instead. Content over that limit is refused
outright, never silently truncated, so a caller never receives a partial, possibly corrupt file. Infomaniak
documents that its download endpoint may answer with a redirect to the actual storage location of the
content; Qatlas follows at most one such redirect, only to an `https` location, and removes the
`Authorization` header before the redirected request is sent to any host other than
`api.infomaniak.com`, so the token is never sent to that storage location.

## Errors

Errors keep stable classes and never carry the token or a raw provider response body:

| Class | Cause |
| --- | --- |
| `auth` | Infomaniak rejected the API token |
| `permission` | this token may not perform the operation, or the account's rights refuse it; for a change this includes a drive plan that does not allow it, which Infomaniak reports as a refusal without a separate class |
| `not-found` | Infomaniak does not hold the resource, or does not show it to this token |
| `rate-limited` | Infomaniak rate-limited the request; it allows at most 60 requests per minute per token, and Qatlas already paces its own requests to stay under that limit on its own |
| `timeout` | Infomaniak did not answer in time |
| `unreachable` | Infomaniak kDrive is unavailable, in maintenance, or could not be reached |
| `invalid-provider-response` | the answer was unreadable, too large, or reported an error despite an HTTP success status; for a change the message adds that the change may have been applied |
| `provider-error` | every other rejection, including a `conflict` (HTTP 409: the name already exists or the target is not in a state that allows the change), an etag conflict of a replacing upload, a failed `If-Match` precondition and a redirect on an endpoint that must not answer with one |

A `user_id`, `team_id`, or `user_ids` or `team_ids` entry that is not proven to belong to the drive is an
invalid request too, see [Access](sharing.md#access). A `drive_id` outside the connection's targets, or one the live ownership check finds belongs to another
account, is an invalid request, never a provider error, so a scope refusal is never mistaken for a missing
drive. A failed or unreadable answer to that ownership check itself keeps its own class (`auth`,
`permission`, `rate-limited`, `timeout`, `unreachable`, or `invalid-provider-response`) and aborts the request
before the file endpoint is called either way.

## Untrusted data

Drive and file names, MIME types, and every other value in a listing or a metadata read come from the
account and are untrusted data. Qatlas normalises them into a stable envelope and never renders them, follows
a link inside them, or executes anything derived from them.

## Business kSuite and personal my kSuite

This provider works the same way against a Business kSuite drive and a personal my kSuite drive: both are
ordinary kDrives reachable through the same REST API and the same API token, and both are listed, browsed,
and read identically through the tools above. The changes, uploads, downloads, and share links use the same endpoints on both. Whether a plan
permits a change, for example a folder or file operation or an upload on a restricted my kSuite drive, is Infomaniak's
decision: a refusal arrives as `permission` (or `provider-error`) and is never worked around, a share link or dropbox included: which rights, expiry, and settings a plan allows is its decision, and a refusal is not retried with other settings, and the same holds for sharing access with users and teams, which a plan or the rights of the token's user may refuse as `permission`, and this
provider documents no plan-specific behaviour beyond that. The one documented difference between the two plans is WebDAV
access, which my kSuite does not guarantee the way a Business kSuite subscription does; this provider does
not depend on WebDAV at all; it is unaffected either way. A my kSuite account may hold fewer or more
restricted drives than a Business kSuite account, which shows up only as fewer or no rows in
`infomaniak.drive.drives.list`, never as a different error class.

## Boundary

This provider covers kDrive alone. Infomaniak Mail, CalDAV/CardDAV, and kChat are separate Infomaniak products
with their own authentication: Mail is read over IMAP with mailbox credentials, CalDAV/CardDAV over WebDAV
with Basic auth similar to the `nextcloud` provider, and kChat through a Mattermost-style token against its
own API, none of them the Bearer API token this provider uses. Because every secret role a provider declares
is mandatory for every one of its connections today, a single `infomaniak` provider spanning all of them
would force a kDrive-only connection to also declare secret roles it never uses. Each of them can be added
later as its own sibling provider without changing this one. Within kDrive itself, this provider offers no
creation or removal of users or teams of the drive, no invitation to the drive itself, no handling of access
requests, no synchronisation with the rights of the parent, no resending of an invitation, no access `message`,
no `none` right, no dropbox creation or change, no dropbox invitation (`/dropbox/invite`) and no listing or search of dropboxes, no link statistics, no trashed
folder browsing, no restore outside the drive, no copy to another drive, no `version` conflict mode, no search,
no activity or version history, and no account, settings, or quota administration.
