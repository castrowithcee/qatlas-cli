---
description: >
  Describes the Infomaniak kDrive provider: API token setup, the account and drive allow-list, the drive,
  folder, metadata, and content reads, the download to a local path, the confirmed folder creation, rename,
  move, copy, and file upload or replacement, the trash reads and the confirmed trash, restore, and permanent
  deletion, the share link reads and the confirmed deletion of share links with their
  URL handling, the dropbox (upload link) read and the confirmed creation, change, and deletion
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
On a connection that holds the `create` or `update` permission it also creates one folder, renames, moves, or
copies one file or folder inside one drive, uploads one file or replaces the content of one, and restores a
trash entry, each only after an explicit confirmation. It reads the trash too. Moving a file or folder to the
trash, deleting a trash entry for good, and emptying the trash are separate tools that need the `delete`
permission and an explicit tools list. It reads the share link of a file or folder and lists the files that have
one; deleting a share link is a separate, confirmed tool, and all share link tools,
the reading ones included, need an explicit tools list. Qatlas creates no permanent access or data paths to the
outside (webhooks, invitations, public links); reading, pausing, revoking, and deleting remain, so no tool creates or
changes a share link. It reads the dropbox (upload link) of a folder and
creates, changes, or deletes it with separate, confirmed tools that likewise all need an explicit tools list. It reads the access of a file or folder and grants, changes, or revokes the access of users, teams, and
invited e-mail addresses to it; these are separate tools that each need an explicit tools list, and a user or
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
2. Every `infomaniakdrive.files.*` (upload and download included), `infomaniakdrive.trash.*`, `infomaniakdrive.links.*`, `infomaniakdrive.dropbox.*`, `infomaniakdrive.access.*`, and `infomaniakdrive.folders.create` call then confirms with Infomaniak's own drive detail endpoint (`GET /2/drive/{drive_id}`, which needs no
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
| `infomaniakdrive.files.download` | writes exactly one file to a local path, returning only its metadata; needs a directory the connection releases for writing |
| `infomaniakdrive.files.upload` | uploads one new file into a folder, or replaces the content of one file given its etag, from a local path or inline |
| `infomaniakdrive.folders.create` | creates exactly one folder below an existing folder (`POST /3/drive/{d}/files/{id}/directory`) |
| `infomaniakdrive.files.rename` | renames exactly one file or folder (`POST /2/drive/{d}/files/{id}/rename`) |
| `infomaniakdrive.files.move` | moves exactly one file or folder into another folder of the same drive (`POST /3/drive/{d}/files/{id}/move/{dest}`) |
| `infomaniakdrive.files.copy` | copies exactly one file or folder into another folder of the same drive (`POST /3/drive/{d}/files/{id}/copy/{dest}`) |
| `infomaniakdrive.trash.list` | the top-level entries of the trash of one drive, cursor-paginated (`GET /3/drive/{d}/trash`) |
| `infomaniakdrive.files.trash` | moves exactly one file or folder to the trash (`DELETE /2/drive/{d}/files/{id}`) |
| `infomaniakdrive.trash.restore` | restores exactly one trash entry into a folder of the same drive (`POST /2/drive/{d}/trash/{id}/restore`) |
| `infomaniakdrive.trash.delete` | deletes exactly one trash entry for good (`DELETE /2/drive/{d}/trash/{id}`) |
| `infomaniakdrive.trash.empty` | deletes every entry of the trash of one drive for good (`DELETE /2/drive/{d}/trash`) |
| `infomaniakdrive.links.get` | the share link of exactly one file or folder (`GET /2/drive/{d}/files/{id}/link`) |
| `infomaniakdrive.links.list` | the files and folders of one drive that have a share link, cursor-paginated (`GET /3/drive/{d}/files/links`) |
| `infomaniakdrive.links.delete` | deletes exactly one share link (`DELETE /2/drive/{d}/files/{id}/link`) |
| `infomaniakdrive.dropbox.get` | the dropbox of exactly one folder (`GET /2/drive/{d}/files/{id}/dropbox`) |
| `infomaniakdrive.dropbox.create` | creates exactly one dropbox for a folder (`POST /2/drive/{d}/files/{id}/dropbox`) |
| `infomaniakdrive.dropbox.update` | changes exactly one dropbox (`PUT /2/drive/{d}/files/{id}/dropbox`) |
| `infomaniakdrive.dropbox.delete` | deletes exactly one dropbox (`DELETE /2/drive/{d}/files/{id}/dropbox`) |
| `infomaniakdrive.access.get` | the users, teams, and invited people with access to exactly one file or folder (`GET /2/drive/{d}/files/{id}/access`) |
| `infomaniakdrive.access.grant` | gives users, teams, and e-mail addresses access in one request (`POST /2/drive/{d}/files/{id}/access`) |
| `infomaniakdrive.access.update` | changes the right of exactly one user or team (`PUT /2/drive/{d}/files/{id}/access/users/{uid}` or `.../teams/{tid}`) |
| `infomaniakdrive.access.revoke` | removes the access of exactly one user or team (`DELETE /2/drive/{d}/files/{id}/access/users/{uid}` or `.../teams/{tid}`) |

