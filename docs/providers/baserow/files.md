---
description: >
  Describes Baserow file upload and download for file fields, the file address rules, and `url` uploads.
type: knowledge
edit: shared
created: 2026-10-27
updated: 2026-10-28
---

# Files

The table boundary is in [the README](README.md#scope) and the bounds in [Bounds](README.md#bounds).

## Files

Files are addressed only by table (reachable), row, and the name of a field of type `file`; the field must
exist in the table's schema (one field read), and an upload also needs it to be writable. No message quotes a field
name.

`files.upload` takes `table_id`, `row_id`, `field`, and exactly one source:

- `local_path`: a file inside a directory the connection releases for reading (`files.read`); the stored name
  is the name of the local file, and `name` is not allowed.
- `content_base64`: inline content up to 4 MiB, with `name`.
- `url`: an `https` URL that Baserow fetches itself (`upload-via-url`); `name` is optional (Baserow derives one
  from the URL).

It needs `confirm`. The cell is read first; then one upload request (`POST /api/user-files/upload-file/`, or
`upload-via-url`) and one row change (`PATCH`) follow that keep the files the cell held and append the new one.
Neither request is repeated. Baserow has no atomic append, so a change to the same cell between the read and the row
change is overwritten. A failure of the upload, or an unclear result of either request, says that the file may have
been uploaded or attached, and a refused row change says that the file was uploaded but is not attached (an unattached
user file stays on the instance). The answer names the stored file (`id`, `name`, `size`, `sha256`) and never the file
address. Baserow names a stored file `<unique>_<sha256>.<extension>`; for a path or inline upload the hash must equal
the SHA-256 Qatlas computed, otherwise the file is not attached. The size must equal the size sent.

`files.get` takes `table_id`, `row_id`, `field`, and optionally `name` (the shown name or the stored name) or `index`;
a cell with exactly one file needs neither, an ambiguous name is refused. The file is chosen from the cell as it is
now, never from an address in the arguments. Without `local_path` the content comes back as `content_base64` up to 4
MiB; with `local_path`, inside a directory released for writing (`files.write`), it is streamed to the file and only
`id`, `name`, `size`, and `sha256` are returned. An existing file is replaced only with confirmation. The download is
checked against the announced length and against the SHA-256 in the stored name.

Transfers of file content have a timeout of 30 minutes; all other requests keep 30 seconds.

Because both tools declare local file access, a connection offers them only when it lists `files` entries for their
direction (`read` for the upload, `write` for the download), also for inline and `url` transfers.

**File addresses are untrusted.** The address in a cell is used only when it is `https`, has no user info or fragment,
is on exactly the host (and port) of the connection's base URL, and its path is exactly `/media/user_files/<stored
name>` (below the installation path, if any) for the file that was picked. The download sends no token; Baserow serves
stored files under unguessable names, and a signed query, where the instance uses one, is kept and never reported. No
redirect is followed. A file that the instance serves from another host, such as an object store or a CDN, cannot be
shown to belong to the instance and is refused; such files cannot be downloaded with this tool.

**`url` uploads.** Only `https` URLs on a public DNS host name are accepted: no user info, no port, no fragment, no
numeric address (decimal, hexadecimal, or dotted, IPv4 or IPv6), ASCII labels only (internationalised names in `xn--`
form), at least two labels with an alphabetic top-level label, and none of the names reserved for internal use
(`localhost`, `local`, `internal`, `intranet`, `lan`, `home`, `corp`, `private`, `test`, `example`, `invalid`,
`onion`, `arpa`, `alt`, `localdomain`). The URL never appears in a message. This list is syntactic: Qatlas does not
resolve the name, because Baserow, not Qatlas, fetches the URL. A public name that resolves to a private address is
stopped by Baserow's own address guard (the `advocate` library in Baserow 2.4.0, which also checks redirects), and
this holds only as long as the instance does not relax it. Do not release `url` uploads on a self-hosted instance
whose network an untrusted agent must not reach.
