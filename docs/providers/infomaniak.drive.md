---
description: >
  Describes the Infomaniak kDrive provider: API token setup, the account, drive, and folder or file binding, the drive,
  folder, metadata, and content reads, the download to a local path, the confirmed folder creation, rename,
  move, copy, and file upload or replacement, the trash reads and the confirmed trash, restore, and permanent
  deletion, the share link reads and the confirmed deletion of share links with their
  URL handling, the dropbox (upload link) read and the confirmed deletion
  of dropboxes, the access read and the confirmed granting, changing, and revoking of user,
  team, and invitation access with their membership check and personal data handling, pagination and cursor
  contracts, the Business kSuite versus personal my kSuite compatibility cases, redirect handling on downloads,
  and the boundary to Mail, CalDAV/CardDAV, and kChat.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-03
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

The target picker offers `account`, then `drive` below the chosen account. It works on a draft
connection that has no account yet, and it lists what the token reaches, not only the current targets.
Accounts are read from the drives listing without an `account_id` and grouped; their names come from the
accounts endpoint when the token may read it, otherwise an account is named by its ID. Names are untrusted
data. Lists are paged on request only; an account that holds drives on several pages can appear on each of
them. The picker never searches on the server.

A URL of the web interface on `kdrive.infomaniak.com` or `ksuite.infomaniak.com` is turned into
`drive/DRIVE_ID`, and on `ksuite.infomaniak.com` with an account number before the drive also into
`account/ACCOUNT_ID`. Any other host or form gives nothing. A folder or file in the URL is ignored, and the
entries are named by their IDs. Reading a URL sends no request; every list read counts against the shared
60 requests per minute budget, and an account page costs two.

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

### Changes

- `drive_id`, `file_id`, `parent_id`, and `destination_id` are plain positive integers of at most 18 digits,
  validated before any secret is read or request is sent, and used only as path segments of the fixed paths
  above. An object identifier is bound by the
  drive it is used in: the drive goes through the checks under Scope (the proof of a subtree binding
  included) before the change request, and Infomaniak resolves the file and the destination only inside
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
  Only the three trash tools that delete require the tool allow-list, see Trash.
- A change sends exactly one change request after the ownership check, and Qatlas never repeats it. After a
  timeout, a dropped connection, a 5xx answer, or an unreadable answer the error says the change may have
  been applied; read the folder before trying again.
- `status` is `done` when Infomaniak applied the change. When Infomaniak answers `asynchronous`, the change is
  accepted but not finished: `status` is `pending`, and `cancel_id` carries Infomaniak's handle when it
  reports one. A rename or a move answers with a cancel resource (`cancel_id`, `valid_until`) also when it is
  `done`. Qatlas offers no cancel tool. The request and response shapes follow the Infomaniak OpenAPI
  specification (`https://developer.infomaniak.com/openapi.json`).
- The creation and the copy report the new folder or file as `entry`, as untrusted data.

`infomaniak.drive.files.list` and `infomaniak.drive.files.stat` default `folder_id` and `file_id` to `1`, the
fixed identifier of a drive's own root directory, as Infomaniak documents it; `files.list` under a subtree
binding defaults as described under Scope. `infomaniak.drive.files.get`
requires an explicit `file_id`: reading content is never defaulted to the root. A folder identifier passed to
`files.get` reads Infomaniak's own zip archive of that folder, still bounded by the same size limit, rather
than being refused or silently redirected to a listing.

### Trash

- `trash.list` reads one page of the top level of the trash: `limit` (5 to 1000, default 10) and an opaque
  `cursor`, with `has_more` and `cursor` answered as for `files.list`. Each entry is the usual entry plus
  `deleted_at`; names and times are untrusted data and names are length-bounded. A trashed folder's own
  contents are not listed.
- `files.trash` sends `DELETE /2/drive/{d}/files/{id}`, `trash.delete` sends `DELETE /2/drive/{d}/trash/{id}`,
  and `trash.empty` sends `DELETE /2/drive/{d}/trash`. Neither sends a body. `file_id` is a positive integer
  other than the drive's root `1`; `trash.empty` takes only `drive_id`. A trashed entry stays restorable until
  `trash.delete` or `trash.empty` removes it; that removal and the emptying cannot be undone.
- `trash.restore` takes `destination_id`, a required folder of the same drive (the root is `1`), because the
  specification requires `destination_directory_id` in the request body; Qatlas offers no restore to a place
  outside the drive. Restoring is the only trash change that needs just the `update` effect.