`drives.list`, `files.list`, `files.stat`, `files.get`, `files.download`, `trash.list`, `links.get`, and `links.list`
are `read`, safe, and need no confirmation. `folders.create` and `files.copy` have the effect `create`; `files.rename`, `files.move`, and
`files.upload` have the effect `update`. `trash.restore` has the effect `update`. `files.trash`, `trash.delete`, and `trash.empty` have the effect
`delete`, and each of them requires the tool allow-list: no permission alone offers it, a connection must name
the tool in its `tools` list. `links.delete` has the effect `delete`; a share link is access for whoever holds its
URL, so it requires the tool allow-list as well, whichever right the link has. `links.get` and `links.list` are
`read` and safe, but their URLs are that same access, so they require the tool allow-list too. `dropbox.get` is `read` and safe, `dropbox.create` has the
effect `create`, `dropbox.update` the effect `update`, and `dropbox.delete` the effect `delete`; a dropbox URL lets
anyone without an account upload into the folder, so all four require the tool allow-list and the three changes
need `confirm`; all four are `open_world`. `dropbox.create` is non-idempotent, the others are idempotent. `access.get` is `read` and safe, but it discloses people and
their rights, so it requires the tool allow-list too. `access.grant` has the effect `create`, `access.update` the
effect `update`, and `access.revoke` the effect `delete`; all three widen or narrow who reaches a file, so all
three require the tool allow-list and need `confirm`. All changes need `confirm`; they are non-idempotent except `links.delete`, `access.update`, and `access.revoke`, which are idempotent; `access.grant` can mail people, so it is not. The provider's default
permission stays `read`: a connection runs a change only after its permissions name the effect and its tools
list offers the tool where one is required.

The terminal editor offers three setup profiles. `read` is the recommended one and ticks `[read]` and the six
read tools, `trash.list` included. `write` also ticks `[create]`, `[update]`, and the four folder and file organisation tools.
`upload` also ticks `[update]` and `files.upload`. Neither `write` nor `upload` is ever
preselected. No profile contains `files.trash`, `trash.restore`, `trash.delete`, `trash.empty`, or any of the share link
tools, not even `links.get` and `links.list`, since a link URL is access for whoever holds it. No profile contains a
`dropbox.*` tool either, for the same reason. No profile contains
an `access.*` tool either, not even `access.get`, since it discloses people, their rights, and invited e-mail
addresses: a connection adds them by naming them in its `tools` list together with the matching permission.
The profile descriptions of `write` and `upload` stay true: neither shares, grants access, nor deletes. `files.download` is only offered to a connection that releases a directory for writing, and
`files.upload` with a `local_path` only for one that releases a directory for reading, through the `files`
setting of the connection.

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

