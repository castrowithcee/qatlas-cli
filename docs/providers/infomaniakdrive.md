---
description: >
  Describes the Infomaniak kDrive provider: API token setup, the account and drive allow-list, the drive,
  folder, metadata, and content reads, the confirmed folder creation, rename, move, and copy, pagination and cursor contracts, the Business kSuite versus personal
  my kSuite compatibility cases, redirect handling on downloads, and the boundary to Mail, CalDAV/CardDAV,
  and kChat.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-01
---

# Infomaniak kDrive

Infomaniak kDrive is a provider for the Infomaniak kDrive REST API. It lists the drives of one Infomaniak
account, lists the immediate children of one folder page by page, reads the metadata of one file or folder,
and reads bounded file content, all through `https://api.infomaniak.com`. On a connection that holds the
`create` or `update` permission it also creates one folder and renames, moves, or copies one file or folder
inside one drive, each only after an explicit confirmation. It uploads, shares, links, or deletes nothing.

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
    provider: infomaniakdrive
    base_url: https://api.infomaniak.com

credentials:
  infomaniak-reader:
    provider: infomaniakdrive
    type: keyring
```

## Scope

A connection binds exactly one account and, optionally, an allow-list of its drives:

| Target | Binds |
| --- | --- |
| `account/ACCOUNT_ID` | the Infomaniak account this connection may reach; required, exactly one |
| `drive/DRIVE_ID` | one kDrive of that account; optional, repeatable |

```yaml
connections:
  customer-a:
    service: infomaniak
    credential: infomaniak-reader
    targets: [account/1001, drive/5001]
```

Without a `drive/DRIVE_ID` target, every kDrive of the bound account is reachable. With one or more, only
those drives are. A `drive_id` argument goes through two checks before any file endpoint is reached:

1. A `drive_id` outside a configured drive allow-list is refused locally, as an invalid request, before any
   secret is read or any request is sent.
2. Every `infomaniakdrive.files.*` and `infomaniakdrive.folders.create` call then confirms with Infomaniak's own drive detail endpoint (`GET /2/drive/{drive_id}`, which needs no
   `account_id` of its own) that the drive actually belongs to the bound account, in one extra request before
   the file endpoint itself. The allow-list is local configuration a person wrote; it is never trusted on its
   own, because the same token can otherwise reach a drive of another account, and a drive named in an
   allow-list is not proof of which account it really belongs to. A drive of another account, or a check that
   failed or came back unreadable, aborts the request right there; there is no silent fallback to the file
   endpoint.

This second check doubles the number of requests a single `files.*` call spends against the shared 60
requests per minute budget. `infomaniakdrive.drives.list` needs no such check: Infomaniak already answers it
scoped to one `account_id`, and every returned drive is still matched against that same `account_id` again
defensively before it is reported. A successful `qatlas connection test` reads one page of at most one drive
of the bound account: it proves the token is accepted and that it may list drives of that account, not that
every drive on a narrower allow-list exists, is reachable, or actually belongs to that account.

## Tools

| Tool | Does |
| --- | --- |
| `infomaniakdrive.drives.list` | the kDrives of the bound account, filtered to the allow-list, page by page |
| `infomaniakdrive.files.list` | the immediate children of one folder of one drive, cursor-paginated |
| `infomaniakdrive.files.stat` | the metadata of exactly one file or folder |
| `infomaniakdrive.files.get` | the bounded content of exactly one file, as base64 |
| `infomaniakdrive.folders.create` | creates exactly one folder below an existing folder (`POST /3/drive/{d}/files/{id}/directory`) |
| `infomaniakdrive.files.rename` | renames exactly one file or folder (`POST /2/drive/{d}/files/{id}/rename`) |
| `infomaniakdrive.files.move` | moves exactly one file or folder into another folder of the same drive (`POST /3/drive/{d}/files/{id}/move/{dest}`) |
| `infomaniakdrive.files.copy` | copies exactly one file or folder into another folder of the same drive (`POST /3/drive/{d}/files/{id}/copy/{dest}`) |

The first four are `read`, safe, and need no confirmation. `folders.create` and `files.copy` have the effect
`create`, `files.rename` and `files.move` the effect `update`; all four are non-idempotent, need
`confirm`, and are never a delete. The provider's default permission stays `read`: a connection runs a change
only after its permissions name `create` or `update` and its tools list offers the tool.

The terminal editor offers two setup profiles. `read` is the recommended one and ticks `[read]` and the four
read tools. `write` also ticks `[create]`, `[update]`, and the four change tools; it is never preselected.

### Changes

- `drive_id`, `file_id`, `parent_id`, and `destination_id` are plain positive integers of at most 18 digits,
  validated before any secret is read or request is sent, and used only as path segments of the fixed paths
  above. A connection binds an account and drives, not single files, so an object identifier is bound by the
  drive it is used in: the drive goes through the allow-list check and the live ownership check described
  under Scope before the change request, and Infomaniak resolves the file and the destination only inside
  that one drive. A refusal does not name the value. The drive's root (`1`) can be a destination or a parent,
  but is never renamed, moved, or copied, and `destination_id` must differ from `file_id`.
- A new name (`name`) is 1 to 255 bytes of text, without a slash, a control character, or the special names
  `.` and `..`. Only that name is ever sent as a request body; no free URL, method, or provider body exists.
- `files.move` and `files.copy` take an optional `conflict`, a fixed enum of `error` (default) and `rename`,
  and always send it explicitly, because Infomaniak's own default differs: `copy` defaults to `rename`, `move`
  to `error`. With `error` a name that already exists at the destination is refused (HTTP 409), reported as a
  `conflict` provider error, and nothing changes; with `rename` both are kept and the new one gets an available
  name. `version`, which the specification also lists for `copy`, is not offered, because it replaces the
  content of an existing file; any other value is refused locally. Neither tool takes a `name`, and
  `folders.create` and `files.rename` send nothing but `name`: no `color`, `only_for_me`, or `relative_path`.
  No tool of this provider requires the tool allow-list.
- A change sends exactly one change request after the ownership check, and Qatlas never repeats it. After a
  timeout, a dropped connection, a 5xx answer, or an unreadable answer the error says the change may have
  been applied; read the folder before trying again.
- `status` is `done` when Infomaniak applied the change. When Infomaniak answers `asynchronous`, the change is
  accepted but not finished: `status` is `pending`, and `cancel_id` carries Infomaniak's handle when it
  reports one. A rename or a move answers with a cancel resource (`cancel_id`, `valid_until`) also when it is
  `done`. Qatlas offers no cancel tool. The request and response shapes follow the Infomaniak OpenAPI
  specification (`https://developer.infomaniak.com/openapi.json`).