- The same rules as for the other changes apply: the drive goes through the checks under Scope before the one
  request, `files.trash` proves its `file_id` like any object, the four other trash tools act on the whole drive, a refusal names no foreign value, an unclear outcome is reported
  and never repeated, and `asynchronous` is a `pending` change with the cancel handle when Infomaniak reports
  one. A trash change never reports an entry. A 403, for example because the plan does not allow trash
  handling, is reported as `permission`.

### Share links

- A share link makes a file or folder reachable by its URL. `right` decides who may open it: `inherit` only
  users of the drive (the narrowest), `password` anyone with the URL and a password, `public` anyone holding the
  URL with no further check. Deleting a link needs `confirm` for every right, `public` included, and a
  connection must list the tool by name besides holding the permission.
- `file_id` is a positive integer other than the drive's root `1`, validated, like `drive_id`, before any
  secret is read or request is sent; the checks under Scope follow, and a refusal names no foreign value. Qatlas does not offer a link for the root: whether Infomaniak
  allows one is not documented, so the narrower reading applies.
- `links.delete` removes the link; the file stays.
- No link password is ever read from a provider answer or returned.
- The link URL is its own sensitivity class, `infomaniak-kdrive-share-links`, which all three link tools carry
  instead of the file class: the URL is an access capability, not metadata. It is returned only as a string up to
  1024 characters, only when it is an absolute `https` URL without credentials; any other value from Infomaniak is
  dropped and the link is returned without `url`. The other link fields (`right`, `valid_until`, the flags,
  `access_blocked`, and the creation and update times) are untrusted data. A link is only reported for the file
  it was asked for; an answer that names another file, or an unknown `right`, is an invalid response.
- `links.list` reads one page of `GET /3/drive/{d}/files/links` with the link included (`with=sharelink`):
  `limit` (5 to 1000, default 10) and an opaque `cursor`, answered as for `files.list`. Each entry is the usual entry
  plus its `link`. `links.get` answers `not-found` for a file without a link.
- A change sends exactly one request after the ownership check and is never repeated. After a timeout, a
  dropped connection, a 5xx answer, or an unreadable answer, the error says the change may have been applied; read the link before trying again. `asynchronous` is a
  `pending` change. A 403, for example because the drive's plan does not allow share links, is
  reported as `permission`.

### Dropbox