`infomaniakdrive.files.list` and `infomaniakdrive.files.stat` default `folder_id` and `file_id` to `1`, the
fixed identifier of a drive's own root directory, as Infomaniak documents it. `infomaniakdrive.files.get`
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
  outside the drive. Restoring is the only trash change that needs just the `update` permission.
- The same rules as for the other changes apply: the drive goes through the allow-list check and the live
  ownership check before the one request, a refusal names no foreign value, an unclear outcome is reported
  and never repeated, and `asynchronous` is a `pending` change with the cancel handle when Infomaniak reports
  one. A trash change never reports an entry. A 403, for example because the plan does not allow trash
  handling, is reported as `permission`.

### Share links

- A share link makes a file or folder reachable by its URL. `right` decides who may open it: `inherit` only
  users of the drive (the narrowest), `password` anyone with the URL and a password, `public` anyone holding the
  URL with no further check. Deleting a link needs `confirm` for every right, `public` included, and a
  connection must list the tool by name besides holding the permission.
- `file_id` is a positive integer other than the drive's root `1`, validated, like `drive_id`, before any
  secret is read or request is sent; the drive goes through the allow-list check and the live ownership check
  first, and a refusal names no foreign value. Qatlas does not offer a link for the root: whether Infomaniak
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
  without an account. The four tools `dropbox.get`, `dropbox.create`, `dropbox.update`, and `dropbox.delete` act
  on exactly one folder (`file_id`, a positive integer other than the drive's root `1`) of one drive, which goes
  through the allow-list check and the live ownership check first; a foreign `drive_id` is refused locally,
  without naming it, before any secret is read or request is sent. Every one of them, `dropbox.get` included,
  needs an explicit tools list; the three changes need `confirm`.
- `dropbox.create` and `dropbox.update` accept only these settings, sent only when given: `alias` (1 to 100
  bytes, no control character), `password` (8 to 128 characters, no control character), `valid_until` (RFC 3339,
  in the future, at most ten years ahead, sent as a timestamp), `limit_file_size` (1 to 1099511627776 bytes), and
  `email_when_finished` (boolean). Infomaniak also accepts `null` for `alias`, `limit_file_size`, `password`, and
  `valid_until` to clear them; Qatlas does not offer that, so a setting cannot be removed again, and the
  documented bounds of `alias` and `limit_file_size` are Qatlas's own, since Infomaniak documents none.
  `dropbox.update` needs at least one setting; `dropbox.create` takes none or any.
- The password is registered with the redactor before any request, is only ever the request body to Infomaniak,
  and never appears in a result, an error, or the invocation log; the dropbox reports only `has_password`.
- The dropbox has its own sensitivity class, `infomaniak-kdrive-dropboxes`. A dropbox is reported as `id`, `uuid`,
  `name`, `url`, `users_count`, `created_at`, `updated_at`, `last_uploaded_at`, `has_password`, `has_notification`,
  `has_validity`, and `has_size_limit`, all untrusted data and bounded; the `url` is returned only when it is an
  absolute `https` URL without credentials of at most 1024 characters. Other fields Infomaniak documents (the
  creator, the containing directory and user via `with`, and the remaining time, size, and usage figures) are not
  read. `dropbox.get` answers `not-found` for a folder without a dropbox; `dropbox.update` and `dropbox.delete`
  answer only their `status`; `dropbox.create` answers the created dropbox.
- A change sends exactly one request after the ownership check and is never repeated. After a timeout, a dropped
  connection, a 5xx answer, or an unreadable answer, and also when a created dropbox comes back unreadable, the
  error says the change may have been applied; read the dropbox before trying again. `asynchronous` is a
  `pending` change. A 403, for example because the drive's plan does not include dropboxes, is reported as
  `permission`, and a provider failure as `provider-error`; neither is retried with other settings.

### Access

- `access.get` reads one file or folder: `users`, `teams`, and `invitations`, each entry with `id`, the access kind,
  the display `name`, the `right`, and the `status`. Names and every other value are untrusted data; an invitation's
  name can be an e-mail address. A list holds at most 200 entries and a name at most 256 bytes; `truncated` says a
  list was cut. The class of all four tools is `infomaniak-kdrive-access`, separate from file metadata and from link
  URLs, because who may reach a file is personal data.
- `access.grant` sends one `POST /2/drive/{d}/files/{id}/access` with `right`, and any of `user_ids` (at most 20),
  `team_ids` (at most 10), and `emails` (at most 10), at least one of them. `lang` (`de`, `en`, `es`, `fr`, `it`,
  `nl`, `pt`) is required, because Infomaniak sends the invitation mail in it and Qatlas picks no language. No
  `message` is offered. `access.update` and `access.revoke` take exactly one of `user_id` and `team_id`. `right` is
  `read`, `write`, or `manage` (can also share); the API's `none` is not offered, because `access.revoke` removes
  access explicitly. Nothing else of the provider's body exists.
- `file_id` is a positive integer other than the drive's root `1`, checked before any secret is read or request is
  sent; the drive goes through the allow-list check and the live ownership check first, and a refusal names no
  foreign value.
- Every user and team a change names is proven to belong to the same drive before the change request: after the
  ownership check, Qatlas reads the drive's users page by page with their team identifiers (`GET
  /2/drive/{d}/users?with=teams`, 100 per page, at most 10 pages, stopping once everything is found) and counts only
  users whose status is `active`. A user or team that is not among them is an invalid request that does not name the
  value, and nothing is changed. When the bound ends the search without finding it, or the page count is missing,
  the target counts as unproven and is refused the same way instead of being guessed. Infomaniak lists no teams of
  a drive on their own with the `drive` scope, so a team is provable only through an active user of the drive who
  belongs to it; a team without such a user is refused. A grant of only e-mail addresses needs no such read. The
  check costs one request per page; it is a read and sends nothing.
- An e-mail address in `emails` makes Infomaniak send an invitation mail to a person outside the drive, so the tool is
  `open_world`, needs `confirm`, and is never repeated. Each address is checked locally: ASCII, 254 bytes at most, one
  `@`, a plain dotted domain, no space, control or special character, no duplicate. An address is personal data: no
  error and no log quotes one, and the result echoes only what Infomaniak reports per target, up to 254 bytes each.
  The invoke log holds no arguments.
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
  read, and the content request.
- A folder identifier writes Infomaniak's zip archive of it, as with `files.get`. The redirect rules under
  Content and size limits apply unchanged.

## Pagination and cursors

`infomaniakdrive.files.list`, `infomaniakdrive.trash.list`, and `infomaniakdrive.links.list` take `limit` (5 to 1000, default 10) and an opaque `cursor`, and answers
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

`infomaniakdrive.files.get` reads at most 4 MiB and returns it as base64; for a larger file, or to keep the
content out of the answer, use `infomaniakdrive.files.download`, which writes up to 10 GiB to a path instead. Content over that limit is refused
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
invalid request too, see Access. A `drive_id` outside the connection's allow-list, or one the live ownership check finds belongs to another
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
decision: a refusal arrives as `permission` (or `provider-error`) and is never worked around, a share link or dropbox included: which rights, expiry, and settings a plan allows is its decision, and a refusal is not retried with other settings, and the same holds for sharing access with users, teams, and invited people, which a plan or the rights of the token's user may refuse as `permission`, and this
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
creation or removal of users or teams of the drive, no invitation to the drive itself, no handling of access
requests, no synchronisation with the rights of the parent, no resending of an invitation, no access `message`,
no `none` right, no removal of a dropbox setting, no dropbox invitation (`/dropbox/invite`) and no listing or search of dropboxes, no link statistics, no trashed
folder browsing, no restore outside the drive, no copy to another drive, no `version` conflict mode, no search,
no activity or version history, and no account, settings, or quota administration.