- The creation and the copy report the new folder or file as `entry`, as untrusted data.

`infomaniakdrive.files.list` and `infomaniakdrive.files.stat` default `folder_id` and `file_id` to `1`, the
fixed identifier of a drive's own root directory, as Infomaniak documents it. `infomaniakdrive.files.get`
requires an explicit `file_id`: reading content is never defaulted to the root. A folder identifier passed to
`files.get` reads Infomaniak's own zip archive of that folder, still bounded by the same size limit, rather
than being refused or silently redirected to a listing.

## Pagination and cursors

`infomaniakdrive.files.list` takes `limit` (5 to 1000, default 10) and an opaque `cursor`, and answers
`has_more` and a `cursor` for the next page whenever Infomaniak announced one. Each call reads exactly one
Infomaniak page; Qatlas never follows `has_more` on its own, and a complete-looking page can still have
`has_more` true. The cursor is Infomaniak's own opaque value, passed back unchanged; Qatlas adds no binding
of its own.

`infomaniakdrive.drives.list` takes `page` (default 1) and reports the account's own `page`, `pages`, and
`total` transparently, exactly as Infomaniak answers them for the bound account. `drives` and `count` are
filtered to the connection's allow-list afterwards, so `count` can be lower than what an incomplete page
alone would suggest, and a further page of the account may still hold an allow-listed drive that this page
did not.

## Content and size limits

`infomaniakdrive.files.get` reads at most 4 MiB and returns it as base64. Content over that limit is refused
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
| `provider-error` | every other rejection, including a `conflict` (HTTP 409: the name already exists or the target is not in a state that allows the change) and a redirect on an endpoint that must not answer with one |

A `drive_id` outside the connection's allow-list, or one the live ownership check finds belongs to another
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
and read identically through the tools above. The changes use the same endpoints on both. Whether a plan
permits a change, for example a folder or file operation on a restricted my kSuite drive, is Infomaniak's
decision: a refusal arrives as `permission` (or `provider-error`) and is never worked around, and this
provider documents no plan-specific behaviour beyond that. The one documented difference between the two plans is WebDAV
access, which my kSuite does not guarantee the way a Business kSuite subscription does; this provider does
not depend on WebDAV at all; it is unaffected either way. A my kSuite account may hold fewer or more
restricted drives than a Business kSuite account, which shows up only as fewer or no rows in
`infomaniakdrive.drives.list`, never as a different error class.

## Boundary

This provider covers kDrive alone. Infomaniak Mail, CalDAV/CardDAV, and kChat are separate Infomaniak products
with their own authentication: Mail is read over IMAP with mailbox credentials, CalDAV/CardDAV over WebDAV
with Basic auth similar to the `nextcloud` provider, and kChat through a Mattermost-style token against its
own API, none of them the Bearer API token this provider uses. Because every secret role a provider declares
is mandatory for every one of its connections today, a single `infomaniak` provider spanning all of them
would force a kDrive-only connection to also declare secret roles it never uses. Each of them can be added
later as its own sibling provider without changing this one. Within kDrive itself, this provider offers no
upload, share, link, trash, delete, or restore operation, no copy to another drive, no `version` conflict mode, no
search, no activity or version history,
and no account, settings, or quota administration.