- A dropbox is a public upload link for one folder: anyone holding its URL can upload files into the folder
  without an account. Creating or changing one would open or prolong such an access to the outside, so neither is
  offered. The tools `dropbox.get` and `dropbox.delete` act
  on exactly one folder (`file_id`, a positive integer other than the drive's root `1`) of one drive, which goes
  through the checks under Scope first; a foreign `drive_id` is refused locally,
  without naming it, before any secret is read or request is sent. Both need an explicit tools list; the
  deletion needs `confirm`.
- The dropbox has its own sensitivity class, `infomaniak-kdrive-dropboxes`. A dropbox is reported as `id`, `uuid`,
  `name`, `url`, `users_count`, `created_at`, `updated_at`, `last_uploaded_at`, `has_password`, `has_notification`,
  `has_validity`, and `has_size_limit`, all untrusted data and bounded; the `url` is returned only when it is an
  absolute `https` URL without credentials of at most 1024 characters. Other fields Infomaniak documents (the
  creator, the containing directory and user via `with`, and the remaining time, size, and usage figures) are not
  read. `dropbox.get` answers `not-found` for a folder without a dropbox; `dropbox.delete`
  answers only its `status`.
- The deletion sends exactly one request after the ownership check and is never repeated. After a timeout, a dropped
  connection, a 5xx answer, or an unreadable answer, the error says the change may have been applied; read the
  dropbox before trying again. `asynchronous` is a `pending` change. A 403, for example because the drive's plan
  does not include dropboxes, is reported as `permission`, and a provider failure as `provider-error`; neither is
  retried.

### Access

- `access.get` reads one file or folder: `users`, `teams`, and `invitations`, each entry with `id`, the access kind,
  the display `name`, the `right`, and the `status`. Names and every other value are untrusted data; an invitation's
  name can be an e-mail address. A list holds at most 200 entries and a name at most 256 bytes; `truncated` says a
  list was cut. The class of all four tools is `infomaniak-kdrive-access`, separate from file metadata and from link
  URLs, because who may reach a file is personal data.
- `access.grant` sends one `POST /2/drive/{d}/files/{id}/access` with `right`, and any of `user_ids` (at most 20),
  `team_ids` (at most 10), at least one of them. There is no `emails` or `lang` argument, since no tool
  invites by e-mail. No `message` is offered. `access.update` and `access.revoke` take exactly one of `user_id` and `team_id`. `right` is
  `read`, `write`, or `manage` (can also share); the API's `none` is not offered, because `access.revoke` removes
  access explicitly. Nothing else of the provider's body exists.
- `file_id` is a positive integer other than the drive's root `1`, checked before any secret is read or request is
  sent; the checks under Scope follow, and a refusal names no foreign value.
- Every user and team a change names is proven to belong to the same drive before the change request: after the
  ownership check, Qatlas reads the drive's users page by page with their team identifiers (`GET
  /2/drive/{d}/users?with=teams`, 100 per page, at most 10 pages, stopping once everything is found) and counts only
  users whose status is `active`. A user or team that is not among them is an invalid request that does not name the
  value, and nothing is changed. When the bound ends the search without finding it, or the page count is missing,
  the target counts as unproven and is refused the same way instead of being guessed. Infomaniak lists no teams of
  a drive on their own with the `drive` scope, so a team is provable only through an active user of the drive who
  belongs to it; a team without such a user is refused. The
  check costs one request per page; it is a read and sends nothing.
- The grant tool is `open_world`, needs `confirm`, and is never repeated. The result echoes only what Infomaniak
  reports per target, up to 256 bytes each. The invoke log holds no arguments.
- A grant answers `done`, or `partial` when Infomaniak reached some targets and refused others; `granted`, `failed`,
  and `results` (kind, target, whether reached) say which, and Infomaniak's per-target message is never read. An
  update or revoke answers its `status` and the `user_id` or `team_id`. An answer that cannot be read counts as an
  unclear outcome.
- A change sends exactly one request after the checks and is never repeated. After a timeout, a dropped connection,
  a 5xx answer, or an unreadable answer, the error says the change may have been applied; read the access before
  trying again. `asynchronous` is a `pending` change. A 403, for example because the drive's plan or the rights of
  the token's user do not allow sharing, is reported as `permission`.

### Upload

- `files.upload` creates one file in an existing folder (`directory_id`, the drive's root is `1`) or replaces
  the content of one existing file as a new version. Replacing needs `file_id` together with its current
  `etag`, sent as `If-Match`, and takes no `directory_id`, `name`, or `conflict`. If the file changed or is
  locked since, the call fails as an etag conflict and nothing is replaced. A missing folder is never created.
- The content comes from `local_path`, a file inside a directory the connection releases for reading, or inline
  from `content_base64` up to 4 MiB with a `name`. Only metadata comes back: `file_id`, `name`, `size`,
  `sha256`, `method`, `status`, and `client_token`.
- A file up to 100 MB goes in one direct request, larger ones through an upload session of several chunk
  requests; `method` reports which. `client_token`, 16 to 36 characters, is only available for a direct upload
  and makes a repeated upload return the file of the first one; Qatlas generates one when it is omitted.
- A new name that exists is refused as a `conflict` unless `conflict` is `rename`, which keeps both.
- Qatlas never repeats an upload request itself. After a timeout, a dropped connection, a 5xx answer, or an
  unreadable answer the error says the file may have been saved and names the `client_token` (or the session)
  to check or to repeat the same upload; read the folder first.
- An upload session's `upload_url` comes from Infomaniak and is only used when it is `https` on an
  `infomaniak.com` host.
- A 403, for example because the plan does not allow it, is reported as `permission`.

### Download

- `files.download` takes `drive_id`, `file_id`, and `local_path`, an absolute path or one starting with `~/` in
  an existing directory the connection releases for writing. A path outside it is refused before any secret
  is read or request is sent, without naming the path.
- An existing file at the path is kept unless the request is confirmed; with `confirm` it is replaced.
- The content goes to a temporary file that only becomes the target once the transfer is complete and matches
  the reported size, so an aborted or too large transfer leaves no partial file. The result carries `file_id`,
  `name`, `size`, and `sha256`, never the content. It costs three requests: the ownership check, a metadata
  read (the proof under a subtree binding), and the content request.
- A folder identifier writes Infomaniak's zip archive of it, as with `files.get`. The redirect rules under
  Content and size limits apply unchanged.

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
did not.

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
invalid request too, see Access. A `drive_id` outside the connection's targets, or one the live ownership check finds belongs to another
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
