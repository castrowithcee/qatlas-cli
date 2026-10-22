---
description: >
  Describes SeaTable Base file and image cell operations, local file access, and transfer links.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Cell files

`seatable.base.files.upload`, `seatable.base.files.get`, and `seatable.base.files.delete` attach, read, and delete the files
and images of a `file` or `image` column of one row of an allowed table. None of them is in a profile.
`files.delete` is offered only by a connection whose `tools` list names it. The
shared rules are in [the README](README.md#shared-boundaries).

- The token exchange and the metadata read are the only provider access that can precede a refusal such as a
  column of another type.
- `files.get` and `files.delete` address an entry of the current cell with `name` (file name) or `index`
  (position, starting at 0). Qatlas reads the row first and takes the asset path from the cell value of
  that row; a path is never an argument. Without a selector, `files.get` uses the only entry of the cell;
  `files.delete` always needs one. A name that occurs twice needs `index`. An entry that is not a file or
  image stored by SeaTable in this base, such as a custom-folder asset or an address of another server,
  is refused.
- `files.upload` takes the file from `local_path` or inline from `content_base64` (up to 4 MiB) together
  with `name`; the two sources exclude each other. It asks for an upload link, sends the file in exactly one
  upload request, and appends the entry to the cell with exactly one row update. The cell is read before, so
  a change to the same cell between the read and the update is overwritten. SeaTable may rename the file to
  keep names in the month folder unique; the result reports the stored name.
- `files.get` returns the content in `content_base64` up to 4 MiB, or writes it to `local_path`. A larger
  file needs `local_path`.
- `files.delete` deletes the stored asset of the entry. The entry stays in the cell; removing it is a row
  update, and Qatlas does not delete an asset when an entry leaves a cell.
- Local files follow the `files` setting of the connection: `files.read` is a list of directories the
  upload may read, `files.write` a list of directories the download may write. Both tools declare local
  file access, so a connection without a matching `files` entry is not offered `files.upload` (`read`) or
  `files.get` (`write`) at all, and inline content is usable only on a connection that releases a
  directory. A download writes only into an existing directory of the release, through a temporary file,
  checks the announced size, and replaces an existing file only with `confirm`; the replacement is logged as
  an audit event without the path. Messages never name a path.
- SeaTable hands out the upload and download address as a link that works without a credential. Qatlas
  uses it only when its host is the configured service, never follows a redirect, sends no credential to
  it, and never reports it: it appears neither in results, errors, logs, nor audit events. Content
  transfers have a timeout of 30 minutes; every other request keeps 30 seconds.
- `files.upload` needs `create`; `files.delete` needs `delete`. If the upload succeeded but the update did
  not, the file stays stored without an entry. Read the cell before repeating an uncertain change.
- Folders other than the automatic month folders of files and images, and the asset endpoints of the account,
  are not offered.

```sh
qatlas invoke seatable.base.files.upload --connection sales-rw --confirm --arg table=id:0000 \
  --arg row_id=Qtf7xPmoRaiFyQPO1aENTj --arg column=Anhang --arg local_path=~/uploads/offer.pdf
qatlas invoke seatable.base.files.get --connection sales-rw --arg table=id:0000 \
  --arg row_id=Qtf7xPmoRaiFyQPO1aENTj --arg column=Anhang --arg name=offer.pdf --arg local_path=~/downloads/offer.pdf
```
