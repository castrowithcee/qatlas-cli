---
description: >
  Describes the Infomaniak kDrive sharing tools: the share link reads and confirmed deletion with their URL
  handling, the dropbox (upload link) read and confirmed deletion, and the access read and confirmed granting,
  changing, and revoking of user and team access with the membership check and personal data handling.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-22
---

# Infomaniak kDrive sharing

The share link, dropbox, and access tools of the [Infomaniak kDrive provider](README.md). The effects and
allow-list requirements are listed in the [tool overview](README.md#tools).

## Share links

- A share link makes a file or folder reachable by its URL. `right` decides who may open it: `inherit` only
  users of the drive (the narrowest), `password` anyone with the URL and a password, `public` anyone holding the
  URL with no further check. Deleting a link needs `confirm` for every right, `public` included, and a
  connection must list the tool by name besides holding the permission.
- `file_id` is a positive integer other than the drive's root `1`, validated, like `drive_id`, before any
  secret is read or request is sent; the checks under [Scope](README.md#scope) follow, and a refusal names no foreign value. Qatlas does not offer a link for the root: whether Infomaniak
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

## Dropbox

- A dropbox is a public upload link for one folder: anyone holding its URL can upload files into the folder
  without an account. Creating or changing one would open or prolong such an access to the outside, so neither is
  offered. The tools `dropbox.get` and `dropbox.delete` act
  on exactly one folder (`file_id`, a positive integer other than the drive's root `1`) of one drive, which goes
  through the checks under [Scope](README.md#scope) first; a foreign `drive_id` is refused locally,
  without naming it, before any secret is read or request is sent. Both need an explicit tools list; the
  deletion needs `confirm`.
- The dropbox has its own sensitivity class, `infomaniak-kdrive-dropboxes`. Every reported dropbox value is untrusted data and
  bounded; the `url` is returned only when it is an
  absolute `https` URL without credentials of at most 1024 characters. Other fields Infomaniak documents (the
  creator, the containing directory and user via `with`, and the remaining time, size, and usage figures) are not
  read. `dropbox.get` answers `not-found` for a folder without a dropbox; `dropbox.delete`
  answers only its `status`.
- The deletion sends exactly one request after the ownership check and is never repeated. After a timeout, a dropped
  connection, a 5xx answer, or an unreadable answer, the error says the change may have been applied; read the
  dropbox before trying again. `asynchronous` is a `pending` change. A 403, for example because the drive's plan
  does not include dropboxes, is reported as `permission`, and a provider failure as `provider-error`; neither is
  retried.

## Access

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
  sent; the checks under [Scope](README.md#scope) follow, and a refusal names no foreign value.
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
