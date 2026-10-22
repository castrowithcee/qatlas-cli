---
description: >
  Describes the Infomaniak kDrive file tools: the default and root identifiers of the file reads, the zip archive of
  a folder, the confirmed folder creation, rename, move, and copy with their identifier, name, and conflict rules, the
  one-shot change contract with its status and cancel handle, the file upload and replacement, and the download to a
  local path.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-22
---

# Infomaniak kDrive files

The file tools of the [Infomaniak kDrive provider](README.md): reads, changes, upload, and download. The binding
checks they pass are described under [Scope](README.md#scope).

`infomaniak.drive.files.list` and `infomaniak.drive.files.stat` default `folder_id` and `file_id` to `1`, the
fixed identifier of a drive's own root directory, as Infomaniak documents it; `files.list` under a subtree
binding defaults as described under [Scope](README.md#scope). `infomaniak.drive.files.get`
requires an explicit `file_id`: reading content is never defaulted to the root. A folder identifier passed to
`files.get` reads Infomaniak's own zip archive of that folder, still bounded by the same size limit, rather
than being refused or silently redirected to a listing.

`depth` is not offered as an argument of `files.list`: the tool lists the immediate children of one folder.

## Changes

- `drive_id`, `file_id`, `parent_id`, and `destination_id` are plain positive integers of at most 18 digits,
  validated before any secret is read or request is sent, and used only as path segments of the fixed paths
  above. An object identifier is bound by the
  drive it is used in: the drive goes through the checks under [Scope](README.md#scope) (the proof of a subtree binding
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
  Only the three trash tools that delete require the tool allow-list, see [Trash](trash.md).
- A change sends exactly one change request after the ownership check, and Qatlas never repeats it. After a
  timeout, a dropped connection, a 5xx answer, or an unreadable answer the error says the change may have
  been applied; read the folder before trying again.
- `status` is `done` when Infomaniak applied the change. When Infomaniak answers `asynchronous`, the change is
  accepted but not finished: `status` is `pending`, and `cancel_id` carries Infomaniak's handle when it
  reports one. A rename or a move answers with a cancel resource (`cancel_id`, `valid_until`) also when it is
  `done`. Qatlas offers no cancel tool. The request and response shapes follow the Infomaniak OpenAPI
  specification (`https://developer.infomaniak.com/openapi.json`).
- The creation and the copy report the new folder or file as `entry`, as untrusted data.


## Upload

- `files.upload` creates one file in an existing folder (`directory_id`, the drive's root is `1`) or replaces
  the content of one existing file as a new version. Replacing needs `file_id` together with its current
  `etag`, sent as `If-Match`, and takes no `directory_id`, `name`, or `conflict`. If the file changed or is
  locked since, the call fails as an etag conflict and nothing is replaced. A missing folder is never created.
- The content comes from `local_path`, a file inside a directory the connection releases for reading, or inline
  from `content_base64` up to 4 MiB with a `name`. Only metadata comes back, never the content.
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

## Download

- `files.download` takes `drive_id`, `file_id`, and `local_path`, an absolute path or one starting with `~/` in
  an existing directory the connection releases for writing. A path outside it is refused before any secret
  is read or request is sent, without naming the path.
- An existing file at the path is kept unless the request is confirmed; with `confirm` it is replaced.
- The content goes to a temporary file that only becomes the target once the transfer is complete and matches
  the reported size, so an aborted or too large transfer leaves no partial file. The result carries only metadata, never
  the content. It costs three requests: the ownership check, a metadata
  read (the proof under a subtree binding), and the content request.
- A folder identifier writes Infomaniak's zip archive of it, as with `files.get`. The redirect rules under
  [Content and size limits](README.md#content-and-size-limits) apply unchanged.
